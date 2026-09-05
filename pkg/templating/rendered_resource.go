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

package templating

import (
	"errors"
	"fmt"
	"slices"
	"sync"
)

// RenderedResource represents a full Kubernetes resource that templates
// declare during rendering as "I want this to exist on the cluster, owned by
// the controller". The applier consumes these and reconciles them via SSA
// (Server-Side Apply); resources owned by the controller but no longer
// declared in a render are pruned.
//
// This mirrors the StatusPatch / StatusPatchCollector pattern in
// status_patch.go but for whole-resource lifecycle instead of status-only
// updates. The same resource-agnostic principle applies: the controller
// never names a specific resource kind in code — it just applies whatever the
// template emits. Templates decide *what* to emit; the controller is the
// generic vehicle.
//
// Object holds the desired API resource as a map[string]any matching the
// shape of the corresponding Kubernetes resource (apiVersion / kind /
// metadata / spec / data / …). The applier marshals the map to JSON and
// sends it to the API server with PatchType=Apply and a fixed field
// manager.
type RenderedResource struct {
	// APIVersion of the target resource (e.g., "v1", "gateway.networking.k8s.io/v1").
	APIVersion string

	// Kind of the target resource (e.g., "Service", "Secret", "ConfigMap").
	Kind string

	// Namespace of the target resource. Empty for cluster-scoped resources;
	// the applier passes Namespace("") to the dynamic client which handles
	// cluster-scoped types automatically.
	Namespace string

	// Name of the target resource.
	Name string

	// Object is the desired resource shape that will be sent verbatim to the
	// API server via SSA. It must include `apiVersion`, `kind`, and
	// `metadata.name` — the collector validates and injects those at
	// Register time, so callers can omit them and pass only the deltas.
	Object map[string]any

	// CreateOnlyFields are dotted field paths the applier sends when it creates
	// this object and omits from every apply after that, leaving them to
	// whoever runs it.
	CreateOnlyFields []string
}

// renderedResourceKey uniquely identifies a target resource for collector merging.
// Mirrors statusPatchKey: namespace/name/apiVersion/kind.
func renderedResourceKey(namespace, name, apiVersion, kind string) string {
	return namespace + "/" + name + "/" + apiVersion + "/" + kind
}

// RenderedResourceCollector collects desired Kubernetes resources for the
// applier to reconcile. Filled by the renderer after rendering each entry
// in `spec.k8sResources`: every YAML document in the rendered output
// becomes one Register call. Thread-safe so the renderer can populate it
// from parallel template goroutines if needed (same lifecycle / same shape
// as StatusPatchCollector).
//
// Created per render cycle. Multiple Register calls for the same key are
// last-write-wins on Object (the prior Object is replaced wholesale; the
// applier checksums the final object and skips unchanged SSA patches).
type RenderedResourceCollector struct {
	mu          sync.Mutex
	resources   map[string]*RenderedResource // keyed by renderedResourceKey
	projections map[string]statusPatchProjectionValue
	frozen      bool
	snapshot    *RenderedResourceSnapshot
}

// NewRenderedResourceCollector creates an empty thread-safe collector.
func NewRenderedResourceCollector() *RenderedResourceCollector {
	return &RenderedResourceCollector{
		resources:   make(map[string]*RenderedResource),
		projections: make(map[string]statusPatchProjectionValue),
	}
}

// Register declares that a resource should exist on the cluster after this
// render lands. apiVersion / kind / namespace / name identify the target;
// `object` is the desired shape (without apiVersion / kind / metadata.name —
// those are injected so the SSA payload is well-formed regardless of which
// keys the template author included).
//
// Repeated calls with the same (namespace, name, apiVersion, kind) replace
// the prior Object — last write wins. This keeps the semantics simple for
// templates that conditionally re-emit resources during a render. The
// applier's SHA-256 checksum cache prevents the resulting last-version
// from triggering a redundant API call when nothing actually changed
// across renders, so this last-write-wins doesn't risk hammering the API.
func (c *RenderedResourceCollector) Register(apiVersion, kind, namespace, name string, object map[string]any) error {
	return c.RegisterWithCreateOnlyFields(apiVersion, kind, namespace, name, object, nil)
}

