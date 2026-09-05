// Copyright 2025 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package controller

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"path"
	"slices"
	"sync"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"gitlab.com/haproxy-haptic/haptic/pkg/compression"
	"gitlab.com/haproxy-haptic/haptic/pkg/k8s/client"
	"gitlab.com/haproxy-haptic/haptic/pkg/k8s/configpublisher"
	"gitlab.com/haproxy-haptic/haptic/pkg/k8s/types"
	"gitlab.com/haproxy-haptic/haptic/pkg/k8s/watcher"
)

const (
	secretKind      = "Secret"
	secretDataField = "data"
)

// publishedAuxCRD describes one child kind committed by HAProxyCfg status.
type publishedAuxCRD struct {
	gvr             schema.GroupVersionResource
	kind            string
	referenceFields []string
	contentField    string
}

func publishedAuxCRDList() []publishedAuxCRD {
	return []publishedAuxCRD{
		{haproxyMapFileGVR, "HAProxyMapFile", []string{"mapFiles"}, "entries"},
		{secretGVR, secretKind, []string{"sslCertificates", "sslCaFiles"}, ""},
		{haproxyGeneralFileGVR, "HAProxyGeneralFile", []string{"generalFiles"}, "content"},
		{haproxyCRTListFileGVR, "HAProxyCRTListFile", []string{"crtListFiles"}, "entries"},
	}
}

type publishedAuxFile struct {
	path            string
	content         string
	setID           string
	checksum        string
	resourceVersion string
	caFile          bool
}

type publishedAuxRef struct {
	name      string
	namespace string
}

type publishedAuxCommit struct {
	setID string
	refs  map[string][]publishedAuxRef
}

type publishedStoreSyncer interface {
	WaitForSync(context.Context) (int, error)
	Store() types.Store
}

// publishedAuxFiles advances only when one complete parent reference set resolves.
type publishedAuxFiles struct {
	mu sync.RWMutex

	namespace      string
	commit         *publishedAuxCommit
	byGVR          map[string]map[string]publishedAuxFile
	current        map[string]string
	currentRoot    *currentAuxFilesMapRoot
	ready          bool
	legacy         bool
	leader         bool
	modernAccepted bool
	lastErr        error
	unavailable    error
}

func newPublishedAuxFiles(namespace string) *publishedAuxFiles {
	empty := map[string]string{}
	root, err := newCurrentAuxFilesMapRoot(empty)
	if err != nil {
		panic(err)
	}
	return &publishedAuxFiles{
		namespace:   namespace,
		byGVR:       map[string]map[string]publishedAuxFile{},
		current:     root.files,
		currentRoot: root,
	}
}

func (p *publishedAuxFiles) get() (map[string]string, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if p.unavailable != nil {
		return nil, p.unavailable
	}
	if p.currentRoot == nil || p.currentRoot.files == nil {
		return nil, errors.New("published auxiliary root is unavailable")
	}
	return maps.Clone(p.currentRoot.files), nil
}

func (p *publishedAuxFiles) availabilityError() error {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.unavailable
}

func (p *publishedAuxFiles) setForGVR(gvr string, files map[string]publishedAuxFile) {
	p.mu.Lock()
	defer p.mu.Unlock()

	legacyChanged := p.legacy && p.legacyReferencesChanged(gvr, files)
	p.byGVR[gvr] = files
	if legacyChanged && !p.leader && (p.commit == nil || p.commit.setID == "") {
		p.markLegacyUnavailable()
		return
	}
	p.advanceLocked()
}

func (p *publishedAuxFiles) setCommit(commit *publishedAuxCommit) {
	p.mu.Lock()
	defer p.mu.Unlock()

	legacyChanged := p.legacy && !publishedAuxCommitsEqual(p.commit, commit)
	p.commit = commit
	if p.modernAccepted && (commit == nil || commit.setID == "") {
		p.legacy = false
		p.markModernDowngradeUnavailable()
		return
	}
	if legacyChanged && !p.leader && (commit == nil || commit.setID == "") {
		p.markLegacyUnavailable()
		return
	}
	p.advanceLocked()
}

func (p *publishedAuxFiles) setError(err error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.lastErr = err
	if p.legacy && !p.leader {
		p.markLegacyUnavailable()
	}
}

