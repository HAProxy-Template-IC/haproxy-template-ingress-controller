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

package webhook

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"reflect"
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/configtest"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/conversion"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/helpers"
	ctrlhttpstore "gitlab.com/haproxy-haptic/haptic/pkg/controller/httpstore"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/pipeline"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/renderer"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/typebootstrap"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/validation"
	coreconfig "gitlab.com/haproxy-haptic/haptic/pkg/core/config"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane"
	"gitlab.com/haproxy-haptic/haptic/pkg/stores"
)

// SchemaBootstrapper resolves the typed-resource reflect.Types and declarations
// for a prospective config from live schemas. It mirrors the
// validator.TypeBootstrapper the daemon load gate uses; the two share the same
// underlying signature so the controller wiring can convert one to the other.
//
// When set on a ConfigValidator, admission bootstraps schemas from the
// PROSPECTIVE config — exactly as the load gate does on load — so the two gates
// build the test/render engine from the same type set. This is what makes the
// "a config the webhook admits will load" guarantee hold even when the
// prospective config changes its own typed `watchedResources` relative to what
// the controller was started with. Without it, the webhook validated against
// the startup-fixed type set, which could both false-reject a config that adds
// a new typed watched resource and false-admit one whose tests pass under the
// stale types but fail under the new ones.
type SchemaBootstrapper func(ctx context.Context, cfg *coreconfig.Config) (*typebootstrap.Result, error)

// HAProxyTemplateConfigGVK is the canonical GVK string the webhook server
// uses to dispatch admission requests for the controller's own CRD.
const HAProxyTemplateConfigGVK = "haproxy-haptic.org/v1alpha1.HAProxyTemplateConfig"

// configValidationTestsBudget bounds the validationTests run at admission.
//
// It is sized so the suite RELIABLY gets its full budget within the config-GVK
// internal deadline (configAdmissionTimeout = 9s in component.go), accounting
// for everything that runs first on the same context:
//
//	schema bootstrap (≤ typeBootstrapFetchTimeout = 2s)
//	+ render + `haproxy -c`        (≤ ~2s even for a large config)
//	+ this budget                  (5s)
//	= 9s internal deadline         (< 10s chart timeoutSeconds)
//
// Because RunValidationTests bounds the run with context.WithTimeout(ctx,
// budget) — i.e. min(budget, time left on the admission ctx) — the suite gets
// the full 5s whenever bootstrap+render ≤ 4s, which always holds (bootstrap
// self-caps at 2s, render is sub-second in practice). The bundled suite is
// ~3.9s, so 5s leaves margin. A suite that still can't finish is admitted with a
// warning (the load gate enforces authoritatively on load) rather than blocking
// the apply.
const configValidationTestsBudget = 5 * time.Second

// ConfigValidator validates a prospective HAProxyTemplateConfig admission by:
//  1. Parsing the admitted CRD spec into a *config.Config.
//  2. Compiling an ephemeral template engine from the prospective templates.
//  3. Building an ephemeral RenderService + Pipeline (strict ValidationService)
//     and executing it against the controller's CURRENT resource stores.
//  4. Mapping the result to an admission decision.
//
// This is the upstream gate that lets the leader-side reconcile pipeline
// safely skip `haproxy -c`.
//
// Failure-policy on the chart-side ValidatingWebhookConfiguration is `Ignore`,
// so this validator MUST be safe to be entirely absent: when the webhook is
// unreachable, the dataplane API still runs `haproxy -c` server-side before
// accepting any /raw push, and the controller surfaces the resulting failure
// via HAProxyCfg.status.
type ConfigValidator struct {
	logger             *slog.Logger
	strictValidator    *validation.ValidationService
	storeProvider      stores.StoreProvider
	capabilities       dataplane.Capabilities
	httpStoreComponent *ctrlhttpstore.Component
	declarations       map[string]any
	typedResourceTypes map[string]reflect.Type
	bootstrap          SchemaBootstrapper
	effectiveResolver  func(ctx context.Context, cfg *coreconfig.Config) (*coreconfig.Config, error)
}

