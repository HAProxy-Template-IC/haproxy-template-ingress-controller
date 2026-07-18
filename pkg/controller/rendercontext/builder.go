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

// Package rendercontext provides a centralized builder for template rendering contexts.
//
// This package consolidates the previously duplicated context creation logic from
// renderer, testrunner, benchmark, and dryrunvalidator into a single, reusable builder.
//
// Usage:
//
//	builder := rendercontext.NewBuilder(
//	    cfg,
//	    pathResolver,
//	    logger,
//	    rendercontext.WithStores(stores),
//	)
//	res := builder.Build()
//	ctx := res.Context
package rendercontext

import (
	"cmp"
	"fmt"
	"log/slog"
	"maps"
	"reflect"
	"runtime"
	"slices"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/names"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/typebootstrap"
	"gitlab.com/haproxy-haptic/haptic/pkg/core/config"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/parser/parserconfig"
	"gitlab.com/haproxy-haptic/haptic/pkg/k8s/typegen"
	"gitlab.com/haproxy-haptic/haptic/pkg/stores"
	"gitlab.com/haproxy-haptic/haptic/pkg/templating"
)

// Builder constructs template rendering contexts with consistent structure.
// Use NewBuilder() to create a builder and functional options to configure it.
type Builder struct {
	// Required dependencies
	config       *config.Config
	pathResolver *templating.PathResolver
	logger       *slog.Logger

	// Optional dependencies (set via options)
	stores             map[string]stores.Store
	haproxyPodStore    stores.Store
	httpFetcher        templating.HTTPFetcher
	currentConfig      *parserconfig.StructuredConfig
	typedResourceTypes map[string]reflect.Type
	capabilities       dataplane.Capabilities
	renderMode         RenderMode
}

// RenderMode tells conflict-style template checks whether this render is an
// admission dry-run — a proposed change being validated, where the check should
// fail loud so the webhook denies it — or a normal reconcile render of existing
// live state, where the check should warn (via recordEvent) and let the render
// proceed so one already-present bad resource can't block the whole fleet's
// config. It is exposed to templates as the `renderMode` string global.
type RenderMode string

const (
	// RenderModeReconcile is the live reconcile render: lenient (warn, never
	// abort). It is the safe default when unset.
	RenderModeReconcile RenderMode = "reconcile"
	// RenderModeAdmission is a webhook / proposed-change validation render:
	// strict (fail, so admission denies the change).
	RenderModeAdmission RenderMode = "admission"
)

// Option configures a Builder.
type Option func(*Builder)

// WithStores sets the resource stores for the template context.
// Each store is wrapped in a StoreWrapper to provide template-friendly methods.
func WithStores(storeMap map[string]stores.Store) Option {
	return func(b *Builder) {
		b.stores = storeMap
	}
}

// WithHAProxyPodStore sets the HAProxy pod store for controller.haproxy_pods.
// This enables templates to access HAProxy pod count for calculations.
func WithHAProxyPodStore(store stores.Store) Option {
	return func(b *Builder) {
		b.haproxyPodStore = store
	}
}

// WithHTTPFetcher sets the HTTP fetcher for http.Fetch() calls in templates.
// Pass nil to disable HTTP fetching capability.
func WithHTTPFetcher(fetcher templating.HTTPFetcher) Option {
	return func(b *Builder) {
		b.httpFetcher = fetcher
	}
}

// WithCurrentConfig sets the current deployed HAProxy config for templates.
// This enables slot-aware server assignment and other config-aware features.
// The config is parsed from the HAProxyCfg CRD's spec.content field.
// If nil, templates receive nil currentConfig (first deployment case).
func WithCurrentConfig(cfg *parserconfig.StructuredConfig) Option {
	return func(b *Builder) {
		b.currentConfig = cfg
	}
}

