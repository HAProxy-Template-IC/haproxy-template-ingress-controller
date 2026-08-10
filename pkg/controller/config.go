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
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"path"
	"strings"
	"time"

	"golang.org/x/sync/errgroup"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	apitypes "k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"

	"gitlab.com/haproxy-haptic/haptic/pkg/apis/haproxytemplate/v1alpha1"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/configchange"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/conversion"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/events"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/validator"
	coreconfig "gitlab.com/haproxy-haptic/haptic/pkg/core/config"
	"gitlab.com/haproxy-haptic/haptic/pkg/generated/clientset/versioned"
	"gitlab.com/haproxy-haptic/haptic/pkg/k8s/client"
)

const (
	// metadataKey is the object field the ownerReference patch targets.
	metadataKey = "metadata"
	// configKind is the owning kind stamped onto objects the config owns.
	configKind = "HAProxyTemplateConfig"
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
	// Sources names every config of the merged set, in merge order, at the
	// generation the merge observed — for per-object status stamping.
	Sources []events.ConfigSourceRef
}

func fetchAndValidateInitialConfig(
	ctx context.Context,
	k8sClient *client.Client,
	crdName string,
	secretName string,
	crdGVR schema.GroupVersionResource,
	libraryGVR schema.GroupVersionResource,
	secretGVR schema.GroupVersionResource,
	logger *slog.Logger,
) (*InitialConfigBundle, error) {
	logger.Info("Fetching initial CRDs and credentials", "crd_name", crdName)

	var configResource *unstructured.Unstructured
	var secretResource *unstructured.Unstructured

	g, gCtx := errgroup.WithContext(ctx)

	g.Go(func() error {
		var err error
		configResource, err = k8sClient.GetResource(gCtx, crdGVR, crdName)
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

	// Which snippets to fetch is only known once the config is in hand, so this
	// runs after the group rather than inside it.
	crdResources, unresolved, err := ResolveLibraryRefs(ctx, k8sClient, libraryGVR, configResource)
	if err != nil {
		return nil, err
	}
	if len(unresolved) > 0 {
		return nil, fmt.Errorf("HAProxyTemplateConfig %q references HAProxyTemplateLibrary that are missing or at a different revision: %s",
			crdName, strings.Join(unresolved, ", "))
	}

	ensureLibraryOwnership(ctx, k8sClient, libraryGVR, crdResources, logger)

	// Parse initial configuration
	logger.Info("Parsing initial configuration and credentials")

	crdResource, overrides, err := conversion.MergeSpecs(crdResources)
	if err != nil {
		return nil, fmt.Errorf("merging initial HAProxyTemplateConfigs: %w", err)
	}
	logSpecOverrides(overrides, logger)

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

	configVersion := conversion.CompositeVersion(crdResources)

	logger.Info("Initial configuration validated successfully",
		"config_version", configVersion,
		"secret_version", secretResource.GetResourceVersion())

	sourceRefs := make([]events.ConfigSourceRef, 0, len(crdResources))
	for _, resource := range crdResources {
		sourceRefs = append(sourceRefs, events.ConfigSourceRef{
			Namespace:  resource.GetNamespace(),
			Name:       resource.GetName(),
			Generation: resource.GetGeneration(),
		})
	}

	bundle := &InitialConfigBundle{
		Config:             cfg,
		CRD:                crd,
		Credentials:        creds,
		ConfigVersion:      configVersion,
		CredentialsVersion: secretResource.GetResourceVersion(),
		Sources:            sourceRefs,
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
// cfg is the EFFECTIVE config (post installEffectiveConfig) — NOT
// bundle.Config, which still carries the snippets and tests that the
// resolution stripped for unavailable optional resources; compiling those
// against a cluster without their schemas fails the gate spuriously. The
// bundle contributes only the source refs for the status write-back.
func validateInitialConfigValidationTests(
	ctx context.Context,
	cfg *coreconfig.Config,
	bundle *InitialConfigBundle,
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
	reportLoadGateFailure(ctx, k8sClient, bundle, failures, logger)
	return err
}

// reportLoadGateFailure best-effort records the startup load-gate rejection on
// the HAProxyTemplateConfig status (via configchange.ReportConfigLoadFailure). It
// builds a throwaway CRD client because the event-driven status path isn't wired
// at load-gate time, and never returns an error — a status write must not mask or
// delay the fail-closed crash-loop.
func reportLoadGateFailure(ctx context.Context, k8sClient *client.Client, bundle *InitialConfigBundle, failures []string, logger *slog.Logger) {
	crdClient, err := versioned.NewForConfig(k8sClient.RestConfig())
	if err != nil {
		logger.Warn("Cannot build CRD client to report load-gate failure on status", "error", err)
		return
	}
	// Every source of the merged set gets the verdict: the failure is a
	// property of the set, and an operator describes whichever object they
	// edited (ADR-0016).
	for _, ref := range bundle.Sources {
		configchange.ReportConfigLoadFailure(ctx, crdClient, ref, failures, logger)
	}
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
	crdName string,
	crdGVR schema.GroupVersionResource,
	libraryGVR schema.GroupVersionResource,
	state *configState,
	logger *slog.Logger,
) error {
	logger.Info("Stage 2: Fetching and validating initial configuration")
	state.SetWaiting("waiting for HAProxyTemplateConfig")

	// Try immediately first
	missing, _ := incompleteConfigSet(ctx, k8sClient, crdGVR, libraryGVR, crdName)
	if len(missing) == 0 {
		logger.Info("HAProxyTemplateConfig and its snippets found", "name", crdName)
		return nil
	}

	logger.Info("Waiting for the HAProxyTemplateConfig set to become available",
		"missing", missing,
		"poll_interval", ConfigPollInterval)

	ticker := time.NewTicker(ConfigPollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			missing, err := incompleteConfigSet(ctx, k8sClient, crdGVR, libraryGVR, crdName)
			if err != nil {
				// Log at debug level - transient errors during polling are expected
				logger.Debug("Error checking for the HAProxyTemplateConfig set", "error", err)
				continue
			}
			if len(missing) == 0 {
				logger.Info("HAProxyTemplateConfig and its snippets found", "name", crdName)
				return nil
			}
			logger.Debug("HAProxyTemplateConfig set not yet complete, continuing to wait",
				"missing", missing)
		}
	}
}

// incompleteConfigSet reports what still stands between the controller and a
// renderable configuration: the config object itself, or any snippet it
// references that is absent or at a different revision.
func incompleteConfigSet(
	ctx context.Context,
	k8sClient *client.Client,
	crdGVR schema.GroupVersionResource,
	libraryGVR schema.GroupVersionResource,
	crdName string,
) ([]string, error) {
	config, err := k8sClient.GetResource(ctx, crdGVR, crdName)
	if err != nil {
		if apierrors.IsNotFound(err) {
			return []string{fmt.Sprintf("HAProxyTemplateConfig %s (missing)", crdName)}, nil
		}
		return nil, err
	}

	_, unresolved, err := ResolveLibraryRefs(ctx, k8sClient, libraryGVR, config)
	if err != nil {
		return nil, err
	}
	return unresolved, nil
}

// logSpecOverrides reports each guarded-section name the last config in the
// merge order redefines. An operator overriding a bundled entry is the
// documented escape hatch; a collision anywhere earlier in the order is an
// error inside MergeSpecs and never reaches here.
func logSpecOverrides(overrides []conversion.SpecOverride, logger *slog.Logger) {
	for _, override := range overrides {
		logger.Info("Config entry overridden by the last config in the merge order",
			"section", override.Section,
			"name", override.Name,
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

// ResolveLibraryRefs fetches the HAProxyTemplateLibrary a config references,
// in merge order, and returns them followed by the config itself — the config
// is last so its inline content wins over every referenced snippet.
//
// A reference resolves only when the object exists AND its spec.revision equals
// the revision the reference names. Both strings come from whoever wrote the
// objects; nothing here derives a revision from content, so an operator editing
// a snippet in place keeps a resolving reference while a half-applied set does
// not. Unresolved references are returned for the caller to report, not
// treated as an error.
func ResolveLibraryRefs(
	ctx context.Context,
	k8sClient *client.Client,
	libraryGVR schema.GroupVersionResource,
	config *unstructured.Unstructured,
) (sources []*unstructured.Unstructured, unresolved []string, err error) {
	refs, found, err := unstructured.NestedSlice(config.Object, "spec", "libraryRefs")
	if err != nil {
		return nil, nil, fmt.Errorf("reading spec.libraryRefs: %w", err)
	}
	if !found {
		return []*unstructured.Unstructured{config}, nil, nil
	}

	sources = make([]*unstructured.Unstructured, 0, len(refs)+1)
	for i, entry := range refs {
		fields, ok := entry.(map[string]any)
		if !ok {
			return nil, nil, fmt.Errorf("spec.libraryRefs[%d] is not an object", i)
		}
		name, _ := fields["name"].(string)
		want, _ := fields["revision"].(string)
		if name == "" || want == "" {
			return nil, nil, fmt.Errorf("spec.libraryRefs[%d] needs both name and revision", i)
		}

		observed, getErr := k8sClient.GetResource(ctx, libraryGVR, name)
		if getErr != nil {
			if !apierrors.IsNotFound(getErr) {
				return nil, nil, fmt.Errorf("fetching HAProxyTemplateLibrary %q: %w", name, getErr)
			}
			unresolved = append(unresolved, fmt.Sprintf("%s (missing)", name))
			continue
		}
		got, _, _ := unstructured.NestedString(observed.Object, "spec", "revision")
		if got != want {
			unresolved = append(unresolved, fmt.Sprintf("%s (want revision %q, have %q)", name, want, got))
			continue
		}
		sources = append(sources, observed)
	}

	return append(sources, config), unresolved, nil
}

// ensureLibraryOwnership stamps an ownerReference from the config onto each
// HAProxyTemplateLibrary it references, so resource-tree views (Argo CD,
// `kubectl tree`) show the relationship and `helm uninstall` cannot strand the
// content objects.
//
// The chart cannot do this: an ownerReference needs the owner's UID, which does
// not exist until the config is applied. Best-effort — a failure here must
// never stop a valid configuration from loading, so every error is logged and
// swallowed.
func ensureLibraryOwnership(
	ctx context.Context,
	k8sClient *client.Client,
	libraryGVR schema.GroupVersionResource,
	sources []*unstructured.Unstructured,
	logger *slog.Logger,
) {
	config := conversion.ConfigOf(sources)
	if config == nil {
		return
	}
	owner := metav1.OwnerReference{
		APIVersion: config.GetAPIVersion(),
		Kind:       config.GetKind(),
		Name:       config.GetName(),
		UID:        config.GetUID(),
		Controller: ptr.To(true),
	}
	if owner.UID == "" {
		return
	}

	for _, library := range conversion.LibrariesOf(sources) {
		if hasOwner(library.GetOwnerReferences(), owner.UID) {
			continue
		}
		patch, err := json.Marshal(map[string]any{
			metadataKey: map[string]any{
				"ownerReferences": append(withoutSupersededController(library.GetOwnerReferences(), &owner), owner),
			},
		})
		if err != nil {
			logger.Warn("Building the ownerReference patch failed", "library", library.GetName(), "error", err)
			continue
		}
		if _, err := k8sClient.DynamicClient().
			Resource(libraryGVR).
			Namespace(library.GetNamespace()).
			Patch(ctx, library.GetName(), apitypes.MergePatchType, patch, metav1.PatchOptions{}); err != nil {
			logger.Warn("Could not stamp the config as owner of a HAProxyTemplateLibrary; "+
				"resource-tree views will not show the relationship",
				"library", library.GetName(), "error", err)
			continue
		}
		logger.Debug("Stamped ownerReference on HAProxyTemplateLibrary", "library", library.GetName())
	}
}

func hasOwner(refs []metav1.OwnerReference, uid apitypes.UID) bool {
	for _, ref := range refs {
		if ref.UID == uid {
			return true
		}
	}
	return false
}

// withoutSupersededController drops a controller reference left by an earlier
// incarnation of the same owner — same kind and name, different UID.
//
// Deleting and recreating the config gives it a new UID. A library that
// outlives that (orphan or foreground deletion, or one the collector has not
// reached) still carries the old reference, and an object may have only ONE
// controller reference: appending would make the apiserver reject the patch.
// Since the failure is swallowed, the tree relationship would then never be
// re-established and nothing would say why.
func withoutSupersededController(
	refs []metav1.OwnerReference,
	owner *metav1.OwnerReference,
) []metav1.OwnerReference {
	kept := make([]metav1.OwnerReference, 0, len(refs))
	for _, ref := range refs {
		superseded := ref.Controller != nil && *ref.Controller &&
			ref.Kind == owner.Kind && ref.APIVersion == owner.APIVersion &&
			ref.Name == owner.Name && ref.UID != owner.UID
		if superseded {
			continue
		}
		kept = append(kept, ref)
	}
	return kept
}