// ConfigValidatorConfig wires the ConfigValidator's dependencies. All fields
// are required except HTTPStoreComponent (templates that use `http.Fetch`
// need it; chart-default templates do not).
type ConfigValidatorConfig struct {
	// Logger is the structured logger.
	Logger *slog.Logger

	// StrictValidator is the strict ValidationService used for the
	// admission webhook (full syntax + schema + `haproxy -c` semantic
	// validation). MUST be the strict instance — the whole point of this
	// gate is to catch bad templates that the fast leader-side pipeline
	// would skip.
	StrictValidator *validation.ValidationService

	// StoreProvider grants access to the controller's live resource
	// stores. The prospective config is rendered against current cluster
	// state — no overlay is applied (unlike DryRunValidator, which is
	// validating a hypothetical resource addition/update).
	StoreProvider stores.StoreProvider

	// Capabilities are the HAProxy version capabilities the ephemeral
	// RenderService needs to compute capability-conditional output.
	Capabilities dataplane.Capabilities

	// HTTPStoreComponent (optional) wires `http.Fetch` for templates that
	// use it. Pass the live controller's instance — admission renders
	// against accepted HTTP-store content, same as DryRunValidator.
	HTTPStoreComponent *ctrlhttpstore.Component

	// Declarations carries the typed-resource globals from typebootstrap
	// (and the currentConfig declaration). The ephemeral engine MUST be
	// constructed with these so chart templates compile identically
	// against either render path.
	Declarations map[string]any

	// TypedResourceTypes is the per-resource generated Go type map fed
	// to the ephemeral RenderService so it wraps each store snapshot
	// into the typed shape expected by templates. Used as the fallback
	// when Bootstrap is nil or its live-schema resolution fails.
	TypedResourceTypes map[string]reflect.Type

	// Bootstrap (optional) resolves typed schemas from the PROSPECTIVE config
	// at admission, so admission builds the engine from the same type set the
	// daemon load gate will use on load (true parity — see SchemaBootstrapper).
	// When nil, the validator falls back to the startup-fixed Declarations /
	// TypedResourceTypes (the prior behavior; still correct for configs that
	// don't change their own typed watchedResources). Production wires the live
	// bootstrapper; unit tests may leave it nil or pass a stub.
	Bootstrap SchemaBootstrapper

	// EffectiveResolver (optional) transforms the parsed prospective config
	// into the EFFECTIVE config before validation — the same transformation
	// the load gate applies (candidate apiVersions resolved against live
	// discovery, requires/requiresFields-unsatisfied snippets and tests
	// stripped; see coreconfig.ResolveEffective). Without it, admission
	// compiles snippets the controller itself would strip — on a cluster
	// without Gateway API CRDs the chart's gateway snippets then fail typed
	// compilation and EVERY config update is denied (issue #79). Production
	// wires the iteration's resolver; nil (unit tests) validates the raw
	// config as-is.
	EffectiveResolver func(ctx context.Context, cfg *coreconfig.Config) (*coreconfig.Config, error)
}

// NewConfigValidator constructs a ConfigValidator. Panics if any required
// field is nil — these are construction-time mistakes that should fail
// loudly, not at admission time.
func NewConfigValidator(cfg *ConfigValidatorConfig) *ConfigValidator {
	if cfg.StrictValidator == nil {
		panic("ConfigValidator: StrictValidator is required")
	}
	if cfg.StoreProvider == nil {
		panic("ConfigValidator: StoreProvider is required")
	}
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &ConfigValidator{
		logger:             logger.With("component", "configvalidator"),
		strictValidator:    cfg.StrictValidator,
		storeProvider:      cfg.StoreProvider,
		capabilities:       cfg.Capabilities,
		httpStoreComponent: cfg.HTTPStoreComponent,
		declarations:       cfg.Declarations,
		typedResourceTypes: cfg.TypedResourceTypes,
		bootstrap:          cfg.Bootstrap,
		effectiveResolver:  cfg.EffectiveResolver,
	}
}