// WithCapabilities sets the HAProxy version capabilities exposed to templates
// under the top-level "capabilities" key. The production renderer always
// injects this key, so validation and benchmark contexts must too — otherwise
// a template branching on `capabilities.supports_crt_list` (and similar)
// behaves differently between `controller validate` and production, defeating
// the purpose of pre-flight validation. When unset, Build() still populates
// "capabilities" with an all-false map (zero-value Capabilities), matching a
// no-capability HAProxy rather than omitting the key.
func WithCapabilities(caps dataplane.Capabilities) Option {
	return func(b *Builder) {
		b.capabilities = caps
	}
}

// WithRenderMode sets the RenderMode exposed to templates under "renderMode".
// When unset, Build() defaults to RenderModeReconcile — the lenient mode — so a
// caller that forgets to set it degrades to warn-and-proceed rather than failing
// a live render.
func WithRenderMode(mode RenderMode) Option {
	return func(b *Builder) {
		b.renderMode = mode
	}
}

// WithTypedResources supplies the per-resource generated Go types
// produced by typebootstrap (pkg/controller/typebootstrap). When set,
// Build emits one *additional* top-level context entry per supplied
// type: the resource's name maps to a *[]*<generated-struct> value
// populated by wrapping each item of the matching store's snapshot
// through typegen.WrapInto.
//
// The typed entries coexist with the existing map-keyed
// resources["<name>"] access — chart templates can adopt the typed
// shape per snippet without breaking templates that still use the
// untyped path. The two access paths may load their snapshots a
// few microseconds apart; templates are expected to use ONE shape
// or the OTHER for a given resource within a single render
// (mixing wouldn't compile anyway — the untyped variable is `any`).
//
// Resources whose names appear in `types` but for which no store
// is registered (via WithStores) are silently skipped. That keeps
// the option safe to use even when typebootstrap successfully
// generated a type for a resource the local controller doesn't
// happen to watch — a common case in tests.
func WithTypedResources(types map[string]reflect.Type) Option {
	return func(b *Builder) {
		b.typedResourceTypes = types
	}
}

// NewBuilder creates a new context builder with required dependencies.
//
// Parameters:
//   - cfg: Controller configuration (required)
//   - pathResolver: Path resolver for file paths (required)
//   - logger: Structured logger (required)
//   - opts: Optional configuration via functional options
func NewBuilder(cfg *config.Config, pathResolver *templating.PathResolver, logger *slog.Logger, opts ...Option) *Builder {
	b := &Builder{
		config:       cfg,
		pathResolver: pathResolver,
		logger:       logger,
	}

	for _, opt := range opts {
		opt(b)
	}

	return b
}

// BuildResult is the bundle returned from Build(). Callers that only need a
// subset (e.g. just the context map for a benchmark or test fixture) read the
// relevant field; the unused collectors are then garbage-collected with the
// result struct.
type BuildResult struct {
	Context                   map[string]any
	FileRegistry              *FileRegistry
	StatusPatchCollector      *templating.StatusPatchCollector
	RenderedResourceCollector *templating.RenderedResourceCollector
	EventCollector            *templating.EventCollector
}

