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

package schemafetcher

import (
	"context"
	"errors"
	"fmt"

	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/kube-openapi/pkg/validation/spec"
)

// Fetcher retrieves OpenAPI v3 schemas for Kubernetes resources by
// [schema.GroupVersionKind]. Implementations are responsible for
// translating from the cluster's representation (aggregated OpenAPI
// for built-ins, CustomResourceDefinition objects for CRDs) into the
// kube-openapi [spec.Schema] shape that pkg/k8s/typegen
// consumes.
//
// Implementations MUST be safe for concurrent use — controller
// bootstrap fans out across watched resources in parallel.
type Fetcher interface {
	// Fetch returns the schema for the supplied GVK plus the
	// components map needed to resolve any $ref entries the schema
	// contains. K8s aggregated OpenAPI v3 wraps shared types
	// (ObjectMeta, Time, …) in `allOf: [$ref: ...]` patterns, so
	// the consuming converter MUST be able to walk into the same
	// components map the cluster published the schema with.
	//
	// CRD-backed implementations return nil components: CRDs inline
	// every shared shape and never use $refs across schemas. The
	// converter contract handles a nil components map by treating
	// any $ref encountered as unresolvable (which it would be).
	//
	// A nil schema without an error MUST NOT be returned: either
	// the schema is found and returned, or the error path carries
	// the failure via [ErrSchemaNotAvailable].
	Fetch(ctx context.Context, gvk schema.GroupVersionKind) (*spec.Schema, map[string]spec.Schema, error)
}

// ErrSchemaNotAvailable is returned when the fetcher couldn't produce
// a schema for the requested GVK. The controller bootstrap is
// fail-closed: any per-resource ErrSchemaNotAvailable surfaces as
// a hard iteration-startup error so the operator gets a clear
// signal to investigate the underlying RBAC / CRD installation /
// apiserver health issue rather than running with silently
// degraded typed access.
//
// Wraps the underlying cause so errors.Is / errors.As work for the
// usual suspects (context.DeadlineExceeded, network errors,
// 404 / NotFound from the API server).
type ErrSchemaNotAvailable struct {
	GVK   schema.GroupVersionKind
	Cause error
}

func (e *ErrSchemaNotAvailable) Error() string {
	if e.Cause == nil {
		return fmt.Sprintf("schema not available for %s", e.GVK)
	}
	return fmt.Sprintf("schema not available for %s: %v", e.GVK, e.Cause)
}

func (e *ErrSchemaNotAvailable) Unwrap() error {
	return e.Cause
}

// errNotFound is the canonical "didn't find anything matching" inner
// cause used by the cluster fetcher when neither the CRD path nor the
// OpenAPI v3 path returns a match. Wrapped by ErrSchemaNotAvailable.
// Exported via [IsNotFound] so callers don't have to import the
// sentinel directly.
var errNotFound = errors.New("schema not found")

// IsNotFound reports whether err (or any error it wraps) indicates a
// "schema doesn't exist for this GVK" outcome — distinct from
// transient failures (network errors, API-server timeouts, …) that
// callers might want to retry. Bootstrap is currently fail-closed
// on all ErrSchemaNotAvailable outcomes, but this distinction is
// here for callers that want a different policy (e.g. a future
// reconciler that retries transient errors but surfaces NotFound
// as a config-level mistake).
func IsNotFound(err error) bool {
	return errors.Is(err, errNotFound)
}