// ValidateDirect performs synchronous validation of a prospective
// HAProxyTemplateConfig admission. Returns the same triple the webhook
// component's DryRunValidator interface returns so it plugs into the same
// ValidationFunc bridge.
//
// On DELETE: admits without rendering — the deletion can't render anything.
// On CREATE/UPDATE: parses the prospective CRD, builds an ephemeral
// pipeline, executes against live stores, maps result to a decision.
func (v *ConfigValidator) ValidateDirect(ctx context.Context, gvk, namespace, name string, object any, operation string) (allowed bool, reason string, warnings []string) {
	v.logger.Debug("Direct HAProxyTemplateConfig validation request",
		"gvk", gvk,
		"namespace", namespace,
		"name", name,
		"operation", operation)

	if operation == "DELETE" {
		return true, "", nil
	}

	u, ok := object.(*unstructured.Unstructured)
	if !ok {
		return false, fmt.Sprintf("expected *unstructured.Unstructured, got %T", object), nil
	}

	cfg, _, err := conversion.ParseCRD(u)
	if err != nil {
		return false, fmt.Sprintf("parsing HAProxyTemplateConfig: %v", err), nil
	}

	// Resolve the EFFECTIVE config first — the load gate strips
	// requires/requiresFields-unsatisfied snippets and tests against live
	// resource availability before compiling, and admission must judge the
	// same config the controller would actually load (issue #79). Must run
	// BEFORE resolveSchemas so typebootstrap probes only resolved-version
	// resources. On resolver error, admit with a warning rather than compile
	// the raw config (which would reproduce the bug) or deny (which would
	// block operator applies on transient apiserver blips): the load gate
	// still deterministically enforces the config, matching this webhook's
	// safe-to-be-absent posture.
	if v.effectiveResolver != nil {
		effective, resolveErr := v.effectiveResolver(ctx, cfg)
		if resolveErr != nil {
			v.logger.Warn("Effective-config resolution failed at admission; admitting (load gate still enforces)",
				"namespace", namespace, "name", name, "error", resolveErr)
			return true, "", []string{fmt.Sprintf(
				"effective-config resolution failed at admission: %v — validation skipped; the controller's load gate will still enforce this config", resolveErr)}
		}
		cfg = effective
	}

	// Resolve the typed-resource declarations + reflect.Types this admission
	// renders/tests against, bootstrapping from the PROSPECTIVE config when a
	// live bootstrapper is wired (true parity with the load gate).
	declarations, typedResourceTypes := v.resolveSchemas(ctx, cfg, namespace, name)

	// Compile an ephemeral template engine from the prospective config's
	// templates. Failure here means a Scriggo-level syntax problem in the
	// templates — surface it as a render-phase error (same simplification
	// path operators see for any other render failure).
	engine, err := helpers.NewEngineFromConfigWithOptions(cfg, nil, nil, declarations, helpers.EngineOptions{})
	if err != nil {
		return false, dataplane.SimplifyRenderingError(fmt.Errorf("compiling templates: %w", err)), nil
	}

	// Ephemeral RenderService. HAProxyPodStore and CurrentConfigStore are
	// intentionally nil — the webhook validates the prospective config
	// against the cluster's CURRENT routing resources only; pod/HTTP-store
	// state is not part of admission semantics.
	rs := renderer.NewRenderService(&renderer.RenderServiceConfig{
		Engine:             engine,
		Config:             cfg,
		Logger:             v.logger,
		Capabilities:       v.capabilities,
		HTTPStoreComponent: v.httpStoreComponent,
		TypedResourceTypes: typedResourceTypes,
	})

	// Strict pipeline: runs full syntax + schema + `haproxy -c`. This is
	// the whole point of the upstream gate — catch bad templates here so
	// the leader-side pipeline can skip `haproxy -c` and shave 94 ms off
	// rolling-restart reactions.
	p := pipeline.New(&pipeline.PipelineConfig{
		Renderer:  rs,
		Validator: v.strictValidator,
		Logger:    v.logger,
	})

	_, valResult, execErr := p.ExecuteWithResult(ctx, v.storeProvider)
	if execErr != nil {
		var perr *pipeline.PipelineError
		if errors.As(execErr, &perr) && perr.Phase == pipeline.PhaseRender {
			return false, dataplane.SimplifyRenderingError(perr.Cause), nil
		}
		return false, execErr.Error(), nil
	}

	if valResult != nil && !valResult.Valid {
		return false, dataplane.SimplifyValidationError(valResult.Error), nil
	}

	// Run the config's embedded validationTests at admission, via the same
	// configtest helper AND (when a bootstrapper is wired) the same
	// prospective-config schema set the daemon load gate uses. This is what
	// keeps the two consistent: a config admitted here will load — so a config
	// that FAILS its own tests is refused now, never entering etcd to crash-loop
	// the next fresh controller pod. The run is bounded so it can't approach the
	// webhook timeout; on an incomplete run (pathologically large suite /
	// contention) we admit with a warning and let the load gate enforce — the
	// webhook is failurePolicy:Ignore, so this path never blocks an operator's
	// recovery apply. (Already-observed failures still deny even on a cut-short
	// run; see configtest.RunValidationTests.)
	testResult, testErr := configtest.RunValidationTests(ctx, cfg, engine, typedResourceTypes, configValidationTestsBudget, v.logger)
	switch {
	case testErr != nil:
		v.logger.Warn("ValidationTests could not run at admission; admitting (load gate still enforces)",
			"namespace", namespace, "name", name, "error", testErr)
		return true, "", []string{fmt.Sprintf("validationTests could not run at admission: %v — the controller's load gate will still enforce them", testErr)}
	case testResult.Incomplete:
		v.logger.Warn("ValidationTests did not finish within the admission budget; admitting (load gate still enforces)",
			"namespace", namespace, "name", name, "budget", configValidationTestsBudget)
		return true, "", []string{fmt.Sprintf("validationTests did not finish within %s at admission and were not fully checked here — the controller's load gate will enforce them", configValidationTestsBudget)}
	case !testResult.Passed:
		v.logger.Info("HAProxyTemplateConfig admission denied: validationTests failed",
			"namespace", namespace, "name", name, "failures", testResult.Failures)
		return false, "validationTests failed:\n  " + strings.Join(testResult.Failures, "\n  "), nil
	}

	v.logger.Debug("HAProxyTemplateConfig admission validated successfully",
		"namespace", namespace,
		"name", name,
		"operation", operation)
	return true, "", nil
}

