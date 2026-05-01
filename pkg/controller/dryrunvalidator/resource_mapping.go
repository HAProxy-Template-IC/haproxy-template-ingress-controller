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

	"k8s.io/apimachinery/pkg/runtime"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/events"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane"
	"gitlab.com/haproxy-haptic/haptic/pkg/stores"
)

const (
	operationCreate = "CREATE"
	operationUpdate = "UPDATE"
	operationDelete = "DELETE"

	phaseRender = "render"

	resourceTypeIngresses = "ingresses"
	resourceTypeEndpoints = "endpoints"
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

// mapGVKToResourceType maps a GVK string to a resource type name.
//
// Examples:
//   - "networking.k8s.io/v1.Ingress" -> "ingresses"
//   - "v1.Service" -> "services"
//   - "v1.ConfigMap" -> "configmaps"
//
// Returns the plural, lowercase resource type name used as store keys.
func (c *Component) mapGVKToResourceType(gvk string) (string, error) {
	// Extract Kind from GVK
	// Format: "group/version.Kind" or "version.Kind"
	parts := strings.Split(gvk, ".")
	if len(parts) < 2 {
		return "", fmt.Errorf("invalid GVK format: %s", gvk)
	}

	kind := parts[len(parts)-1]

	// Convert Kind to plural resource type
	// Handle common irregular plurals and special cases
	kindLower := strings.ToLower(kind)

	// Map of irregular plurals and special cases for Kubernetes resources.
	// The default rule (append 's') doesn't work for:
	// - Words ending in -ss need -es suffix (ingress -> ingresses, not ingresss)
	// - Words ending in consonant + y need -ies suffix (policy -> policies)
	// - Words that are already plural (endpoints)
	irregularPlurals := map[string]string{
		// -ss ending needs -es suffix
		"ingress":       resourceTypeIngresses,
		"ingressclass":  "ingressclasses",
		"storageclass":  "storageclasses",
		"priorityclass": "priorityclasses",
		"runtimeclass":  "runtimeclasses",
		// -y ending (after consonant) needs -ies suffix
		"networkpolicy":     "networkpolicies",
		"podsecuritypolicy": "podsecuritypolicies",
		// Already plural (no change needed)
		resourceTypeEndpoints: resourceTypeEndpoints,
	}

	if plural, ok := irregularPlurals[kindLower]; ok {
		return plural, nil
	}

	// Default: add 's' for regular plurals
	return kindLower + "s", nil
}

// publishResponse publishes a WebhookValidationResponse event.
func (c *Component) publishResponse(requestID string, allowed bool, reason string) {
	response := events.NewWebhookValidationResponse(requestID, ValidatorID, allowed, reason)
	c.eventBus.Publish(response)

	if allowed {
		c.logger.Debug("Published allowed response", "request_id", requestID)
	} else {
		c.logger.Info("Published denied response",
			"request_id", requestID,
			"reason", reason)
	}
}