// Build creates the template rendering context, file registry, status patch
// collector, and rendered resource collector.
//
// The context structure is:
//
//	{
//	  "resources": map of StoreWrappers,
//	  "controller": {"haproxy_pods": StoreWrapper},
//	  "templateSnippets": []string,
//	  "fileRegistry": FileRegistry,
//	  "statusPatchCollector": StatusPatchCollector,
//	  "renderedResourceCollector": RenderedResourceCollector,
//	  "pathResolver": PathResolver,
//	  "dataplane": Config.Dataplane,
//	  "capabilities": map[string]any (HAProxy version capabilities),
//	  "currentConfig": *StructuredConfig (nil on first deployment),
//	  "shared": map[string]any,
//	  "runtimeEnvironment": RuntimeEnvironment,
//	  "http": HTTPFetcher (if set),
//	  "extraContext": map from config,
//	}
func (b *Builder) Build() *BuildResult {
	// Create controller namespace with typed ResourceStore values. The
	// haproxy-pods watcher is auto-injected by ResourceWatcherComponent
	// with a fixed IndexBy of ["metadata.namespace", "metadata.name"]
	// (see pkg/controller/resourcewatcher/watcher.go) — mirror that here
	// so the wrapper's snapshot index agrees with the underlying store.
	//
	// `resources` is no longer a map[string]ResourceStore. It is a
	// dynamically-built struct value (see addTypedResources below); chart
	// templates reach it via direct field access (`resources.gateways`)
	// rather than the previous map+method shape (`resources.gateways.List()`).
	controller := make(map[string]templating.ResourceStore)
	if b.haproxyPodStore != nil {
		b.logger.Debug("Wrapping HAProxy pods store for rendering context")
		controller["haproxy_pods"] = &StoreWrapper{
			Store:        b.haproxyPodStore,
			ResourceType: names.HAProxyPodsResourceType,
			Logger:       b.logger,
			IndexBy:      []string{"metadata.namespace", "metadata.name"},
		}
	}

	// Sort template snippet names alphabetically
	snippetNames := SortSnippetNames(b.config.TemplateSnippets)

	// Create file registry for dynamic auxiliary file registration
	fileRegistry := NewFileRegistry(b.pathResolver)

	// Create status patch collector for template-driven status updates
	statusPatchCollector := templating.NewStatusPatchCollector()

	// Create event collector for template-driven Kubernetes Events (recordEvent).
	// Resource-agnostic like the status patch collector: templates pass any
	// apiVersion / kind for the involved object.
	eventCollector := templating.NewEventCollector()

	// Create rendered resource collector for template-driven owned-resource
	// reconciliation — anything where the chart needs the controller to spawn /
	// update / prune Kubernetes resources alongside the HAProxy config. The
	// collector is resource-agnostic: templates pass any apiVersion / kind.
	renderedResourceCollector := templating.NewRenderedResourceCollector()

	b.logger.Debug("Rendering context built",
		"resource_count", len(b.typedResourceTypes),
		"controller_fields", len(controller),
		"snippet_count", len(snippetNames))

	// Build final context. `resources` is populated below by
	// addTypedResources — leaving it absent here lets that helper
	// hand the dynamically-typed struct value into the map without a
	// throwaway placeholder.
	templateContext := map[string]any{
		"controller":                controller,
		"templateSnippets":          snippetNames,
		"fileRegistry":              fileRegistry,
		"statusPatchCollector":      statusPatchCollector,
		"recordEventCollector":      eventCollector,
		"renderedResourceCollector": renderedResourceCollector,
		"pathResolver":              b.pathResolver,
		"dataplane":                 b.config.Dataplane,
		"capabilities":              CapabilitiesToMap(&b.capabilities),
		"renderMode":                string(cmp.Or(b.renderMode, RenderModeReconcile)),
		"shared":                    templating.NewSharedContext(),
		"runtimeEnvironment": &templating.RuntimeEnvironment{
			GOMAXPROCS: runtime.GOMAXPROCS(0),
		},
	}

	// Add current config if provided (NOT added when nil - Scriggo panics with nil pointer initializers)
	// This enables slot-aware server assignment during rolling deployments
	// Templates should use isNil(currentConfig) to check if it's available
	if b.currentConfig != nil {
		templateContext["currentConfig"] = b.currentConfig
	}

	// Add HTTP fetcher if provided
	if b.httpFetcher != nil {
		b.logger.Debug("HTTP object added to template context")
		templateContext["http"] = b.httpFetcher
	}

	// Populate the single `resources` top-level global with a
	// dynamically-built struct value (see typebootstrap's
	// BuildEngineDeclarations for the matching declaration shape).
	// One field per watched resource — typed `[]*GeneratedT` when the
	// schema resolved, untyped `[]any` when it didn't.
	b.addTypedResources(templateContext)

	// Merge extraContext variables into top-level context
	MergeExtraContextInto(templateContext, b.config)

	// renderMode is controller-set, never user-set: re-assert it AFTER the
	// extraContext merge so a user's extraContext.renderMode can't overwrite the
	// top-level global and silently flip a webhook render from fail to warn.
	templateContext["renderMode"] = string(cmp.Or(b.renderMode, RenderModeReconcile))

	if b.config.TemplatingSettings.ExtraContext != nil {
		b.logger.Debug("Added extra context variables to template context",
			"variable_count", len(b.config.TemplatingSettings.ExtraContext))
	}

	return &BuildResult{
		Context:                   templateContext,
		FileRegistry:              fileRegistry,
		StatusPatchCollector:      statusPatchCollector,
		RenderedResourceCollector: renderedResourceCollector,
		EventCollector:            eventCollector,
	}
}