// resolveSchemas returns the typed-resource declarations + reflect.Types the
// admission render/test should use for cfg. When a live bootstrapper is wired,
// it derives them from the PROSPECTIVE config — exactly as the daemon load gate
// does on load — so both gates build the engine from the same type set (true
// parity: a config the webhook admits will load, even when it changes its own
// typed watchedResources relative to the running controller).
//
// On bootstrap failure it degrades to the startup-fixed wiring rather than
// skipping validation: a transient schema-fetch failure shouldn't blind the
// gate, and the load gate re-bootstraps and enforces on load anyway. With no
// bootstrapper wired (unit tests), it returns the startup-fixed wiring directly.
func (v *ConfigValidator) resolveSchemas(ctx context.Context, cfg *coreconfig.Config, namespace, name string) (declarations map[string]any, typedResourceTypes map[string]reflect.Type) {
	if v.bootstrap == nil {
		return v.declarations, v.typedResourceTypes
	}
	bootstrapResult, err := v.bootstrap(ctx, cfg)
	if err != nil {
		v.logger.Warn("Per-admission schema bootstrap failed; validating with startup-fixed schemas (load gate re-bootstraps on load)",
			"namespace", namespace, "name", name, "error", err)
		return v.declarations, v.typedResourceTypes
	}
	return helpers.BuildAdditionalDeclarations(cfg, bootstrapResult), bootstrapResult.Types
}
