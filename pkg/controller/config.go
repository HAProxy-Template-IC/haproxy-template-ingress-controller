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
	"encoding/base64"
	"fmt"
	"log/slog"
	"path/filepath"
	"time"

	"golang.org/x/sync/errgroup"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"gitlab.com/haproxy-haptic/haptic/pkg/apis/haproxytemplate/v1alpha1"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/conversion"
	coreconfig "gitlab.com/haproxy-haptic/haptic/pkg/core/config"
	"gitlab.com/haproxy-haptic/haptic/pkg/k8s/client"
)

// WebhookCertificates holds the TLS certificate and private key for the webhook server.
type WebhookCertificates struct {
	CertPEM []byte
	KeyPEM  []byte
	Version string
}

// fetchAndValidateInitialConfig fetches, parses, and validates the initial ConfigMap and Secret.
//
// Returns the validated configuration and credentials, or an error if any step fails.
func fetchAndValidateInitialConfig(
	ctx context.Context,
	k8sClient *client.Client,
	crdName string,
	secretName string,
	webhookCertSecretName string,
	crdGVR schema.GroupVersionResource,
	secretGVR schema.GroupVersionResource,
	logger *slog.Logger,
) (*coreconfig.Config, *v1alpha1.HAProxyTemplateConfig, *coreconfig.Credentials, *WebhookCertificates, error) {
	logger.Info("Fetching initial CRD, credentials, and webhook certificates",
		"crd_name", crdName)

	var crdResource *unstructured.Unstructured
	var secretResource *unstructured.Unstructured
	var webhookCertSecretResource *unstructured.Unstructured

	g, gCtx := errgroup.WithContext(ctx)

	// Fetch HAProxyTemplateConfig CRD
	g.Go(func() error {
		var err error
		crdResource, err = k8sClient.GetResource(gCtx, crdGVR, crdName)
		if err != nil {
			return fmt.Errorf("failed to fetch HAProxyTemplateConfig %q: %w", crdName, err)
		}
		return nil
	})

	// Fetch Secret (credentials)
	g.Go(func() error {
		var err error
		secretResource, err = k8sClient.GetResource(gCtx, secretGVR, secretName)
		if err != nil {
			return fmt.Errorf("failed to fetch Secret %q: %w", secretName, err)
		}
		return nil
	})

	// Fetch Secret (webhook certificates)
	g.Go(func() error {
		var err error
		webhookCertSecretResource, err = k8sClient.GetResource(gCtx, secretGVR, webhookCertSecretName)
		if err != nil {
			return fmt.Errorf("failed to fetch webhook certificate Secret %q: %w", webhookCertSecretName, err)
		}
		return nil
	})

	// Wait for all fetches to complete
	if err := g.Wait(); err != nil {
		return nil, nil, nil, nil, err
	}

	// Parse initial configuration
	logger.Info("Parsing initial configuration, credentials, and webhook certificates")

	cfg, crd, err := parseCRD(crdResource)
	if err != nil {
		return nil, nil, nil, nil, fmt.Errorf("failed to parse initial HAProxyTemplateConfig: %w", err)
	}

	creds, err := parseSecret(secretResource)
	if err != nil {
		return nil, nil, nil, nil, fmt.Errorf("failed to parse initial Secret: %w", err)
	}

	webhookCerts, err := parseWebhookCertSecret(webhookCertSecretResource)
	if err != nil {
		return nil, nil, nil, nil, fmt.Errorf("failed to parse webhook certificate Secret: %w", err)
	}

	// Validate initial configuration
	logger.Info("Validating initial configuration and credentials")

	if err := coreconfig.ValidateStructure(cfg); err != nil {
		return nil, nil, nil, nil, fmt.Errorf("initial configuration validation failed: %w", err)
	}

	if err := coreconfig.ValidateCredentials(creds); err != nil {
		return nil, nil, nil, nil, fmt.Errorf("initial credentials validation failed: %w", err)
	}

	logger.Info("Initial configuration validated successfully",
		"crd_version", crdResource.GetResourceVersion(),
		"secret_version", secretResource.GetResourceVersion(),
		"webhook_cert_version", webhookCertSecretResource.GetResourceVersion())

	return cfg, crd, creds, webhookCerts, nil
}

// waitForInitialConfig polls for the HAProxyTemplateConfig until it exists.
// This handles the race condition during fresh installs where the controller pod
// may start before the HAProxyTemplateConfig CR is fully available in the API server.
//
// Returns nil when config is found, or ctx.Err() if context is cancelled.
func waitForInitialConfig(
	ctx context.Context,
	k8sClient *client.Client,
	crdName string,
	crdGVR schema.GroupVersionResource,
	state *configState,
	logger *slog.Logger,
) error {
	state.SetWaiting("waiting for HAProxyTemplateConfig")

	// Try immediately first
	exists, _ := checkConfigExists(ctx, k8sClient, crdGVR, crdName)
	if exists {
		logger.Info("HAProxyTemplateConfig found", "name", crdName)
		return nil
	}

	logger.Info("Waiting for HAProxyTemplateConfig to become available",
		"name", crdName,
		"poll_interval", ConfigPollInterval)

	ticker := time.NewTicker(ConfigPollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			exists, err := checkConfigExists(ctx, k8sClient, crdGVR, crdName)
			if err != nil {
				// Log at debug level - transient errors during polling are expected
				logger.Debug("Error checking for HAProxyTemplateConfig", "error", err)
				continue
			}
			if exists {
				logger.Info("HAProxyTemplateConfig found", "name", crdName)
				return nil
			}
			logger.Debug("HAProxyTemplateConfig not yet available, continuing to wait",
				"name", crdName)
		}
	}
}

