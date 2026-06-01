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

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/conversion"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/helpers"
	ctrlhttpstore "gitlab.com/haproxy-haptic/haptic/pkg/controller/httpstore"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/pipeline"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/renderer"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/validation"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane"
	"gitlab.com/haproxy-haptic/haptic/pkg/stores"
)

// HAProxyTemplateConfigGVK is the canonical GVK string the webhook server
// uses to dispatch admission requests for the controller's own CRD.
const HAProxyTemplateConfigGVK = "haproxy-haptic.org/v1alpha1.HAProxyTemplateConfig"

// ConfigValidator validates a prospective HAProxyTemplateConfig admission by:
//  1. Parsing the admitted CRD spec into a *config.Config.
//  2. Compiling an ephemeral template engine from the prospective templates.
//  3. Building an ephemeral RenderService + Pipeline (strict ValidationService)
//     and executing it against the controller's CURRENT resource stores.
//  4. Mapping the result to an admission decision.
//
// This is the upstream gate that lets the leader-side reconcile pipeline
// safely skip `haproxy -c` — see project_haptic_rolling_restart_root_cause.md.
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
	// into the typed shape expected by templates.
	TypedResourceTypes map[string]reflect.Type
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

	// Compile an ephemeral template engine from the prospective config's
	// templates. Failure here means a Scriggo-level syntax problem in the
	// templates — surface it as a render-phase error (same simplification
	// path operators see for any other render failure).
	engine, err := helpers.NewEngineFromConfigWithOptions(cfg, nil, nil, v.declarations, helpers.EngineOptions{})
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
		TypedResourceTypes: v.typedResourceTypes,
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

	v.logger.Debug("HAProxyTemplateConfig admission validated successfully",
		"namespace", namespace,
		"name", name,
		"operation", operation)
	return true, "", nil
}