// addTypedResources populates ctx["resources"] with a single
// dynamically-built struct value (matching the shape
// [typebootstrap.BuildEngineDeclarations] declared at engine boot).
// Each outer-struct field is a `*innerStore` pointer to a struct
// whose `List` / `Fetch` / `GetSingle` fields are closures over the
// underlying [stores.Store] for that resource.
//
// The closures preserve the existing chart-facing API
// (`resources.X.List()`, `resources.X.Fetch(ns, name)`,
// `resources.X.GetSingle(ns, name)`) — with two improvements vs the
// previous map-of-StoreWrapper design:
//
//   - Return types are typed (`*GeneratedT` / `[]*GeneratedT`) when
//     the resource's schema resolved, so Scriggo type-checks chart
//     field access at engine boot (`res.Metadata.Namespace`).
//   - The per-resource `indexBy` from the chart's
//     `watchedResources` configuration is honoured by Fetch and
//     GetSingle (closures delegate to the underlying StoreWrapper,
//     which already knows the indexBy keys). Resources indexed by a
//     non-default key (e.g. by label or by namespace) keep working
//     without per-call-site JSONPath plumbing.
//
// Method receiver rather than free function so the closures can
// capture `b.stores` / `b.config.WatchedResources` / `b.logger`
// without threading them as arguments.
func (b *Builder) addTypedResources(ctx map[string]any) {
	// Single source of truth — delegates to BuildResourcesValue so
	// production renderer, testrunner, and any other consumer
	// produce byte-identical struct shapes. No dual-shape: the
	// engine no longer declares an untyped `resources` default
	// (registerScriggoRuntimeVars dropped it) and consumers that
	// previously relied on the map fallback must now go through
	// helpers.BuildAdditionalDeclarations / supply their own
	// typed declaration. watchedNames mirrors what
	// typebootstrap.BuildEngineDeclarations iterated as extras —
	// every WatchedResources entry gets a field on the resources
	// struct, even those without a generated type.
	var watchedNames []string
	if b.config != nil {
		watchedNames = make([]string, 0, len(b.config.WatchedResources))
		for name := range b.config.WatchedResources {
			watchedNames = append(watchedNames, name)
		}
	}
	ctx["resources"] = BuildResourcesValue(
		b.stores,
		b.typedResourceTypes,
		watchedNames,
		func(name string) []string {
			if b.config == nil {
				return nil
			}
			if wr, ok := b.config.WatchedResources[name]; ok {
				return wr.IndexBy
			}
			return nil
		},
		func(name string) bool {
			if b.config == nil {
				return false
			}
			if wr, ok := b.config.WatchedResources[name]; ok {
				return wr.Store == storeKindOnDemand
			}
			return false
		},
		func(name string) string {
			if b.config == nil {
				return ""
			}
			// The effective config's APIVersion IS the resolved version
			// (candidate lists are collapsed at iteration start).
			if wr, ok := b.config.WatchedResources[name]; ok {
				return wr.APIVersion
			}
			return ""
		},
		b.logger,
	)
}