// beginLeaderTerm lifts the legacy latch. Only a leader publishes, so once this
// replica leads no old-version leader can still mutate a set-ID-less set: accept
// the visible legacy snapshot (as a fresh process would) rather than latching
// until a restart; the term's first publish replaces it with a set-ID-bearing one.
func (p *publishedAuxFiles) beginLeaderTerm() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.leader = true
	if p.unavailable != nil && !p.modernAccepted {
		p.unavailable = nil
		p.advanceLocked()
	}
}

func (p *publishedAuxFiles) endLeaderTerm() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.leader = false
}

func (p *publishedAuxFiles) markLegacyUnavailable() {
	p.unavailable = errors.New("legacy auxiliary publication changed without a set ID; currentFiles is unavailable until a new set is committed")
}

func (p *publishedAuxFiles) markModernDowngradeUnavailable() {
	p.unavailable = errors.New("auxiliary publication lost its set ID; currentFiles is unavailable until a new set is committed")
}

func (p *publishedAuxFiles) legacyReferencesChanged(gvr string, files map[string]publishedAuxFile) bool {
	if p.commit == nil {
		return false
	}
	previous := p.byGVR[gvr]
	for _, kind := range publishedAuxCRDList() {
		if kind.gvr.String() != gvr {
			continue
		}
		for _, referenceField := range kind.referenceFields {
			for _, ref := range p.commit.refs[referenceField] {
				before, beforeExists := previous[ref.name]
				after, afterExists := files[ref.name]
				if beforeExists != afterExists || before != after {
					return true
				}
			}
		}
	}
	return false
}

func publishedAuxCommitsEqual(a, b *publishedAuxCommit) bool {
	if a == nil || b == nil {
		return a == b
	}
	if a.setID != b.setID {
		return false
	}
	for _, kind := range publishedAuxCRDList() {
		for _, referenceField := range kind.referenceFields {
			if !slices.Equal(a.refs[referenceField], b.refs[referenceField]) {
				return false
			}
		}
	}
	return true
}

func (p *publishedAuxFiles) advanceLocked() {
	if p.commit == nil {
		if p.unavailable != nil {
			return
		}
		p.setCurrentLocked(map[string]string{})
		p.ready = true
		p.legacy = false
		p.lastErr = nil
		return
	}
	if p.commit.setID == "" && p.unavailable != nil {
		return
	}

	next, err := p.resolveCommitLocked()
	if err != nil {
		p.lastErr = err
		return
	}
	p.setCurrentLocked(next)
	p.ready = true
	p.legacy = p.commit.setID == ""
	p.lastErr = nil
	if !p.legacy {
		p.modernAccepted = true
		p.unavailable = nil
	}
}

func (p *publishedAuxFiles) setCurrentLocked(next map[string]string) {
	root, err := retainCurrentAuxFilesMapRoot(p.currentRoot, next)
	if err != nil {
		p.lastErr = err
		return
	}
	p.current = root.files
	p.currentRoot = root
}

func (p *publishedAuxFiles) resolveCommitLocked() (map[string]string, error) {
	if ref, aliased := p.aliasedSecretReference(); aliased {
		return nil, fmt.Errorf("certificate and CA references alias Secret %s/%s", ref.namespace, ref.name)
	}
	next := map[string]string{}
	var legacySetID *string
	kinds := publishedAuxCRDList()
	for i := range kinds {
		kind := &kinds[i]
		for _, referenceField := range kind.referenceFields {
			for _, ref := range p.commit.refs[referenceField] {
				file, err := p.resolvePublishedFile(kind, ref)
				if err != nil {
					return nil, err
				}
				if err := validatePublishedSetID(p.commit.setID, &legacySetID, file.setID); err != nil {
					return nil, fmt.Errorf("committed %s %s/%s: %w", kind.kind, p.namespace, ref.name, err)
				}
				if kind.contentField != "" && !file.caFile {
					next[path.Base(file.path)] = file.content
				}
			}
		}
	}
	return next, nil
}

func (p *publishedAuxFiles) aliasedSecretReference() (publishedAuxRef, bool) {
	if len(p.commit.refs["sslCertificates"]) == 0 || len(p.commit.refs["sslCaFiles"]) == 0 {
		return publishedAuxRef{}, false
	}
	certificates := make(map[publishedAuxRef]struct{}, len(p.commit.refs["sslCertificates"]))
	for _, ref := range p.commit.refs["sslCertificates"] {
		if ref.namespace == "" {
			ref.namespace = p.namespace
		}
		certificates[ref] = struct{}{}
	}
	for _, ref := range p.commit.refs["sslCaFiles"] {
		if ref.namespace == "" {
			ref.namespace = p.namespace
		}
		if _, exists := certificates[ref]; exists {
			return ref, true
		}
	}
	return publishedAuxRef{}, false
}