// checkConfigExists checks if the HAProxyTemplateConfig resource exists.
// Returns (true, nil) if exists, (false, nil) if not found, or (false, err) on other errors.
func checkConfigExists(ctx context.Context, k8sClient *client.Client, gvr schema.GroupVersionResource, name string) (bool, error) {
	_, err := k8sClient.GetResource(ctx, gvr, name)
	if err != nil {
		if apierrors.IsNotFound(err) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// finalizeConfigLoad marks config as loaded for health checks and sets the initial config
// version to prevent the infinite reinitialization loop. CRDWatcher.onAdd will trigger
// ConfigValidatedEvent with this version - without tracking it, that event would trigger
// reinitialization creating an infinite loop.
func finalizeConfigLoad(state *configState, setup *componentSetup, resourceVersion string) {
	state.SetLoaded()
	setup.ConfigChangeHandler.SetInitialConfigVersion(resourceVersion)
}

// parseCRD extracts and converts configuration from a HAProxyTemplateConfig CRD resource.
func parseCRD(resource *unstructured.Unstructured) (*coreconfig.Config, *v1alpha1.HAProxyTemplateConfig, error) {
	return conversion.ParseCRD(resource)
}

// parseSecret extracts and parses credentials from a Secret resource.
func parseSecret(resource *unstructured.Unstructured) (*coreconfig.Credentials, error) {
	// Extract Secret data field
	dataRaw, found, err := unstructured.NestedMap(resource.Object, "data")
	if err != nil {
		return nil, fmt.Errorf("failed to extract data field: %w", err)
	}
	if !found {
		return nil, fmt.Errorf("secret has no data field")
	}

	// Parse Secret data (handles base64 decoding)
	data, err := coreconfig.ParseSecretData(dataRaw)
	if err != nil {
		return nil, err
	}

	// Load credentials
	creds, err := coreconfig.LoadCredentials(data)
	if err != nil {
		return nil, fmt.Errorf("failed to load credentials: %w", err)
	}

	return creds, nil
}

// parseWebhookCertSecret extracts and decodes webhook TLS certificate data from a Secret.
func parseWebhookCertSecret(resource *unstructured.Unstructured) (*WebhookCertificates, error) {
	// Extract Secret data field
	dataRaw, found, err := unstructured.NestedMap(resource.Object, "data")
	if err != nil {
		return nil, fmt.Errorf("failed to extract data field: %w", err)
	}
	if !found {
		return nil, fmt.Errorf("secret has no data field")
	}

	// Extract tls.crt (standard Kubernetes TLS Secret key)
	tlsCertBase64, ok := dataRaw["tls.crt"]
	if !ok {
		return nil, fmt.Errorf("secret data missing 'tls.crt' key")
	}

	// Extract tls.key (standard Kubernetes TLS Secret key)
	tlsKeyBase64, ok := dataRaw["tls.key"]
	if !ok {
		return nil, fmt.Errorf("secret data missing 'tls.key' key")
	}

	// Decode base64 certificate
	var certPEM []byte
	if strValue, ok := tlsCertBase64.(string); ok {
		certPEM, err = base64.StdEncoding.DecodeString(strValue)
		if err != nil {
			return nil, fmt.Errorf("failed to decode base64 tls.crt: %w", err)
		}
	} else {
		return nil, fmt.Errorf("tls.crt has invalid type: %T", tlsCertBase64)
	}

	// Decode base64 private key
	var keyPEM []byte
	if strValue, ok := tlsKeyBase64.(string); ok {
		keyPEM, err = base64.StdEncoding.DecodeString(strValue)
		if err != nil {
			return nil, fmt.Errorf("failed to decode base64 tls.key: %w", err)
		}
	} else {
		return nil, fmt.Errorf("tls.key has invalid type: %T", tlsKeyBase64)
	}

	// Validate we have non-empty data
	if len(certPEM) == 0 {
		return nil, fmt.Errorf("tls.crt is empty")
	}
	if len(keyPEM) == 0 {
		return nil, fmt.Errorf("tls.key is empty")
	}

	return &WebhookCertificates{
		CertPEM: certPEM,
		KeyPEM:  keyPEM,
		Version: resource.GetResourceVersion(),
	}, nil
}

// validationDirConfig contains directory configuration derived from dataplane settings.
// This struct centralizes the directory name extraction to avoid repetition.
type validationDirConfig struct {
	BaseDir     string // Parent directory (e.g., /etc/haproxy)
	MapsDir     string // Relative maps directory name (e.g., maps)
	SSLCertsDir string // Relative SSL certs directory name (e.g., ssl)
	GeneralDir  string // Relative general files directory name (e.g., general)
}

// extractValidationDirConfig derives directory names from dataplane configuration.
// The BaseDir is the parent of MapsDir, and individual directory names are extracted
// using filepath.Base() to get just the directory name component.
func extractValidationDirConfig(dataplaneConfig *coreconfig.DataplaneConfig) validationDirConfig {
	return validationDirConfig{
		BaseDir:     filepath.Dir(dataplaneConfig.MapsDir),
		MapsDir:     filepath.Base(dataplaneConfig.MapsDir),
		SSLCertsDir: filepath.Base(dataplaneConfig.SSLCertsDir),
		GeneralDir:  filepath.Base(dataplaneConfig.GeneralStorageDir),
	}
}
