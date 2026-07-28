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
	"strings"
	"time"

	"golang.org/x/sync/errgroup"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"gitlab.com/haproxy-haptic/haptic/pkg/apis/haproxytemplate/v1alpha1"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/configchange"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/conversion"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/validator"
	coreconfig "gitlab.com/haproxy-haptic/haptic/pkg/core/config"
	"gitlab.com/haproxy-haptic/haptic/pkg/generated/clientset/versioned"
	"gitlab.com/haproxy-haptic/haptic/pkg/k8s/client"
)

// fetchAndValidateInitialConfig fetches, parses, and validates the initial HAProxyTemplateConfig CRD and credentials Secret.
//
// Returns the validated configuration and credentials, or an error if any step fails.
// InitialConfigBundle holds the parsed initial CRD / Secret values plus the
// resource versions of the underlying Secrets, so the iteration startup
// can wire bootstrap-version filtering for ConfigChangeHandler.
type InitialConfigBundle struct {
	Config      *coreconfig.Config
	CRD         *v1alpha1.HAProxyTemplateConfig
	Credentials *coreconfig.Credentials
	// ConfigVersion identifies the whole merged set, not just the primary
	// config — see conversion.CompositeVersion. The bootstrap guard compares
	// it for equality, so it has to change when ANY member changes.
	ConfigVersion      string
	CredentialsVersion string
}

func fetchAndValidateInitialConfig(
	ctx context.Context,
	k8sClient *client.Client,
	crdNames []string,
	secretName string,
	crdGVR schema.GroupVersionResource,
	secretGVR schema.GroupVersionResource,
	logger *slog.Logger,
) (*InitialConfigBundle, error) {
	logger.Info("Fetching initial CRDs and credentials", "crd_names", crdNames)

	crdResources := make([]*unstructured.Unstructured, len(crdNames))
	var secretResource *unstructured.Unstructured

	g, gCtx := errgroup.WithContext(ctx)

	// Fetch every HAProxyTemplateConfig. Order is the merge order, so each
	// fetch writes its own slot rather than appending.
	for i, crdName := range crdNames {
		g.Go(func() error {
			resource, err := k8sClient.GetResource(gCtx, crdGVR, crdName)
			if err != nil {
				return fmt.Errorf("fetching HAProxyTemplateConfig %q: %w", crdName, err)
			}
			crdResources[i] = resource
			return nil
		})
	}

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

	crdResource, overrides, err := conversion.MergeSpecs(crdResources)
	if err != nil {
		return nil, fmt.Errorf("merging initial HAProxyTemplateConfigs: %w", err)
	}
	logSnippetOverrides(overrides, logger)

	cfg, crd, err := conversion.ParseCRD(crdResource)
	if err != nil {
		return nil, fmt.Errorf("parsing initial HAProxyTemplateConfig: %w", err)
	}

	creds, err := parseSecret(secretResource)
	if err != nil {
		return nil, fmt.Errorf("parsing initial Secret: %w", err)
	}

	// Before ValidateStructure, so the discovered tests go through the same
	// structural checks and requires-stripping as the inline ones.
	if err := unionDiscoveredValidationTests(ctx, k8sClient, cfg, crd, logger); err != nil {
		return nil, fmt.Errorf("resolving validation tests: %w", err)
	}
	if err := enforceRequireValidationTests(cfg, crd.Spec.RequireValidationTests); err != nil {
		return nil, err
	}

	// Validate initial configuration
	logger.Info("Validating initial configuration and credentials")

	if err := coreconfig.ValidateStructure(cfg); err != nil {
		return nil, fmt.Errorf("initial configuration validation failed: %w", err)
	}

	if err := coreconfig.ValidateCredentials(creds); err != nil {
		return nil, fmt.Errorf("initial credentials validation failed: %w", err)
	}

	configVersion := conversion.CompositeVersion(crdResources)

	logger.Info("Initial configuration validated successfully",
		"config_version", configVersion,
		"secret_version", secretResource.GetResourceVersion())

	bundle := &InitialConfigBundle{
		Config:             cfg,
		CRD:                crd,
		Credentials:        creds,
		ConfigVersion:      configVersion,
		CredentialsVersion: secretResource.GetResourceVersion(),
	}
	return bundle, nil
}

// initialValidationTestsRunTimeout is the suite budget for the LOAD-path
// validationTests gate below. It is deliberately much larger than the live
// change gate's default (validator.validationTestsRunTimeout, 25s): the live
// gate answers a scatter-gather aggregation and must self-report before the
// coordinator's configValidationTimeout, while the load gate runs once at
// startup where the only outer bound is the startup probe's 300s budget.
// On a cold, contended node (CI runners provisioning a whole cluster at
// once) the engine compile plus dozens of `haproxy -c` checks legitimately
// exceed 25s — observed as "validationTests did not complete within the
// suite timeout" crash-looping an otherwise healthy install. 120s leaves
// room for one retry iteration inside the startup probe window.
const initialValidationTestsRunTimeout = 120 * time.Second