func (p *publishedAuxFiles) resolvePublishedFile(kind *publishedAuxCRD, ref publishedAuxRef) (publishedAuxFile, error) {
	if ref.namespace != "" && ref.namespace != p.namespace {
		return publishedAuxFile{}, fmt.Errorf("committed %s %s/%s is outside namespace %s", kind.kind, ref.namespace, ref.name, p.namespace)
	}
	file, ok := p.byGVR[kind.gvr.String()][ref.name]
	if !ok {
		return publishedAuxFile{}, fmt.Errorf("committed %s %s/%s is unavailable", kind.kind, p.namespace, ref.name)
	}
	return file, nil
}

func validatePublishedSetID(want string, legacy **string, got string) error {
	if want != "" {
		if got != want {
			return fmt.Errorf("belongs to auxiliary set %q, want %q", got, want)
		}
		return nil
	}
	if *legacy == nil {
		legacySetID := got
		*legacy = &legacySetID
		return nil
	}
	if **legacy != got {
		return errors.New("legacy references span multiple auxiliary sets")
	}
	return nil
}

func (p *publishedAuxFiles) readinessError() error {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if p.lastErr != nil {
		return p.lastErr
	}
	if p.unavailable != nil {
		return p.unavailable
	}
	if p.ready {
		return nil
	}
	return fmt.Errorf("committed auxiliary snapshot is unavailable")
}

// setupPublishedAuxFilesStore starts a silent watcher over each aux-file CRD kind
// (HAPTIC's own published output) and returns a snapshot that stays in sync with
// them. It waits for each initial sync so the snapshot is populated before the
// first render. The watchers publish no events — they only refresh the snapshot —
// so they cannot trigger a reconcile loop against the files the controller itself
// publishes.
func setupPublishedAuxFilesStore(
	setup *componentSetup,
	k8sClient *client.Client,
	crdName string,
	logger *slog.Logger,
) (*publishedAuxFiles, error) {
	store := newPublishedAuxFiles(k8sClient.Namespace())
	runtimeConfigName := configpublisher.GenerateRuntimeConfigName(crdName)

	kinds := publishedAuxCRDList()
	for i := range kinds {
		kind := &kinds[i]
		gvrKey := kind.gvr.String()
		refresh := func(s types.Store, _ types.ChangeStats) {
			files, err := publishedAuxFilesFromStore(s, kind)
			if err != nil {
				logger.Warn("Reading published auxiliary files failed", "kind", kind.kind, "error", err)
				store.setError(err)
				return
			}
			store.setForGVR(gvrKey, files)
		}

		watcherConfig := types.WatcherConfig{
			GVR:           kind.gvr,
			Namespace:     k8sClient.Namespace(),
			IndexBy:       []string{metadataNameIndex},
			StoreType:     types.StoreTypeMemory,
			LabelSelector: configpublisher.RuntimeConfigLabelSelector(runtimeConfigName),
			OnChange:      refresh,
			OnSyncComplete: func(s types.Store, _ int) {
				refresh(s, types.ChangeStats{})
			},
		}
		if kind.contentField == "" {
			watcherConfig.IgnoreFields = []string{secretDataField, "stringData", "type", "immutable", "metadata.managedFields"}
		}
		w, err := watcher.New(watcherConfig, k8sClient, logger)
		if err != nil {
			return nil, fmt.Errorf("creating %s watcher: %w", kind.gvr.Resource, err)
		}

		startInErrGroup(setup.ErrGroup, setup.IterCtx, logger, setup.Cancel, kind.gvr.Resource+" watcher", w.Start)
		if err := syncAndRefreshPublishedStore(setup.IterCtx, w, refresh); err != nil {
			return nil, fmt.Errorf("%s watcher sync failed: %w", kind.gvr.Resource, err)
		}
	}

	refreshCommit := func(s types.Store, _ types.ChangeStats) {
		commit, found, err := publishedAuxCommitFromStore(s, runtimeConfigName)
		if err != nil {
			logger.Warn("Reading committed auxiliary references failed", "error", err)
			store.setError(err)
			return
		}
		if !found {
			commit = nil
		}
		store.setCommit(commit)
	}
	runtimeConfigWatcher, err := watcher.New(types.WatcherConfig{
		GVR:       haproxyCfgGVR,
		Namespace: k8sClient.Namespace(),
		IndexBy:   []string{metadataNameIndex},
		StoreType: types.StoreTypeMemory,
		OnChange:  refreshCommit,
		OnSyncComplete: func(s types.Store, _ int) {
			refreshCommit(s, types.ChangeStats{})
		},
	}, k8sClient, logger)
	if err != nil {
		return nil, fmt.Errorf("creating %s watcher: %w", haproxyCfgGVR.Resource, err)
	}
	startInErrGroup(setup.ErrGroup, setup.IterCtx, logger, setup.Cancel, haproxyCfgGVR.Resource+" currentFiles watcher", runtimeConfigWatcher.Start)
	if err := syncAndRefreshPublishedStore(setup.IterCtx, runtimeConfigWatcher, refreshCommit); err != nil {
		return nil, fmt.Errorf("%s currentFiles watcher sync failed: %w", haproxyCfgGVR.Resource, err)
	}
	if err := store.readinessError(); err != nil {
		return nil, fmt.Errorf("loading committed auxiliary snapshot: %w", err)
	}

	return store, nil
}

