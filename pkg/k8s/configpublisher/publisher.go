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

package configpublisher

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"log/slog"
	"path"
	"slices"
	"sync"
	"time"

	haproxyv1alpha1 "gitlab.com/haproxy-haptic/haptic/pkg/apis/haproxytemplate/v1alpha1"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/auxiliaryfiles"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/renderoutput"
	"gitlab.com/haproxy-haptic/haptic/pkg/generated/clientset/versioned"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// Publisher publishes HAProxy runtime configuration as Kubernetes resources.
//
// This is a pure component (no EventBus dependency) that creates and updates
// HAProxyCfg, HAProxyMapFile, and Secret resources to expose the
// actual runtime configuration applied to HAProxy pods.
//
// When listers are provided, the Publisher uses informer-backed caches for
// initial reads, significantly reducing API calls for status updates.
type Publisher struct {
	k8sClient kubernetes.Interface
	crdClient versioned.Interface
	logger    *slog.Logger

	// listers provide informer-backed cached reads (optional, may be nil).
	// When set, status updates first check the cache to determine if an update
	// is needed, avoiding unnecessary API GETs.
	listers *Listers

	// auxStamps elides per-pod status re-stamps on auxiliary-file CRs whose
	// value is unchanged (see aux_stamp_cache.go). Zero value is ready to use.
	auxStamps auxStampCache

	// publishedMu guards published, the last successful publication per
	// (namespace, template, suffix) key. With republishInterval > 0 an
	// unchanged republish inside the interval returns the recorded result
	// without any API call — the same elision contract as auxStampCache one
	// level up: only a write of provably identical content is skipped, and
	// the interval-expiry republish stays the authoritative self-heal for
	// out-of-band child deletions. Zero interval disables skipping, so every
	// constructor that does not opt in keeps the old behavior.
	publishedMu       sync.Mutex
	published         map[publishedKey]publishedState
	republishInterval time.Duration
}

// publishedKey identifies one publication target.
type publishedKey struct {
	namespace  string
	name       string
	nameSuffix string
}

// publishedState is what the last successful PublishConfig wrote for a key.
// A request matches iff every content-bearing field is equal; the owner UID is
// content here because it lands in every child's ownerReferences.
type publishedState struct {
	ownerUID             string
	setID                string
	checksum             string
	config               string
	configPath           string
	validationError      string
	compressionThreshold int64
	at                   time.Time
	result               PublishResult
}

// SetRepublishInterval enables unchanged-republish skipping for at most d per
// key. Pass the drift-prevention interval so the publish self-heal cadence
// matches the deployment one; zero disables skipping.
func (p *Publisher) SetRepublishInterval(d time.Duration) {
	p.publishedMu.Lock()
	defer p.publishedMu.Unlock()
	p.republishInterval = d
}

// clonePublishResult deep-copies the slices so a consumer mutating a returned
// result cannot poison the cached one (and vice versa).
func clonePublishResult(r *PublishResult) PublishResult {
	out := *r
	out.MapFileNames = slices.Clone(r.MapFileNames)
	out.SecretNames = slices.Clone(r.SecretNames)
	out.SSLCaFileNames = slices.Clone(r.SSLCaFileNames)
	out.GeneralFileNames = slices.Clone(r.GeneralFileNames)
	out.CRTListFileNames = slices.Clone(r.CRTListFileNames)
	return out
}

// skippableResult returns the recorded result when the canonicalized request
// matches the last successful publication for its key inside the republish
// interval. Must be called with the CANONICAL request (auxiliarySetID set).
func (p *Publisher) skippableResult(req *PublishRequest) (*PublishResult, bool) {
	p.publishedMu.Lock()
	defer p.publishedMu.Unlock()
	if p.republishInterval <= 0 || req.Force {
		return nil, false
	}
	key := publishedKey{namespace: req.TemplateConfigNamespace, name: req.TemplateConfigName, nameSuffix: req.NameSuffix}
	state, ok := p.published[key]
	if !ok || time.Since(state.at) >= p.republishInterval {
		return nil, false
	}
	if state.ownerUID != string(req.TemplateConfigUID) ||
		state.setID != req.auxiliarySetID ||
		state.checksum != req.Checksum ||
		state.config != req.Config ||
		state.configPath != req.ConfigPath ||
		state.validationError != req.ValidationError ||
		state.compressionThreshold != req.CompressionThreshold {
		return nil, false
	}
	result := clonePublishResult(&state.result)
	return &result, true
}

