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
// final s straight on) to derive from a single rule reliably. Adding
// a new offline-supported resource means one line here.
//
// Resources without an entry surface as a not-found error. The
// offline validate caller (cmd/controller/validate.go) skips
// unresolved entries before passing the list to Bootstrap, so
// they never reach the fail-closed schema-fetch path — the
// chart still validates for them through dig().
type OfflineGVKResolver struct {
	// entries is the runtime lookup table. Built once in
	// NewOfflineGVKResolver from the builtin set; callers can extend
	// via Register before passing to Bootstrap.
	entries map[offlineKey]schema.GroupVersionKind
}

type offlineKey struct {
	apiVersion string
	resources  string
}

// kindGateway and groupGatewayAPI are repeated across the bundled GVK
// entries and the test tables that pin them. Lifted to constants so
// goconst stops counting occurrences without adding noise.
const (
	kindGateway     = "Gateway"
	groupGatewayAPI = "gateway.networking.k8s.io"
)

// NewOfflineGVKResolver returns a resolver pre-loaded with the GVKs
// the controller's bundled chart libraries refer to. Add new entries
// via Register when contributing a new builtin schema in
// pkg/k8s/schemafetcher/builtin.
//
// Bundled entries are deliberately conservative — only resources whose
// typed access is actually exercised by the bundled chart libraries
// today. Phantom entries (a GVK with no consumer) are pure noise.
func NewOfflineGVKResolver() *OfflineGVKResolver {
	r := &OfflineGVKResolver{entries: make(map[offlineKey]schema.GroupVersionKind, 4)}
	// Gateway API v1. Add new pairs alongside the matching builtin
	// schema file in pkg/k8s/schemafetcher/builtin.
	r.Register(groupGatewayAPI+"/v1", "gateways",
		schema.GroupVersionKind{Group: groupGatewayAPI, Version: "v1", Kind: kindGateway})
	return r
}

// Register adds (or overrides) a single (apiVersion, resources-plural)
// → GVK mapping. Returns the receiver for chaining in test setup.
func (r *OfflineGVKResolver) Register(apiVersion, resources string, gvk schema.GroupVersionKind) *OfflineGVKResolver {
	r.entries[offlineKey{apiVersion: apiVersion, resources: resources}] = gvk
	return r
}

// Resolve returns the fully qualified GVK for the supplied
// (apiVersion, resources-plural) pair. Missing entries return an
// error with a hint about adding a builtin schema; the bootstrap
// caller logs and degrades that single resource.
func (r *OfflineGVKResolver) Resolve(apiVersion, resources string) (schema.GroupVersionKind, error) {
	gvk, ok := r.entries[offlineKey{apiVersion: apiVersion, resources: resources}]
	if !ok {
		return schema.GroupVersionKind{}, fmt.Errorf(
			"offline GVK resolver has no entry for apiVersion=%q resources=%q "+
				"(add a builtin schema in pkg/k8s/schemafetcher/builtin and register the GVK in NewOfflineGVKResolver)",
			apiVersion, resources)
	}
	return gvk, nil
}