func syncAndRefreshPublishedStore(
	ctx context.Context,
	w publishedStoreSyncer,
	refresh func(types.Store, types.ChangeStats),
) error {
	if _, err := w.WaitForSync(ctx); err != nil {
		return err
	}
	refresh(w.Store(), types.ChangeStats{})
	return nil
}

func publishedAuxFilesFromStore(s types.Store, kind *publishedAuxCRD) (map[string]publishedAuxFile, error) {
	items, err := s.List()
	if err != nil {
		return nil, fmt.Errorf("listing resources: %w", err)
	}

	files := make(map[string]publishedAuxFile, len(items))
	for _, item := range items {
		obj, ok := unstructuredMap(item)
		if !ok {
			continue
		}
		name, file, found, err := publishedAuxFileFromObject(obj, kind)
		if err != nil {
			return nil, err
		}
		if !found {
			continue
		}
		files[name] = file
	}
	return files, nil
}

func publishedAuxFileFromObject(obj map[string]any, kind *publishedAuxCRD) (name string, file publishedAuxFile, found bool, err error) {
	name, _, err = unstructured.NestedString(obj, "metadata", "name")
	if err != nil {
		return "", publishedAuxFile{}, false, fmt.Errorf("reading resource name: %w", err)
	}
	if name == "" {
		return "", publishedAuxFile{}, false, nil
	}
	setID, _, err := unstructured.NestedString(obj, "metadata", "annotations", configpublisher.AuxiliarySetIDAnnotationKey)
	if err != nil {
		return "", publishedAuxFile{}, false, fmt.Errorf("reading %s auxiliary set ID: %w", name, err)
	}
	if kind.contentField == "" {
		checksum, _, err := unstructured.NestedString(obj, "metadata", "annotations", configpublisher.AuxiliaryChecksumAnnotationKey)
		if err != nil {
			return "", publishedAuxFile{}, false, fmt.Errorf("reading %s checksum: %w", name, err)
		}
		resourceVersion, _, err := unstructured.NestedString(obj, "metadata", "resourceVersion")
		if err != nil {
			return "", publishedAuxFile{}, false, fmt.Errorf("reading %s resource version: %w", name, err)
		}
		return name, publishedAuxFile{
			setID:           setID,
			checksum:        checksum,
			resourceVersion: resourceVersion,
		}, true, nil
	}
	filePath, _, err := unstructured.NestedString(obj, "spec", "path")
	if err != nil {
		return "", publishedAuxFile{}, false, fmt.Errorf("reading %s path: %w", name, err)
	}
	if filePath == "" {
		return "", publishedAuxFile{}, false, nil
	}
	content, found, err := unstructured.NestedString(obj, "spec", kind.contentField)
	if err != nil {
		return "", publishedAuxFile{}, false, fmt.Errorf("reading %s content field %s: %w", name, kind.contentField, err)
	}
	if !found {
		return "", publishedAuxFile{}, false, fmt.Errorf("%s has no content field %s", name, kind.contentField)
	}
	compressed, _, err := unstructured.NestedBool(obj, "spec", "compressed")
	if err != nil {
		return "", publishedAuxFile{}, false, fmt.Errorf("reading %s compression flag: %w", name, err)
	}
	if compressed {
		content, err = compression.Decompress(content)
		if err != nil {
			return "", publishedAuxFile{}, false, fmt.Errorf("decompressing %s: %w", filePath, err)
		}
	}
	caFile, _, err := unstructured.NestedBool(obj, "spec", "caFile")
	if err != nil {
		return "", publishedAuxFile{}, false, fmt.Errorf("reading %s CA file flag: %w", name, err)
	}
	return name, publishedAuxFile{path: filePath, content: content, setID: setID, caFile: caFile}, true, nil
}

