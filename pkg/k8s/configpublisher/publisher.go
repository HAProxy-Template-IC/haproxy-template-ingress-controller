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

	haproxyv1alpha1 "gitlab.com/haproxy-haptic/haptic/pkg/apis/haproxytemplate/v1alpha1"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/auxiliaryfiles"
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
// 3. Creates/updates Secret resources for SSL certificates
// 4. Sets owner references for cascade deletion
// 5. Updates HAProxyCfg status with references to child resources
//
// Returns PublishResult containing the names of created/updated resources.
func (p *Publisher) PublishConfig(ctx context.Context, req *PublishRequest) (*PublishResult, error) {
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
	runtimeConfig, err := p.createOrUpdateRuntimeConfig(ctx, req)
	if err != nil {
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
		GeneralFileNames:       []string{},
		CRTListFileNames:       []string{},
	}

	if req.AuxiliaryFiles != nil {
		if err := p.publishAuxiliaryFiles(ctx, req, runtimeConfig, result); err != nil {
			return result, err
		}
	}

	if err := p.updateRuntimeConfigStatus(ctx, runtimeConfig, result); err != nil {
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
		return result, err
	}

	p.logger.Debug("Published runtime config",
		"runtime_config", runtimeConfig.Name,
		"map_files", len(result.MapFileNames),
		"secrets", len(result.SecretNames),
		"general_files", len(result.GeneralFileNames),
		"crt_list_files", len(result.CRTListFileNames),
	)

	return result, nil
}

func canonicalizePublishRequest(req *PublishRequest) (*PublishRequest, error) {
	canonical := *req
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
	files, err := dataplane.CanonicalizeAuxiliaryFiles(inputFiles)
	if err != nil {
		return nil, err
	}
	canonical.AuxiliaryFiles = &AuxiliaryFiles{
		MapFiles:        files.MapFiles,
		SSLCertificates: files.SSLCertificates,
		SSLCaFiles:      files.SSLCaFiles,
		GeneralFiles:    files.GeneralFiles,
		CRTListFiles:    files.CRTListFiles,
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