// storeKindOnDemand is the WatchedResource.Store value that selects
// the CachedStore backend; mirrors pkg/k8s/types.StoreTypeCached.String()
// without importing the heavier types package.
const storeKindOnDemand = "on-demand"

// BuildResourcesValue constructs the typed-struct `resources`
// runtime value the engine binds at template-run time. The shape
// matches [typebootstrap.BuildEngineDeclarations] exactly:
//
//	*struct{ <PascalCased-name> *innerStore; … }
//
// with one field per watched resource. The inner store's List /
// Fetch / GetSingle closures return typed pointers when a
// generated type is available, untyped any otherwise — the OUTER
// struct shape stays the same either way, so the runtime value
// keeps binding cleanly against the engine declaration regardless
// of whether typebootstrap produced types for any given resource.
//
// Production is fail-closed on typebootstrap failures (Bootstrap
// aborts iteration startup; helpers.BuildAdditionalDeclarations
// panics on nil Result), so every production engine declares the
// typed struct and this function always produces a typed value.
// There is no map fallback — callers that bypass the typed-engine
// path (a unit test constructing templating.New(...) directly
// without BuildAdditionalDeclarations) must build their own
// map[string]templating.ResourceStore aligned with the engine's
// default declaration in registerScriggoRuntimeVars; do not call
// BuildResourcesValue from that path.
//
// Inputs:
//
//   - resourceStores: per-name [stores.Store] for the resources the
//     local controller has live watchers for. Looked up by name; a
//     watched-resource name with no entry gets a struct field whose
//     closures collapse to empty results. Names in this map that are
//     NOT in watchedNames are silently ignored (e.g. the auto-
//     injected haproxy_pods store, which belongs in
//     controller["haproxy_pods"], not `resources`).
//   - typedTypes: per-name generated [reflect.Type] from
//     typebootstrap. Looked up by name; an unset entry yields an
//     untyped-closure field for that resource. Names not in
//     watchedNames are ignored.
//   - watchedNames: every watched-resource name from the config —
//     SOLE iteration source. Must mirror what
//     typebootstrap.BuildEngineDeclarations iterated when it built
//     the engine-side declaration; any field-list drift trips
//     Scriggo's "must have type assignable to struct {...}" bind-
//     time panic.
//   - indexByFor: returns the per-resource IndexBy slice the
//     watcher used to build the underlying store; forwarded to the
//     StoreWrapper so per-render snapshot indices align with the
//     live store state.
//   - lazyFor: returns whether the per-resource wrapper should use
//     LazySnapshot mode. True when the WatchedResource config has
//     `store: on-demand` (CachedStore-backed) — see StoreWrapper's
//     LazySnapshot field documentation for the semantics. A nil
//     callback or one that always returns false keeps the historical
//     eager-snapshot behaviour for every resource.
func BuildResourcesValue(
	resourceStores map[string]stores.Store,
	typedTypes map[string]reflect.Type,
	watchedNames []string,
	indexByFor func(name string) []string,
	lazyFor func(name string) bool,
	apiVersionFor func(name string) string,
	logger *slog.Logger,
) any {
	if indexByFor == nil {
		indexByFor = func(string) []string { return nil }
	}
	if lazyFor == nil {
		lazyFor = func(string) bool { return false }
	}
	if apiVersionFor == nil {
		apiVersionFor = func(string) string { return "" }
	}
	// watchedNames is the SOLE iteration source — it must mirror what
	// typebootstrap.BuildEngineDeclarations iterated when it built the
	// engine-side struct declaration (which itself reduces to "one
	// field per WatchedResource"; see BuildEngineDeclarations comment).
	// Folding extra names in from resourceStores or typedTypes is a
	// trap: provider.StoreNames() in production includes the
	// auto-injected `haproxy_pods` store (it lives in
	// controller["haproxy_pods"], NOT in `resources`), and including
	// it here adds a phantom Haproxy_pods field that doesn't exist on
	// the engine declaration — Scriggo then panics with
	// "must have type assignable to struct {...}" at the first render.
	// typedTypes is always a subset of watchedNames in production, so
	// it adds nothing either. Dedupe watchedNames defensively for the
	// callers that don't.
	seen := make(map[string]struct{}, len(watchedNames))
	for _, name := range watchedNames {
		seen[name] = struct{}{}
	}
	if len(seen) == 0 {
		return reflect.New(reflect.StructOf(nil)).Interface()
	}
	resourceNames := slices.Sorted(maps.Keys(seen))

	fields := make([]reflect.StructField, 0, len(resourceNames))
	values := make([]reflect.Value, 0, len(resourceNames))
	for _, name := range resourceNames {
		elemType := typedTypes[name]
		store := resourceStores[name]
		var wrapper *StoreWrapper
		if store != nil {
			wrapper = &StoreWrapper{
				Store:        store,
				ResourceType: name,
				Logger:       logger,
				IndexBy:      indexByFor(name),
				LazySnapshot: lazyFor(name),
			}
		}
		innerType := typebootstrap.BuildPerResourceStoreType(elemType)
		innerValue := buildPerResourceStoreValue(innerType, wrapper, elemType, name, apiVersionFor(name), logger)
		fields = append(fields, reflect.StructField{
			Name: typegen.GoFieldName(name),
			Type: reflect.PointerTo(innerType),
			Tag:  reflect.StructTag(`json:"` + name + `"`),
		})
		values = append(values, innerValue)
	}
	resourcesType := reflect.StructOf(fields)
	resources := reflect.New(resourcesType)
	for i, v := range values {
		resources.Elem().Field(i).Set(v)
	}
	return resources.Interface()
}