func publishedAuxCommitFromStore(s types.Store, runtimeConfigName string) (*publishedAuxCommit, bool, error) {
	items, err := s.List()
	if err != nil {
		return nil, false, fmt.Errorf("listing runtime configs: %w", err)
	}

	for _, item := range items {
		obj, ok := unstructuredMap(item)
		if !ok {
			continue
		}
		name, _, err := unstructured.NestedString(obj, "metadata", "name")
		if err != nil {
			return nil, false, fmt.Errorf("reading runtime config name: %w", err)
		}
		if name != runtimeConfigName {
			continue
		}
		commit, err := publishedAuxCommitFromObject(obj, runtimeConfigName)
		return commit, true, err
	}

	return nil, false, nil
}

func publishedAuxCommitFromObject(obj map[string]any, runtimeConfigName string) (*publishedAuxCommit, error) {
	aux, found, err := unstructured.NestedMap(obj, "status", "auxiliaryFiles")
	if err != nil {
		return nil, fmt.Errorf("reading %s status: %w", runtimeConfigName, err)
	}
	if !found {
		return &publishedAuxCommit{refs: map[string][]publishedAuxRef{}}, nil
	}

	commit := &publishedAuxCommit{refs: map[string][]publishedAuxRef{}}
	commit.setID, _, err = unstructured.NestedString(aux, "setID")
	if err != nil {
		return nil, fmt.Errorf("reading committed auxiliary set ID: %w", err)
	}
	for _, kind := range publishedAuxCRDList() {
		for _, referenceField := range kind.referenceFields {
			refs, err := publishedAuxRefs(aux, &kind, referenceField)
			if err != nil {
				return nil, err
			}
			commit.refs[referenceField] = refs
		}
	}
	return commit, nil
}

func publishedAuxRefs(aux map[string]any, kind *publishedAuxCRD, referenceField string) ([]publishedAuxRef, error) {
	rawRefs, _, err := unstructured.NestedSlice(aux, referenceField)
	if err != nil {
		return nil, fmt.Errorf("reading committed %s references: %w", kind.kind, err)
	}
	refs := make([]publishedAuxRef, 0, len(rawRefs))
	for _, raw := range rawRefs {
		ref, ok := raw.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("committed %s reference is malformed", kind.kind)
		}
		parsed, err := publishedAuxRefFromObject(ref, kind)
		if err != nil {
			return nil, err
		}
		refs = append(refs, parsed)
	}
	return refs, nil
}

func publishedAuxRefFromObject(ref map[string]any, kind *publishedAuxCRD) (publishedAuxRef, error) {
	refKind, _, err := unstructured.NestedString(ref, "kind")
	if err != nil {
		return publishedAuxRef{}, fmt.Errorf("reading committed %s reference kind: %w", kind.kind, err)
	}
	refName, _, err := unstructured.NestedString(ref, "name")
	if err != nil {
		return publishedAuxRef{}, fmt.Errorf("reading committed %s reference name: %w", kind.kind, err)
	}
	refNamespace, _, err := unstructured.NestedString(ref, "namespace")
	if err != nil {
		return publishedAuxRef{}, fmt.Errorf("reading committed %s reference namespace: %w", kind.kind, err)
	}
	if refKind != kind.kind || refName == "" {
		return publishedAuxRef{}, fmt.Errorf("committed %s reference has kind %q and name %q", kind.kind, refKind, refName)
	}
	return publishedAuxRef{name: refName, namespace: refNamespace}, nil
}

// unstructuredMap returns the underlying object map for a stored resource,
// whether the store holds it as *unstructured.Unstructured or a bare map.
func unstructuredMap(item any) (map[string]any, bool) {
	switch v := item.(type) {
	case *unstructured.Unstructured:
		return v.Object, true
	case map[string]any:
		return v, true
	default:
		return nil, false
	}
}
