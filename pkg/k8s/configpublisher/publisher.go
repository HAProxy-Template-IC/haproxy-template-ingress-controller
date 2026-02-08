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
	"fmt"
	"log/slog"

	haproxyv1alpha1 "gitlab.com/haproxy-haptic/haptic/pkg/apis/haproxytemplate/v1alpha1"
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
}

// New creates a new Publisher instance.
func New(k8sClient kubernetes.Interface, crdClient versioned.Interface, logger *slog.Logger) *Publisher {
	return &Publisher{
		k8sClient: k8sClient,
		crdClient: crdClient,
		logger:    logger,
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
	p.logger.Debug("publishing runtime config",
		"templateConfig", req.TemplateConfigName,
		"namespace", req.TemplateConfigNamespace,
	)

	// Create or update HAProxyCfg
	runtimeConfig, err := p.createOrUpdateRuntimeConfig(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("failed to create/update runtime config: %w", err)
	}

	result := &PublishResult{
		RuntimeConfigName:      runtimeConfig.Name,
		RuntimeConfigNamespace: runtimeConfig.Namespace,
		MapFileNames:           []string{},
		SecretNames:            []string{},
		GeneralFileNames:       []string{},
		CRTListFileNames:       []string{},
	}

	// Publish auxiliary files (map files, SSL secrets, general files, crt-list files)
	if req.AuxiliaryFiles != nil {
		p.publishAuxiliaryFiles(ctx, req, runtimeConfig, result)
	}

	// Update HAProxyCfg status with child resource references
	if err := p.updateRuntimeConfigStatus(ctx, runtimeConfig, result); err != nil {
		p.logger.Debug("status update conflict (will retry on next reconciliation)",
			"type", "runtime_config_status",
			"name", runtimeConfig.Name,
			"error", err,
		)
		// Non-blocking - status update is informational
	}

	p.logger.Debug("published runtime config",
		"runtimeConfig", runtimeConfig.Name,
		"mapFiles", len(result.MapFileNames),
		"secrets", len(result.SecretNames),
		"generalFiles", len(result.GeneralFileNames),
		"crtListFiles", len(result.CRTListFileNames),
	)

	return result, nil
}

// publishAuxiliaryFiles creates or updates all auxiliary file resources.
// Errors are logged but don't fail the overall publish operation.
func (p *Publisher) publishAuxiliaryFiles(
	ctx context.Context,
	req *PublishRequest,
	runtimeConfig *haproxyv1alpha1.HAProxyCfg,
	result *PublishResult,
) {
	// Create or update map files
	for _, mapFile := range req.AuxiliaryFiles.MapFiles {
		mapFileName, err := p.createOrUpdateMapFile(ctx, req, runtimeConfig, mapFile)
		if err != nil {
			p.logger.Debug("auxiliary file update conflict (will retry on next reconciliation)",
				"type", "map_file",
				"name", mapFile.Path,
				"error", err,
			)
			continue
		}
		result.MapFileNames = append(result.MapFileNames, mapFileName)
	}

	// Create or update SSL certificate secrets
	for _, cert := range req.AuxiliaryFiles.SSLCertificates {
		secretName, err := p.createOrUpdateSSLSecret(ctx, req, runtimeConfig, cert)
		if err != nil {
			p.logger.Debug("auxiliary file update conflict (will retry on next reconciliation)",
				"type", "ssl_secret",
				"path", cert.Path,
				"error", err,
			)
			continue
		}
		result.SecretNames = append(result.SecretNames, secretName)
	}

	// Create or update general files
	for _, generalFile := range req.AuxiliaryFiles.GeneralFiles {
		generalFileName, err := p.createOrUpdateGeneralFile(ctx, req, runtimeConfig, generalFile)
		if err != nil {
			p.logger.Debug("auxiliary file update conflict (will retry on next reconciliation)",
				"type", "general_file",
				"name", generalFile.Filename,
				"error", err,
			)
			continue
		}
		result.GeneralFileNames = append(result.GeneralFileNames, generalFileName)
	}

	// Create or update crt-list files
	for _, crtListFile := range req.AuxiliaryFiles.CRTListFiles {
		crtListFileName, err := p.createOrUpdateCRTListFile(ctx, req, runtimeConfig, crtListFile)
		if err != nil {
			p.logger.Debug("auxiliary file update conflict (will retry on next reconciliation)",
				"type", "crt_list_file",
				"path", crtListFile.Path,
				"error", err,
			)
			continue
		}
		result.CRTListFileNames = append(result.CRTListFileNames, crtListFileName)
	}
}

// DeleteRuntimeConfig deletes a HAProxyCfg resource.
//
// Used to clean up invalid configuration resources when validation succeeds again.
func (p *Publisher) DeleteRuntimeConfig(ctx context.Context, namespace, name string) error {
	err := p.crdClient.HaproxyTemplateICV1alpha1().
		HAProxyCfgs(namespace).
		Delete(ctx, name, metav1.DeleteOptions{})

	if err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("failed to delete runtime config %s/%s: %w", namespace, name, err)
	}

	if err == nil {
		p.logger.Debug("deleted runtime config",
			"name", name,
			"namespace", namespace,
		)
	}

	return nil
}