// recordPublished stores a successful publication for skippableResult.
func (p *Publisher) recordPublished(req *PublishRequest, result *PublishResult) {
	p.publishedMu.Lock()
	defer p.publishedMu.Unlock()
	if p.republishInterval <= 0 {
		return
	}
	if p.published == nil {
		p.published = make(map[publishedKey]publishedState)
	}
	key := publishedKey{namespace: req.TemplateConfigNamespace, name: req.TemplateConfigName, nameSuffix: req.NameSuffix}
	p.published[key] = publishedState{
		ownerUID:             string(req.TemplateConfigUID),
		setID:                req.auxiliarySetID,
		checksum:             req.Checksum,
		config:               req.Config,
		configPath:           req.ConfigPath,
		validationError:      req.ValidationError,
		compressionThreshold: req.CompressionThreshold,
		at:                   time.Now(),
		result:               clonePublishResult(result),
	}
}

// forgetPublished voids a key's state after a failed publish: the failure may
// have landed a partial write (e.g. the HAProxyCfg spec), so the recorded
// claim about what is live no longer holds.
func (p *Publisher) forgetPublished(req *PublishRequest) {
	p.publishedMu.Lock()
	defer p.publishedMu.Unlock()
	delete(p.published, publishedKey{namespace: req.TemplateConfigNamespace, name: req.TemplateConfigName, nameSuffix: req.NameSuffix})
}

// ResetAuxiliaryStampCache drops every remembered auxiliary-file status stamp.
// Call it on a leadership transition so a new leader re-stamps each (pod, file)
// once rather than trusting a cache it did not populate.
func (p *Publisher) ResetAuxiliaryStampCache() {
	p.auxStamps.reset()
}

// forgetAuxFileStampOnDelete drops an auxiliary-file CR's stamp cache entries
// once it is deleted (or already gone), so a recreation under the same
// content-hashed name re-stamps instead of being elided against the stale cache.
func (p *Publisher) forgetAuxFileStampOnDelete(err error, kind, namespace, name string) {
	if err == nil || apierrors.IsNotFound(err) {
		p.auxStamps.forgetAuxFile(kind, namespace, name)
	}
}

// NewWithListers creates a Publisher with informer-backed listers for cached reads.
// This significantly reduces API calls by checking the cache before doing status updates.
func NewWithListers(k8sClient kubernetes.Interface, crdClient versioned.Interface, listers *Listers, logger *slog.Logger) *Publisher {
	return &Publisher{
		k8sClient: k8sClient,
		crdClient: crdClient,
		listers:   listers,
		logger:    logger,
	}
}