// buildPerResourceStoreValue returns a `*innerType` whose List /
// Fetch / GetSingle fields are closures. The closures wrap the
// underlying `*StoreWrapper` (already aware of the per-resource
// indexBy) and adapt its `[]any` / `any` returns to the typed
// `[]*T` / `*T` shape Scriggo declared at engine boot.
//
// When `elemType` is nil (schema bootstrap failed for this
// resource) the closures pass through untyped values, so chart code
// reaches the same access surface but loses compile-time field
// validation on the element shape.
//
// When `wrapper` is nil (typebootstrap produced a type the local
// controller doesn't watch) the closures return empty results — the
// outer field exists so chart code that reaches it doesn't fail to
// compile.
func buildPerResourceStoreValue(
	innerType reflect.Type,
	wrapper *StoreWrapper,
	elemType reflect.Type,
	resourceName string,
	apiVersion string,
	logger *slog.Logger,
) reflect.Value {
	ptr := reflect.New(innerType)
	elem := ptr.Elem()

	listField := elem.FieldByName("List")
	fetchField := elem.FieldByName("Fetch")
	getSingleField := elem.FieldByName("GetSingle")

	// Resolved watch-set metadata (see BuildPerResourceStoreType).
	apiVersionField := elem.FieldByName("APIVersion")
	apiVersionField.Set(reflect.MakeFunc(apiVersionField.Type(), func(_ []reflect.Value) []reflect.Value {
		return []reflect.Value{reflect.ValueOf(apiVersion)}
	}))

	listReturnType := listField.Type().Out(0)
	fetchReturnType := fetchField.Type().Out(0)
	getSingleReturnType := getSingleField.Type().Out(0)

	listField.Set(reflect.MakeFunc(listField.Type(), func(_ []reflect.Value) []reflect.Value {
		if wrapper == nil {
			return []reflect.Value{reflect.MakeSlice(listReturnType, 0, 0)}
		}
		items := wrapper.List()
		return []reflect.Value{
			adaptSliceForResource(items, listReturnType, elemType, resourceName, "List", logger),
		}
	}))

	fetchField.Set(reflect.MakeFunc(fetchField.Type(), func(args []reflect.Value) []reflect.Value {
		if wrapper == nil {
			return []reflect.Value{reflect.MakeSlice(fetchReturnType, 0, 0)}
		}
		// Variadic Fetch: args[0] is the []any keys slice.
		keys := args[0].Interface().([]any)
		items := wrapper.Fetch(keys...)
		return []reflect.Value{
			adaptSliceForResource(items, fetchReturnType, elemType, resourceName, "Fetch", logger),
		}
	}))

	getSingleField.Set(reflect.MakeFunc(getSingleField.Type(), func(args []reflect.Value) []reflect.Value {
		if wrapper == nil {
			return []reflect.Value{reflect.Zero(getSingleReturnType)}
		}
		keys := args[0].Interface().([]any)
		item := wrapper.GetSingle(keys...)
		return []reflect.Value{
			adaptSingleForResource(item, getSingleReturnType, elemType, resourceName, logger),
		}
	}))

	return ptr
}

