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
	"path"
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

// fetchAndValidateInitialConfig fetches, parses, and validates the initial HAProxyTemplateConfig CRD and credentials Secret.
//
// Returns the validated configuration and credentials, or an error if any step fails.
// InitialConfigBundle holds the parsed initial CRD / Secret values plus the
// resource versions of the underlying Secrets, so the iteration startup
// can wire bootstrap-version filtering for ConfigChangeHandler.
type InitialConfigBundle struct {
	Config             *coreconfig.Config
	CRD                *v1alpha1.HAProxyTemplateConfig
	Credentials        *coreconfig.Credentials
	CredentialsVersion string
}

func fetchAndValidateInitialConfig(
	ctx context.Context,
	k8sClient *client.Client,
	crdName string,
	secretName string,
	crdGVR schema.GroupVersionResource,
	secretGVR schema.GroupVersionResource,
	logger *slog.Logger,
) (*InitialConfigBundle, error) {
	logger.Info("Fetching initial CRD and credentials", "crd_name", crdName)

	var crdResource *unstructured.Unstructured
	var secretResource *unstructured.Unstructured

	g, gCtx := errgroup.WithContext(ctx)

	// Fetch HAProxyTemplateConfig CRD
	g.Go(func() error {
		var err error
		crdResource, err = k8sClient.GetResource(gCtx, crdGVR, crdName)
		if err != nil {
			return fmt.Errorf("fetching HAProxyTemplateConfig %q: %w", crdName, err)
		}
		return nil
	})

	// Fetch Secret (credentials)
	g.Go(func() error {
		var err error
		secretResource, err = k8sClient.GetResource(gCtx, secretGVR, secretName)
		if err != nil {
			return fmt.Errorf("fetching Secret %q: %w", secretName, err)
		}
		return nil
	})

	// Wait for all fetches to complete
	if err := g.Wait(); err != nil {
		return nil, err
	}

	// Parse initial configuration
	logger.Info("Parsing initial configuration and credentials")

	cfg, crd, err := conversion.ParseCRD(crdResource)
	if err != nil {
		return nil, fmt.Errorf("parsing initial HAProxyTemplateConfig: %w", err)
	}

	creds, err := parseSecret(secretResource)
	if err != nil {
		return nil, fmt.Errorf("parsing initial Secret: %w", err)
	}

	// Validate initial configuration
	logger.Info("Validating initial configuration and credentials")

	if err := coreconfig.ValidateStructure(cfg); err != nil {
		return nil, fmt.Errorf("initial configuration validation failed: %w", err)
	}

	if err := coreconfig.ValidateCredentials(creds); err != nil {
		return nil, fmt.Errorf("initial credentials validation failed: %w", err)
	}

	logger.Info("Initial configuration validated successfully",
		"crd_version", crdResource.GetResourceVersion(),
		"secret_version", secretResource.GetResourceVersion())

	bundle := &InitialConfigBundle{
		Config:             cfg,
		CRD:                crd,
		Credentials:        creds,
		CredentialsVersion: secretResource.GetResourceVersion(),
	}
	return bundle, nil
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

// finalizeConfigLoad marks config as loaded for health checks and records
// the initial CRD / credentials-Secret resource versions so the bootstrap
// watcher events (which the watcher fires the moment it observes existing
// resources at iteration startup) don't trigger a redundant reinitialization
// loop. The handler still triggers reinitialization on later events whose
// version differs — that's how CRD changes and credentials rotation reach
// the iteration restart, going through the same configChangeCh path.
func finalizeConfigLoad(state *configState, setup *componentSetup, crdVersion, credentialsVersion string) {
	state.SetLoaded()
	setup.ConfigChangeHandler.SetInitialConfigVersion(crdVersion)
	if credentialsVersion != "" {
		setup.ConfigChangeHandler.SetInitialCredentialsVersion(credentialsVersion)
	}
}

// extractSecretData extracts the raw data map from a Kubernetes Secret resource.
func extractSecretData(resource *unstructured.Unstructured) (map[string]any, error) {
	dataRaw, found, err := unstructured.NestedMap(resource.Object, "data")
	if err != nil {
		return nil, fmt.Errorf("extracting data field: %w", err)
	}
	if !found {
		return nil, errors.New("secret has no data field")
	}
	return dataRaw, nil
}

// parseSecret extracts and parses credentials from a Secret resource.
func parseSecret(resource *unstructured.Unstructured) (*coreconfig.Credentials, error) {
	dataRaw, err := extractSecretData(resource)
	if err != nil {
		return nil, err
	}

	// Parse Secret data (handles base64 decoding)
	data, err := coreconfig.ParseSecretData(dataRaw)
	if err != nil {
		return nil, err
	}

	// Load credentials
	creds, err := coreconfig.LoadCredentials(data)
	if err != nil {
		return nil, fmt.Errorf("loading credentials: %w", err)
	}

	return creds, nil
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
// using path.Base() to get just the directory name component. The slash-only path
// package is used (not filepath) because these are HAProxy target paths, which are
// always slash-separated regardless of the OS the controller runs on.
func extractValidationDirConfig(dataplaneConfig *coreconfig.DataplaneConfig) validationDirConfig {
	return validationDirConfig{
		BaseDir:     path.Dir(dataplaneConfig.MapsDir),
		MapsDir:     path.Base(dataplaneConfig.MapsDir),
		SSLCertsDir: path.Base(dataplaneConfig.SSLCertsDir),
		GeneralDir:  path.Base(dataplaneConfig.GeneralStorageDir),
	}
}