// validateInitialConfigValidationTests runs the initial config's embedded
// validationTests synchronously and returns an error if the suite fails, runs
// incomplete, or cannot be set up. runIteration calls this on load so a restart
// or upgrade can't quietly serve a HAProxyTemplateConfig that fails its own
// tests — the live scatter-gather gate only blocks a change on an
// already-running controller, which a fresh pod bypasses.
//
// On error, runIteration returns it, leaving the controller un-initialized
// (/healthz 503). The liveness probe then restarts the pod, so a bad config
// surfaces as CrashLoopBackOff and a rolling upgrade stalls on the old, good
// pods instead of rolling out the break. A config with no validationTests
// passes at zero cost.
// validateInitialConfigValidationTests runs the fatal startup load gate. On
// failure it best-effort records WHY on the CRD status (so an operator sees the
// rejection via kubectl instead of only in a crash-looping pod's logs) and then
// returns the error — the caller stays fail-closed and crash-loops.
func validateInitialConfigValidationTests(
	ctx context.Context,
	cfg *coreconfig.Config,
	crd *v1alpha1.HAProxyTemplateConfig,
	k8sClient *client.Client,
	bootstrap validator.TypeBootstrapper,
	logger *slog.Logger,
) error {
	result, err := validator.RunValidationTestsSync(ctx, cfg, bootstrap, initialValidationTestsRunTimeout, logger)
	if err != nil {
		return fmt.Errorf("running validationTests: %w", err)
	}
	var failures []string
	switch {
	case result.Incomplete:
		failures = []string{"validationTests did not complete within the suite timeout"}
		err = errors.New(failures[0])
	case !result.Passed:
		failures = result.Failures
		err = fmt.Errorf("validationTests failed: %s", strings.Join(result.Failures, "; "))
	default:
		return nil
	}
	reportLoadGateFailure(ctx, k8sClient, crd, failures, logger)
	return err
}

// reportLoadGateFailure best-effort records the startup load-gate rejection on
// the HAProxyTemplateConfig status (via configchange.ReportConfigLoadFailure). It
// builds a throwaway CRD client because the event-driven status path isn't wired
// at load-gate time, and never returns an error — a status write must not mask or
// delay the fail-closed crash-loop.
func reportLoadGateFailure(ctx context.Context, k8sClient *client.Client, crd *v1alpha1.HAProxyTemplateConfig, failures []string, logger *slog.Logger) {
	crdClient, err := versioned.NewForConfig(k8sClient.RestConfig())
	if err != nil {
		logger.Warn("Cannot build CRD client to report load-gate failure on status", "error", err)
		return
	}
	configchange.ReportConfigLoadFailure(ctx, crdClient, crd, failures, logger)
}

// waitForInitialConfig polls until EVERY configured HAProxyTemplateConfig
// exists. This handles the race condition during fresh installs where the
// controller pod may start before the resources are available in the API
// server — and with a merged set, a partial set is just as unusable as none,
// since the libraries the operator config overrides may not be there yet.
//
// Returns nil when all are found, or ctx.Err() if context is cancelled.
func waitForInitialConfig(
	ctx context.Context,
	k8sClient *client.Client,
	crdNames []string,
	crdGVR schema.GroupVersionResource,
	state *configState,
	logger *slog.Logger,
) error {
	logger.Info("Stage 2: Fetching and validating initial configuration")
	state.SetWaiting("waiting for HAProxyTemplateConfig")

	// Try immediately first
	missing, _ := missingConfigs(ctx, k8sClient, crdGVR, crdNames)
	if len(missing) == 0 {
		logger.Info("HAProxyTemplateConfigs found", "names", crdNames)
		return nil
	}

	logger.Info("Waiting for HAProxyTemplateConfigs to become available",
		"missing", missing,
		"poll_interval", ConfigPollInterval)

	ticker := time.NewTicker(ConfigPollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			missing, err := missingConfigs(ctx, k8sClient, crdGVR, crdNames)
			if err != nil {
				// Log at debug level - transient errors during polling are expected
				logger.Debug("Error checking for HAProxyTemplateConfigs", "error", err)
				continue
			}
			if len(missing) == 0 {
				logger.Info("HAProxyTemplateConfigs found", "names", crdNames)
				return nil
			}
			logger.Debug("HAProxyTemplateConfigs not yet available, continuing to wait",
				"missing", missing)
		}
	}
}

// missingConfigs returns the names that do not exist yet, in the given order.
// A non-NotFound error aborts the check — the answer would be unreliable and
// the caller retries anyway.
func missingConfigs(ctx context.Context, k8sClient *client.Client, gvr schema.GroupVersionResource, names []string) ([]string, error) {
	var missing []string
	for _, name := range names {
		exists, err := checkConfigExists(ctx, k8sClient, gvr, name)
		if err != nil {
			return nil, err
		}
		if !exists {
			missing = append(missing, name)
		}
	}
	return missing, nil
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

// primaryConfigName is the config that represents the whole merged set: the
// last one, which by convention is the operator's own rather than a bundled
// library's. It owns the identity derived from the set — the published
// HAProxyCfg's name and the object that carries validation status — so a
// single-config install behaves exactly as it did before configs could be
// merged.
func primaryConfigName(crdNames []string) string {
	if len(crdNames) == 0 {
		return ""
	}
	return crdNames[len(crdNames)-1]
}

// logSnippetOverrides reports each templateSnippets name that more than one
// config defines. An operator overriding a bundled snippet is the documented
// escape hatch; two libraries colliding is a bug that used to resolve silently.
func logSnippetOverrides(overrides []conversion.SnippetOverride, logger *slog.Logger) {
	for _, override := range overrides {
		logger.Info("Template snippet overridden by a later config",
			"snippet", override.Name,
			"overridden_from", override.PreviousSource,
			"defined_by", override.WinningSource)
	}
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
