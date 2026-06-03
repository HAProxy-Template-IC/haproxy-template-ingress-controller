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

package dryrunvalidator

import (
	"fmt"
	"strings"

	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane"
	"gitlab.com/haproxy-haptic/haptic/pkg/stores"
)

const (
	operationCreate = "CREATE"
	operationUpdate = "UPDATE"
	operationDelete = "DELETE"

	phaseRender = "render"
)

// createOverlay builds the StoreOverlay that represents the admission
// request's hypothetical state. DELETE yields an overlay with only the
// deletion recorded, CREATE/UPDATE wrap the incoming object.
//
// Parameters:
//   - namespace/name: Resource coordinates (name may be empty for CREATE
//     with generateName)
//   - object: The Kubernetes resource object (must be runtime.Object for
//     CREATE/UPDATE)
//   - operation: Admission operation (CREATE, UPDATE, DELETE)
//   - requestID: Request ID for logging (can be empty for direct validation)
func (c *Component) createOverlay(namespace, name string, object any, operation, requestID string) *stores.StoreOverlay {
	// Handle DELETE first - it doesn't need an object
	if operation == operationDelete {
		return stores.NewStoreOverlayForDelete(namespace, name)
	}

	// Convert the object to runtime.Object if possible
	obj, ok := object.(runtime.Object)
	if !ok {
		// If not a runtime.Object, return empty overlay
		// This shouldn't happen for K8s resources but handles edge cases
		c.logger.Warn("object is not runtime.Object",
			"request_id", requestID,
			"type", fmt.Sprintf("%T", object))
		return stores.NewStoreOverlay()
	}

	switch operation {
	case operationCreate:
		return stores.NewStoreOverlayForCreate(obj)
	case operationUpdate:
		return stores.NewStoreOverlayForUpdate(obj)
	default:
		c.logger.Warn("unknown operation type",
			"request_id", requestID,
			"operation", operation)
		return stores.NewStoreOverlay()
	}
}

// simplifyError simplifies an error message based on the validation phase.
func (c *Component) simplifyError(phase string, err error) string {
	if err == nil {
		return ""
	}
	switch phase {
	case phaseRender:
		return dataplane.SimplifyRenderingError(err)
	case "syntax", "schema", "semantic":
		return dataplane.SimplifyValidationError(err)
	default:
		return err.Error()
	}
}

// mapGVKToResourceType resolves an admission GVK string to the watched
// resource's plural name — the key the overlay store is registered under.
//
// Resolution goes through the cluster's RESTMapper, so the plural comes from
// discovery data (and, for CRDs, each CRD's own spec.names.plural). Any watched
// resource resolves correctly, including CRDs with irregular or fully custom
// plurals, with no hardcoded pluralization table (RULE #1: the controller stays
// resource-agnostic). An unrecognised kind returns an error rather than a
// guessed plural.
//
// The GVK string is "group/version.Kind" or "version.Kind" — the identifier the
// webhook registers its validators under.
func (c *Component) mapGVKToResourceType(gvk string) (string, error) {
	// Split off the Kind (the segment after the final dot); everything before
	// it is the apiVersion ("group/version" or "version").
	parts := strings.Split(gvk, ".")
	if len(parts) < 2 {
		return "", fmt.Errorf("invalid GVK format: %s", gvk)
	}
	kind := parts[len(parts)-1]
	apiVersion := strings.Join(parts[:len(parts)-1], ".")

	gv, err := schema.ParseGroupVersion(apiVersion)
	if err != nil {
		return "", fmt.Errorf("invalid GVK %q: %w", gvk, err)
	}

	gk := schema.GroupKind{Group: gv.Group, Kind: kind}
	mapping, err := c.restMapper.RESTMapping(gk, gv.Version)
	if err != nil && meta.IsNoMatchError(err) {
		// A deferred discovery mapper caches discovery for its lifetime, so a
		// CRD registered after that cache was first populated would resolve to
		// NoMatch permanently. Refresh discovery once and retry so a
		// late-registered watched resource validates without a controller
		// iteration restart.
		if resettable, ok := c.restMapper.(meta.ResettableRESTMapper); ok {
			resettable.Reset()
			mapping, err = c.restMapper.RESTMapping(gk, gv.Version)
		}
	}
	if err != nil {
		return "", fmt.Errorf("resolving resource type for GVK %q: %w", gvk, err)
	}
	return mapping.Resource.Resource, nil
}