// PublishConfig creates or updates HAProxyCfg and its child resources.
//
// This method:
// 1. Creates/updates HAProxyCfg with the rendered config
// 2. Creates/updates HAProxyMapFile resources for each map file
// 3. Creates/updates Secret resources for SSL certificates and CA files
// 4. Sets owner references for cascade deletion
// 5. Updates HAProxyCfg status with references to child resources
//
// Returns PublishResult containing the names of created/updated resources.
func (p *Publisher) PublishConfig(ctx context.Context, req *PublishRequest) (*PublishResult, error) {
	if req == nil {
		return nil, errors.New("publish request is nil")
	}
	p.logger.Debug("Publishing runtime config",
		"template_config", req.TemplateConfigName,
		"namespace", req.TemplateConfigNamespace,
	)

	runtimeConfigName := runtimeConfigResourceName(req.TemplateConfigName, req.NameSuffix)
	canonicalRequest, err := canonicalizePublishRequest(req)
	if err != nil {
		return nil, incompletePublicationError(
			PublicationStageAuxiliary,
			req.TemplateConfigNamespace,
			runtimeConfigName,
			"AuxiliaryFiles",
			runtimeConfigName,
			fmt.Errorf("validating auxiliary files: %w", err),
		)
	}
	req = canonicalRequest
	if cached, ok := p.skippableResult(req); ok {
		p.logger.Debug("Skipping unchanged republish",
			"runtime_config", cached.RuntimeConfigName,
			"auxiliary_set_id", req.auxiliarySetID,
		)
		return cached, nil
	}
	runtimeConfig, err := p.createOrUpdateRuntimeConfig(ctx, req)
	if err != nil {
		p.forgetPublished(req)
		return nil, incompletePublicationError(
			PublicationStageRuntimeConfig,
			req.TemplateConfigNamespace,
			runtimeConfigName,
			runtimeConfigKind,
			runtimeConfigName,
			fmt.Errorf("creating or updating runtime config: %w", err),
		)
	}

	result := &PublishResult{
		RuntimeConfigName:      runtimeConfig.Name,
		RuntimeConfigNamespace: runtimeConfig.Namespace,
		MapFileNames:           []string{},
		SecretNames:            []string{},
		SSLCaFileNames:         []string{},
		GeneralFileNames:       []string{},
		CRTListFileNames:       []string{},
	}

	if req.AuxiliaryFiles != nil {
		if err := p.publishAuxiliaryFiles(ctx, req, runtimeConfig, result); err != nil {
			p.forgetPublished(req)
			return result, err
		}
	}

	if err := p.updateRuntimeConfigStatus(ctx, runtimeConfig, result); err != nil {
		p.forgetPublished(req)
		return result, incompletePublicationError(
			PublicationStageReferences,
			runtimeConfig.Namespace,
			runtimeConfig.Name,
			runtimeConfigKind,
			runtimeConfig.Name,
			fmt.Errorf("updating auxiliary references: %w", err),
		)
	}
	if err := p.pruneAuxiliaryFiles(ctx, runtimeConfig, result); err != nil {
		p.forgetPublished(req)
		return result, err
	}

	p.recordPublished(req, result)

	p.logger.Debug("Published runtime config",
		"runtime_config", runtimeConfig.Name,
		"map_files", len(result.MapFileNames),
		"certificate_secrets", len(result.SecretNames),
		"ca_secrets", len(result.SSLCaFileNames),
		"general_files", len(result.GeneralFileNames),
		"crt_list_files", len(result.CRTListFileNames),
	)

	return result, nil
}

func canonicalizePublishRequest(req *PublishRequest) (*PublishRequest, error) {
	if req == nil {
		return nil, errors.New("publish request is nil")
	}
	canonical := *req
	config, files, authenticatedChecksum, err := canonicalPublishPayload(req)
	if err != nil {
		return nil, err
	}
	canonical.Config = config
	canonical.OutputSnapshot = nil
	canonical.AuxiliaryFileSnapshot = nil
	canonical.AuxiliaryFiles = publisherAuxiliaryFiles(files)
	if authenticatedChecksum != "" {
		materializedChecksum := dataplane.ComputeContentChecksum(config, files)
		if materializedChecksum != authenticatedChecksum {
			return nil, errors.New("materialized output differs from authenticated snapshot")
		}
		if req.OutputSnapshot == nil && canonical.Checksum != "" && canonical.Checksum != authenticatedChecksum {
			return nil, errors.New("publish checksum differs from authenticated output")
		}
		canonical.Checksum = authenticatedChecksum
	}

	serialized, err := json.Marshal(canonical.AuxiliaryFiles)
	if err != nil {
		return nil, fmt.Errorf("serializing auxiliary set: %w", err)
	}
	h := sha256.New()
	_, _ = h.Write(serialized)
	hashAuxiliaryContents(h, canonical.AuxiliaryFiles.MapFiles)
	hashAuxiliaryContents(h, canonical.AuxiliaryFiles.SSLCertificates)
	hashAuxiliaryContents(h, canonical.AuxiliaryFiles.SSLCaFiles)
	hashAuxiliaryContents(h, canonical.AuxiliaryFiles.GeneralFiles)
	hashAuxiliaryContents(h, canonical.AuxiliaryFiles.CRTListFiles)
	canonical.auxiliarySetID = fmt.Sprintf("sha256:%x", h.Sum(nil))
	return &canonical, nil
}