// adaptSliceForResource converts `items []any` to the static return
// type (`[]*T` typed or `[]any` untyped). For typed slices it runs
// each item through `typegen.WrapInto` — that's the same path the
// pre-pivot direct-WrapSlice approach used, just deferred to call
// time so per-render List() picks up the freshest store snapshot.
func adaptSliceForResource(
	items []any,
	returnType reflect.Type,
	elemType reflect.Type,
	resourceName, op string,
	logger *slog.Logger,
) reflect.Value {
	if elemType == nil {
		// Untyped fallback: return type is []any. Direct copy.
		out := reflect.MakeSlice(returnType, len(items), len(items))
		for i, item := range items {
			if item == nil {
				continue
			}
			out.Index(i).Set(reflect.ValueOf(item))
		}
		return out
	}
	// Typed: each item becomes *T via WrapInto. If WrapInto fails
	// for a single item we log and skip that entry rather than
	// abort the whole call — partial data is better than no
	// data for a single bad shape.
	out := reflect.MakeSlice(returnType, 0, len(items))
	for _, item := range items {
		ptr, err := wrapItemToPointer(item, elemType)
		if err != nil {
			logger.Warn("Typed resource: WrapInto failed; skipping item",
				"resource", resourceName, "op", op, "error", err)
			continue
		}
		out = reflect.Append(out, ptr)
	}
	return out
}

// adaptSingleForResource wraps the wrapper's `any` return value into
// the static return type (`*T` typed, or `any` untyped). Returns the
// zero value of the return type for nil input.
func adaptSingleForResource(
	item any,
	returnType reflect.Type,
	elemType reflect.Type,
	resourceName string,
	logger *slog.Logger,
) reflect.Value {
	if item == nil {
		return reflect.Zero(returnType)
	}
	if elemType == nil {
		// Untyped fallback: return type is `any`. Reflect-wrap so
		// the interface assignment goes through cleanly.
		out := reflect.New(returnType).Elem()
		out.Set(reflect.ValueOf(item))
		return out
	}
	ptr, err := wrapItemToPointer(item, elemType)
	if err != nil {
		logger.Warn("Typed resource: WrapInto failed; returning nil",
			"resource", resourceName, "op", "GetSingle", "error", err)
		return reflect.Zero(returnType)
	}
	return ptr
}

// wrapItemToPointer converts a single store item (typically
// map[string]any from the dynamic client) into a typed `*elemType`
// via typegen.WrapInto. Returns a reflect.Value wrapping the
// pointer.
func wrapItemToPointer(item any, elemType reflect.Type) (reflect.Value, error) {
	m, ok := item.(map[string]any)
	if !ok {
		return reflect.Value{}, fmt.Errorf("expected map[string]any, got %T", item)
	}
	v, err := typegen.WrapInto(m, elemType)
	if err != nil {
		return reflect.Value{}, err
	}
	ptr := reflect.New(elemType)
	ptr.Elem().Set(v)
	return ptr, nil
}