// RegisterWithCreateOnlyFields is Register for a resource whose configuration
// names fields the applier must send only when it creates the object.
func (c *RenderedResourceCollector) RegisterWithCreateOnlyFields(
	apiVersion, kind, namespace, name string,
	object map[string]any,
	createOnlyFields []string,
) error {
	if name == "" || apiVersion == "" || kind == "" {
		return errors.New("k8sResources: name, apiVersion, and kind are required")
	}
	if object == nil {
		return errors.New("k8sResources: object is required")
	}

	// Always normalize identifying fields onto the object so the resulting
	// payload is a complete, valid SSA request regardless of what the
	// template author included.
	obj := make(map[string]any, len(object)+2)
	for k, v := range object {
		obj[k] = v
	}
	obj["apiVersion"] = apiVersion
	obj["kind"] = kind

	metadata, _ := obj["metadata"].(map[string]any)
	if metadata == nil {
		metadata = map[string]any{}
	} else {
		// Copy so we don't mutate the template's map (Scriggo treats maps
		// as references; downstream renders could observe injection).
		copied := make(map[string]any, len(metadata)+2)
		for k, v := range metadata {
			copied[k] = v
		}
		metadata = copied
	}
	metadata["name"] = name
	if namespace != "" {
		metadata["namespace"] = namespace
	} else {
		// Cluster-scoped: omit namespace rather than serializing "".
		delete(metadata, "namespace")
	}
	obj["metadata"] = metadata

	c.mu.Lock()
	defer c.mu.Unlock()
	if c.frozen {
		return errors.New("k8sResources: collector is sealed")
	}
	projection, err := newStatusPatchProjectionValue(obj, make(map[statusPatchProjectionVisit]struct{}), 0)
	if err != nil {
		return fmt.Errorf("k8sResources: object is not immutable: %w", err)
	}
	detached, err := projection.materializeObject()
	if err != nil {
		return fmt.Errorf("k8sResources: detaching object: %w", err)
	}
	key := renderedResourceKey(namespace, name, apiVersion, kind)

	c.resources[key] = &RenderedResource{
		APIVersion: apiVersion,
		Kind:       kind,
		Namespace:  namespace,
		Name:       name,
		Object:     detached,

		CreateOnlyFields: slices.Clone(createOnlyFields),
	}
	c.projections[key] = projection
	return nil
}

// Resources returns a detached, deterministically ordered view. Further
// Register calls do not affect it.
func (c *RenderedResourceCollector) Resources() []RenderedResource {
	c.mu.Lock()
	defer c.mu.Unlock()

	keys := make([]string, 0, len(c.resources))
	for key := range c.resources {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	result := make([]RenderedResource, 0, len(keys))
	for _, key := range keys {
		resource := c.resources[key]
		detached := *resource
		if projection, exists := c.projections[key]; exists {
			object, err := projection.materializeObject()
			if err == nil {
				detached.Object = object
			}
		}
		result = append(result, detached)
	}
	return result
}

// Validate runs lightweight sanity checks every collected resource must
// pass before the applier is allowed to send it. Returned errors include
// the offending key so template authors can locate the bad call site.
//
// The check set is deliberately minimal — the API server will reject
// malformed payloads itself; we just want to catch obvious template
// mistakes (missing kind, missing metadata) before they reach the wire.
func (c *RenderedResourceCollector) Validate() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	for key, r := range c.resources {
		if r.APIVersion == "" || r.Kind == "" || r.Name == "" {
			return fmt.Errorf("k8sResources %s: apiVersion, kind, and name must be non-empty", key)
		}
		if r.Object == nil {
			return fmt.Errorf("k8sResources %s: object is nil", key)
		}
		projection, exists := c.projections[key]
		if !exists {
			return fmt.Errorf("k8sResources %s: object has no immutable projection", key)
		}
		object, err := projection.materializeObject()
		if err != nil || object == nil {
			return fmt.Errorf("k8sResources %s: object has invalid immutable projection", key)
		}
	}
	return nil
}