func canonicalPublishPayload(req *PublishRequest) (
	config string,
	files *dataplane.AuxiliaryFiles,
	checksum string,
	err error,
) {
	if req.OutputSnapshot != nil {
		return canonicalOutputSnapshotPayload(req.OutputSnapshot)
	}
	if req.AuxiliaryFileSnapshot != nil {
		return canonicalAuxiliarySnapshotPayload(req)
	}
	return canonicalLegacyPayload(req)
}

func canonicalOutputSnapshotPayload(
	snapshot *renderoutput.Snapshot,
) (config string, files *dataplane.AuxiliaryFiles, checksum string, err error) {
	if err := snapshot.ValidateAuthentication(); err != nil {
		return "", nil, "", fmt.Errorf("authenticating output snapshot: %w", err)
	}
	config, err = snapshot.Config()
	if err != nil {
		return "", nil, "", fmt.Errorf("reading output config: %w", err)
	}
	artifacts, err := snapshot.ArtifactSnapshot()
	if err != nil {
		return "", nil, "", fmt.Errorf("reading output artifacts: %w", err)
	}
	if _, err := snapshot.PlanID(); err != nil {
		return "", nil, "", fmt.Errorf("reading output plan ID: %w", err)
	}
	checksum, err = snapshot.ContentChecksum()
	if err != nil {
		return "", nil, "", fmt.Errorf("reading output checksum: %w", err)
	}
	files, err = dataplane.MaterializeAuxiliaryFileSnapshot(artifacts)
	if err != nil {
		return "", nil, "", fmt.Errorf("materializing output artifacts: %w", err)
	}
	return config, files, checksum, nil
}

func canonicalAuxiliarySnapshotPayload(
	req *PublishRequest,
) (config string, files *dataplane.AuxiliaryFiles, checksum string, err error) {
	if req.AuxiliaryFiles != nil {
		return "", nil, "", errors.New("auxiliary snapshot is mixed with legacy auxiliary files")
	}
	if err := req.AuxiliaryFileSnapshot.ValidateAuthentication(); err != nil {
		return "", nil, "", fmt.Errorf("authenticating auxiliary snapshot: %w", err)
	}
	checksum, err = dataplane.ComputeSnapshotContentChecksum(req.Config, req.AuxiliaryFileSnapshot)
	if err != nil {
		return "", nil, "", fmt.Errorf("checksumming auxiliary snapshot: %w", err)
	}
	files, err = dataplane.MaterializeAuxiliaryFileSnapshot(req.AuxiliaryFileSnapshot)
	if err != nil {
		return "", nil, "", fmt.Errorf("materializing auxiliary snapshot: %w", err)
	}
	return req.Config, files, checksum, nil
}

func canonicalLegacyPayload(
	req *PublishRequest,
) (config string, files *dataplane.AuxiliaryFiles, checksum string, err error) {
	inputFiles := &dataplane.AuxiliaryFiles{}
	if req.AuxiliaryFiles != nil {
		inputFiles = &dataplane.AuxiliaryFiles{
			MapFiles:        req.AuxiliaryFiles.MapFiles,
			SSLCertificates: req.AuxiliaryFiles.SSLCertificates,
			SSLCaFiles:      req.AuxiliaryFiles.SSLCaFiles,
			GeneralFiles:    req.AuxiliaryFiles.GeneralFiles,
			CRTListFiles:    req.AuxiliaryFiles.CRTListFiles,
		}
	}
	files, err = dataplane.CanonicalizeAuxiliaryFiles(inputFiles)
	if err != nil {
		return "", nil, "", err
	}
	return req.Config, files, "", nil
}

