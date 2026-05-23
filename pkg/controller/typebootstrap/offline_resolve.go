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

package typebootstrap

import (
	"fmt"

	"k8s.io/apimachinery/pkg/runtime/schema"
)

// OfflineGVKResolver maps (apiVersion, resources-plural) pairs to fully
// qualified GVKs without consulting an API server. The production
// wiring uses a RESTMapper built from the cluster's discovery; the
// offline validate path has no cluster, so it threads this through
// instead.
//
// The mapping is intentionally hardcoded rather than rule-based:
// Kubernetes pluralisation has too many irregular cases (HTTPRoute →
// httproutes lowercases the acronym; Endpoints is already plural,
// EndpointSlices is doubly so; IngressClass → ingressclasses runs the
// final s straight on) to derive from a single rule reliably.
//
// The resolver starts empty. Callers populate it from the user's
// `--schema-dir`: every CRD YAML in the directory contributes its
// `spec.names.plural` → GVK mapping, and bare OpenAPI v3 schemas with
// an `x-kubernetes-group-version-kind` extension contribute theirs.
// Resources without a matching entry surface as a not-found error
// pointing the operator at `--schema-dir`. The offline validate caller
// (cmd/controller/validate.go) skips unresolved entries before passing
// the list to Bootstrap, so they never reach the fail-closed
// schema-fetch path — the chart still validates for them through
// dig().
type OfflineGVKResolver struct {
	// entries is the runtime lookup table. Constructors return an
	// empty map; callers populate via Register before passing to
	// Bootstrap. The capacity hint is the typical chart's count of
	// typed-watched resources (Gateway API + haptic CRDs); the map
	// grows on demand for larger schema directories.
	entries map[offlineKey]schema.GroupVersionKind
}

type offlineKey struct {
	apiVersion string
	resources  string
}

// NewOfflineGVKResolver returns an empty resolver. Callers populate
// the (apiVersion, resources-plural) → GVK mapping via Register, most
// commonly by walking a `--schema-dir` and emitting one Register per
// CRD spec.names.plural entry.
//
// An empty resolver is a valid state: configs without typed
// `watchedResources` (or with watched resources whose schemas the
// operator chose not to supply) Bootstrap to a zero Result and the
// chart validates entirely through dig() on the untyped resources
// map.
func NewOfflineGVKResolver() *OfflineGVKResolver {
	return &OfflineGVKResolver{entries: make(map[offlineKey]schema.GroupVersionKind, 8)}
}

// Register adds (or overrides) a single (apiVersion, resources-plural)
// → GVK mapping. Returns the receiver for chaining in test setup.
func (r *OfflineGVKResolver) Register(apiVersion, resources string, gvk schema.GroupVersionKind) *OfflineGVKResolver {
	r.entries[offlineKey{apiVersion: apiVersion, resources: resources}] = gvk
	return r
}

// Resolve returns the fully qualified GVK for the supplied
// (apiVersion, resources-plural) pair. Missing entries return an
// error pointing the operator at `--schema-dir`; the bootstrap
// caller logs and degrades that single resource.
func (r *OfflineGVKResolver) Resolve(apiVersion, resources string) (schema.GroupVersionKind, error) {
	gvk, ok := r.entries[offlineKey{apiVersion: apiVersion, resources: resources}]
	if !ok {
		return schema.GroupVersionKind{}, fmt.Errorf(
			"offline GVK resolver has no entry for apiVersion=%q resources=%q "+
				"(supply the CRD or an OpenAPI v3 schema with x-kubernetes-group-version-kind "+
				"in the directory passed to --schema-dir / HAPTIC_SCHEMA_DIR)",
			apiVersion, resources)
	}
	return gvk, nil
}