// SortSnippetNames sorts template snippet names alphabetically.
// Returns a slice of snippet names in sorted order.
//
// Snippet ordering is controlled by encoding priority in the snippet name
// (e.g., "features-050-ssl" for priority 50). This is required because
// render_glob sorts templates alphabetically.
func SortSnippetNames(snippets map[string]config.TemplateSnippet) []string {
	sorted := make([]string, 0, len(snippets))
	for name := range snippets {
		sorted = append(sorted, name)
	}
	slices.Sort(sorted)
	return sorted
}

// MergeExtraContextInto merges the extraContext variables from the config into the provided template context.
//
// This allows templates to access custom variables directly (e.g., {{ debug.enabled }})
// instead of wrapping them in a "config" object.
//
// The extraContext key is always populated (with an empty map if nil) to prevent
// nil pointer dereferences in templates that use extraContext | dig("key") | fallback(default).
func MergeExtraContextInto(renderCtx map[string]any, cfg *config.Config) {
	if cfg.TemplatingSettings.ExtraContext != nil {
		// Merge at top level
		maps.Copy(renderCtx, cfg.TemplatingSettings.ExtraContext)
		// Also populate the extraContext map for Scriggo templates
		// Scriggo requires compile-time variable declarations, so templates
		// access extraContext values via: extraContext | dig("key") | fallback(default)
		renderCtx["extraContext"] = cfg.TemplatingSettings.ExtraContext
	} else {
		// Always set extraContext, even if empty, to prevent nil pointer dereferences
		// when templates use: extraContext | dig("key") | fallback(default)
		renderCtx["extraContext"] = map[string]any{}
	}
}

// CapabilitiesToMap converts a Capabilities struct to a template-friendly map.
// The map uses snake_case keys matching the Capabilities struct field names
// (e.g., "supports_waf" for SupportsWAF) for consistency with template conventions.
func CapabilitiesToMap(caps *dataplane.Capabilities) map[string]any {
	if caps == nil {
		return map[string]any{}
	}

	return map[string]any{
		// Storage capabilities
		"supports_crt_list":        caps.SupportsCrtList,
		"supports_map_storage":     caps.SupportsMapStorage,
		"supports_general_storage": caps.SupportsGeneralStorage,

		// Configuration capabilities
		"supports_http2": caps.SupportsHTTP2,
		"supports_quic":  caps.SupportsQUIC,

		// Runtime capabilities
		"supports_runtime_maps":    caps.SupportsRuntimeMaps,
		"supports_runtime_servers": caps.SupportsRuntimeServers,

		// Enterprise-only capabilities
		"supports_waf":                     caps.SupportsWAF,
		"supports_waf_global":              caps.SupportsWAFGlobal,
		"supports_waf_profiles":            caps.SupportsWAFProfiles,
		"supports_udp_lb_acls":             caps.SupportsUDPLBACLs,
		"supports_udp_lb_server_switching": caps.SupportsUDPLBServerSwitchingRules,
		"supports_keepalived":              caps.SupportsKeepalived,
		"supports_udp_load_balancing":      caps.SupportsUDPLoadBalancing,
		"supports_bot_management":          caps.SupportsBotManagement,
		"supports_git_integration":         caps.SupportsGitIntegration,
		"supports_dynamic_update":          caps.SupportsDynamicUpdate,
		"supports_aloha":                   caps.SupportsALOHA,
		"supports_advanced_logging":        caps.SupportsAdvancedLogging,
		"supports_ping":                    caps.SupportsPing,

		// Edition detection (convenience flags)
		"is_enterprise": caps.SupportsWAF, // Any enterprise capability indicates Enterprise edition
	}
}