func publisherAuxiliaryFiles(files *dataplane.AuxiliaryFiles) *AuxiliaryFiles {
	return &AuxiliaryFiles{
		MapFiles:        files.MapFiles,
		SSLCertificates: files.SSLCertificates,
		SSLCaFiles:      files.SSLCaFiles,
		GeneralFiles:    files.GeneralFiles,
		CRTListFiles:    files.CRTListFiles,
	}
}

func hashAuxiliaryContents[T auxiliaryfiles.FileItem](h hash.Hash, files []T) {
	for _, file := range files {
		_, _ = h.Write([]byte(calculateChecksum(file.GetContent())))
	}
}

// publishAuxiliaryFiles creates or updates all auxiliary file resources.
func publishAuxiliaryResource(
	initialName, baseName, suffix, identity, ownerName string,
	publish func(string) (string, error),
) (publishedName, attemptedName string, err error) {
	publishedName, err = publish(initialName)
	if err == nil {
		return publishedName, initialName, nil
	}
	var ownershipError *auxiliaryResourceOwnershipError
	if !errors.As(err, &ownershipError) {
		return "", initialName, err
	}

	scopedName := disambiguatedResourceName(baseName, suffix, ownerName+"\x00"+identity)
	publishedName, err = publish(scopedName)
	return publishedName, scopedName, err
}

