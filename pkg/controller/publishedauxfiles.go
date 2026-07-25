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
	"fmt"
	"log/slog"
	"path"
	"sync"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"gitlab.com/haproxy-haptic/haptic/pkg/compression"
	"gitlab.com/haproxy-haptic/haptic/pkg/k8s/client"
	"gitlab.com/haproxy-haptic/haptic/pkg/k8s/types"
	"gitlab.com/haproxy-haptic/haptic/pkg/k8s/watcher"
)

// publishedAuxCRD describes one of HAPTIC's published auxiliary-file CRD kinds
// that feeds `currentFiles`. All three carry `spec.path` and `spec.compressed`;
// only the content field name differs, so a single generic extractor handles
// them keyed uniformly by base filename.
type publishedAuxCRD struct {
	gvr          schema.GroupVersionResource
	contentField string // spec field holding the file content
}

// publishedAuxCRDList lists the CRD-backed aux files exposed via `currentFiles`.
// SSL certificates (published as Secrets, content includes private keys) and CA
// files are deliberately excluded — see currentAuxFilesProvider.
func publishedAuxCRDList() []publishedAuxCRD {
	return []publishedAuxCRD{
		{haproxyMapFileGVR, "entries"},
		{haproxyGeneralFileGVR, "content"},
		{haproxyCRTListFileGVR, "entries"},
	}
}

// publishedAuxFiles is a thread-safe snapshot of the controller's published
// auxiliary-file CRDs (base filename → decompressed content), merged across the
// aux CRD kinds. Each kind's watcher owns its own slot so refreshing one kind
// never clobbers another. It is the aux-file analogue of currentconfigstore.Store:
// watchers keep it in sync so the `currentFiles` render input survives a
// controller restart or config reload, and stays current for a follower later
// promoted to leader.
type publishedAuxFiles struct {
	mu    sync.RWMutex
	byGVR map[string]map[string]string
}

func newPublishedAuxFiles() *publishedAuxFiles {
	return &publishedAuxFiles{byGVR: map[string]map[string]string{}}
}

// get returns the merged snapshot across all aux kinds. Base-filename collisions
// across kinds (rare — extensions differ) resolve in publishedAuxCRDList order.
func (p *publishedAuxFiles) get() map[string]string {
	p.mu.RLock()
	defer p.mu.RUnlock()

	merged := map[string]string{}
	for _, kind := range publishedAuxCRDList() {
		for name, content := range p.byGVR[kind.gvr.String()] {
			merged[name] = content
		}
	}
	return merged
}

func (p *publishedAuxFiles) setForGVR(gvr string, files map[string]string) {
	p.mu.Lock()
	p.byGVR[gvr] = files
	p.mu.Unlock()
}

// setupPublishedAuxFilesStore starts a silent watcher over each aux-file CRD kind
// (HAPTIC's own published output) and returns a snapshot that stays in sync with
// them. It waits for each initial sync so the snapshot is populated before the
// first render. The watchers publish no events — they only refresh the snapshot —
// so they cannot trigger a reconcile loop against the files the controller itself
// publishes (the same contract as the HAProxyCfg watcher in
// setupCurrentConfigStore).
func setupPublishedAuxFilesStore(
	setup *componentSetup,
	k8sClient *client.Client,
	logger *slog.Logger,
) (*publishedAuxFiles, error) {
	store := newPublishedAuxFiles()

	for _, kind := range publishedAuxCRDList() {
		gvrKey := kind.gvr.String()
		refresh := func(s types.Store, _ types.ChangeStats) {
			store.setForGVR(gvrKey, auxFilesFromStore(s, kind.contentField, logger))
		}

		w, err := watcher.New(types.WatcherConfig{
			GVR:       kind.gvr,
			Namespace: k8sClient.Namespace(),
			IndexBy:   []string{"metadata.name"},
			StoreType: types.StoreTypeMemory,
			OnChange:  refresh,
			OnSyncComplete: func(s types.Store, _ int) {
				store.setForGVR(gvrKey, auxFilesFromStore(s, kind.contentField, logger))
			},
		}, k8sClient, logger)
		if err != nil {
			return nil, fmt.Errorf("creating %s watcher: %w", kind.gvr.Resource, err)
		}

		startInErrGroup(setup.ErrGroup, setup.IterCtx, logger, setup.Cancel, kind.gvr.Resource+" watcher", w.Start)
		if _, err := w.WaitForSync(setup.IterCtx); err != nil {
			return nil, fmt.Errorf("%s watcher sync failed: %w", kind.gvr.Resource, err)
		}
	}

	return store, nil
}

// auxFilesFromStore flattens one aux CRD kind's stored objects into
// base-filename → content, reading content from the kind's contentField and
// decompressing any zstd+base64 value. An object that can't be read or decoded
// is skipped (logged), never surfaced as an empty or garbled value.
func auxFilesFromStore(s types.Store, contentField string, logger *slog.Logger) map[string]string {
	items, err := s.List()
	if err != nil {
		logger.Warn("Listing published aux files failed; currentFiles fallback may be stale",
			"field", contentField, "error", err)
		return map[string]string{}
	}

	files := make(map[string]string, len(items))
	for _, item := range items {
		obj, ok := unstructuredMap(item)
		if !ok {
			continue
		}
		filePath, _, _ := unstructured.NestedString(obj, "spec", "path")
		if filePath == "" {
			continue
		}
		content, _, _ := unstructured.NestedString(obj, "spec", contentField)
		compressed, _, _ := unstructured.NestedBool(obj, "spec", "compressed")
		if compressed {
			decoded, derr := compression.Decompress(content)
			if derr != nil {
				logger.Warn("Skipping compressed aux file that failed to decompress",
					"file", filePath, "error", derr)
				continue
			}
			content = decoded
		}
		files[path.Base(filePath)] = content
	}
	return files
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