func (p *Publisher) publishAuxiliaryFiles(
	ctx context.Context,
	req *PublishRequest,
	runtimeConfig *haproxyv1alpha1.HAProxyCfg,
	result *PublishResult,
) error {
	resourceSuffix := auxiliaryResourceSuffix(req.auxiliarySetID, req.NameSuffix)
	mapFileNames := resolveAuxiliaryResourceNames(
		req.AuxiliaryFiles.MapFiles,
		resourceSuffix,
		func(file auxiliaryfiles.MapFile) string { return p.generateMapFileName(path.Base(file.Path)) },
		func(file auxiliaryfiles.MapFile) string { return file.Path },
	)
	// Create or update map files
	for i, mapFile := range req.AuxiliaryFiles.MapFiles {
		baseName := p.generateMapFileName(path.Base(mapFile.Path))
		mapFileName, name, err := publishAuxiliaryResource(
			mapFileNames[i], baseName, resourceSuffix, mapFile.Path, runtimeConfig.Name,
			func(name string) (string, error) {
				return p.createOrUpdateMapFile(ctx, req, runtimeConfig, mapFile, name)
			},
		)
		if err != nil {
			return incompletePublicationError(
				PublicationStageAuxiliary,
				runtimeConfig.Namespace,
				runtimeConfig.Name,
				kindMapFile,
				name,
				err,
			)
		}
		result.MapFileNames = append(result.MapFileNames, mapFileName)
	}

	secretNames := resolveAuxiliaryResourceNames(
		req.AuxiliaryFiles.SSLCertificates,
		resourceSuffix,
		func(file auxiliaryfiles.SSLCertificate) string { return p.generateSecretName(path.Base(file.Path)) },
		func(file auxiliaryfiles.SSLCertificate) string { return file.Path },
	)
	// Create or update SSL certificate secrets
	for i, cert := range req.AuxiliaryFiles.SSLCertificates {
		baseName := p.generateSecretName(path.Base(cert.Path))
		secretName, name, err := publishAuxiliaryResource(
			secretNames[i], baseName, resourceSuffix, cert.Path, runtimeConfig.Name,
			func(name string) (string, error) {
				return p.createOrUpdateSSLSecret(ctx, req, runtimeConfig, cert, name)
			},
		)
		if err != nil {
			return incompletePublicationError(
				PublicationStageAuxiliary,
				runtimeConfig.Namespace,
				runtimeConfig.Name,
				"Secret",
				name,
				err,
			)
		}
		result.SecretNames = append(result.SecretNames, secretName)
	}

	caSecretNames := resolveAuxiliaryResourceNames(
		req.AuxiliaryFiles.SSLCaFiles,
		resourceSuffix,
		func(file auxiliaryfiles.SSLCaFile) string { return p.generateCASecretName(path.Base(file.Path)) },
		func(file auxiliaryfiles.SSLCaFile) string { return file.Path },
	)
	for i, ca := range req.AuxiliaryFiles.SSLCaFiles {
		baseName := p.generateCASecretName(path.Base(ca.Path))
		secretName, name, err := publishAuxiliaryResource(
			caSecretNames[i], baseName, resourceSuffix, ca.Path, runtimeConfig.Name,
			func(name string) (string, error) {
				return p.createOrUpdateSSLCASecret(ctx, req, runtimeConfig, ca, name)
			},
		)
		if err != nil {
			return incompletePublicationError(
				PublicationStageAuxiliary,
				runtimeConfig.Namespace,
				runtimeConfig.Name,
				"Secret",
				name,
				err,
			)
		}
		result.SSLCaFileNames = append(result.SSLCaFileNames, secretName)
	}

	generalFileNames := resolveAuxiliaryResourceNames(
		req.AuxiliaryFiles.GeneralFiles,
		resourceSuffix,
		func(file auxiliaryfiles.GeneralFile) string { return p.generateGeneralFileName(file.Filename) },
		func(file auxiliaryfiles.GeneralFile) string { return file.Filename },
	)
	// Create or update general files
	for i, generalFile := range req.AuxiliaryFiles.GeneralFiles {
		baseName := p.generateGeneralFileName(generalFile.Filename)
		generalFileName, name, err := publishAuxiliaryResource(
			generalFileNames[i], baseName, resourceSuffix, generalFile.Filename, runtimeConfig.Name,
			func(name string) (string, error) {
				return p.createOrUpdateGeneralFile(ctx, req, runtimeConfig, generalFile, name)
			},
		)
		if err != nil {
			return incompletePublicationError(
				PublicationStageAuxiliary,
				runtimeConfig.Namespace,
				runtimeConfig.Name,
				kindGeneralFile,
				name,
				err,
			)
		}
		result.GeneralFileNames = append(result.GeneralFileNames, generalFileName)
	}

	crtListFileNames := resolveAuxiliaryResourceNames(
		req.AuxiliaryFiles.CRTListFiles,
		resourceSuffix,
		func(file auxiliaryfiles.CRTListFile) string { return p.generateCRTListFileName(file.Path) },
		func(file auxiliaryfiles.CRTListFile) string { return file.Path },
	)
	// Create or update crt-list files
	for i, crtListFile := range req.AuxiliaryFiles.CRTListFiles {
		baseName := p.generateCRTListFileName(crtListFile.Path)
		crtListFileName, name, err := publishAuxiliaryResource(
			crtListFileNames[i], baseName, resourceSuffix, crtListFile.Path, runtimeConfig.Name,
			func(name string) (string, error) {
				return p.createOrUpdateCRTListFile(ctx, req, runtimeConfig, crtListFile, name)
			},
		)
		if err != nil {
			return incompletePublicationError(
				PublicationStageAuxiliary,
				runtimeConfig.Namespace,
				runtimeConfig.Name,
				kindCRTListFile,
				name,
				err,
			)
		}
		result.CRTListFileNames = append(result.CRTListFileNames, crtListFileName)
	}

	return nil
}

// DeleteRuntimeConfig deletes a HAProxyCfg resource.
//
// Used to clean up invalid configuration resources when validation succeeds again.
func (p *Publisher) DeleteRuntimeConfig(ctx context.Context, namespace, name string) error {
	err := p.crdClient.HaproxyTemplateICV1alpha1().
		HAProxyCfgs(namespace).
		Delete(ctx, name, metav1.DeleteOptions{})

	if err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("deleting runtime config %s/%s: %w", namespace, name, err)
	}

	if err == nil {
		p.logger.Debug("Deleted runtime config",
			"name", name,
			"namespace", namespace,
		)
	}

	return nil
}
