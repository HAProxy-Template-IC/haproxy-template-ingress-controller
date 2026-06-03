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
	"maps"
	"sync"
)

const (
	statusPhaseRendered     = "rendered"
	statusPhaseDeployed     = "deployed"
	statusPhaseRenderFailed = "renderFailed"
	statusPhaseDeployFailed = "deployFailed"
)

// StatusPatch represents a status update to apply to a Kubernetes resource.
// Templates register patches via the statusPatch() function during rendering.
// Each patch targets a specific resource and contains outcome-keyed variants
// for different pipeline lifecycle phases.
type StatusPatch struct {
	// Namespace of the target Kubernetes resource.
	Namespace string

	// Name of the target Kubernetes resource.
	Name string

	// APIVersion of the target resource (e.g., "networking.k8s.io/v1").
	APIVersion string

	// Kind of the target resource (e.g., "Service", "ConfigMap", or any watched CRD's Kind).
	Kind string

	// Variants maps pipeline phase names to desired status payloads.
	// Keys are phase names: "rendered", "deployed", "renderFailed", "deployFailed".
	// Values are the desired .status content for that phase.
	Variants map[string]map[string]any
}

// statusPatchKey uniquely identifies a target resource for patch merging.
func statusPatchKey(namespace, name, apiVersion, kind string) string {
	return namespace + "/" + name + "/" + apiVersion + "/" + kind
}

// StatusPatchCollector collects status patches registered by templates during rendering.
// It is thread-safe for concurrent writes from parallel template goroutines.
// Created per render cycle (same lifecycle as FileRegistry).
type StatusPatchCollector struct {
	mu      sync.Mutex
	patches map[string]*StatusPatch // keyed by statusPatchKey
}

// NewStatusPatchCollector creates a new thread-safe collector.
func NewStatusPatchCollector() *StatusPatchCollector {
	return &StatusPatchCollector{
		patches: make(map[string]*StatusPatch),
	}
}

// Register registers a status patch for a Kubernetes resource.
// If a patch for the same resource already exists, the variant maps are merged
// (later calls override earlier ones for the same variant key).
//
// The variants parameter maps phase names to status payloads:
//   - "rendered": applied after successful render
//   - "deployed": applied after successful deployment
//   - "renderFailed": applied when later render phases fail
//   - "deployFailed": applied when deployment fails
func (c *StatusPatchCollector) Register(namespace, name, apiVersion, kind string, variants map[string]map[string]any) error {
	// Namespace is intentionally optional: cluster-scoped resources
	// (GatewayClass, ClusterRole, etc.) have no namespace. The applier
	// passes Namespace("") to the dynamic client, which the client-go
	// dynamic interface treats as cluster-scoped automatically.
	if name == "" || apiVersion == "" || kind == "" {
		return errors.New("statusPatch: name, apiVersion, and kind are required")
	}

	if len(variants) == 0 {
		return errors.New("statusPatch: at least one variant is required")
	}

	// Validate phase keys
	for phase := range variants {
		switch phase {
		case statusPhaseRendered, statusPhaseDeployed, statusPhaseRenderFailed, statusPhaseDeployFailed:
			// valid
		default:
			return fmt.Errorf("statusPatch: invalid phase %q, must be one of: rendered, deployed, renderFailed, deployFailed", phase)
		}
	}

	key := statusPatchKey(namespace, name, apiVersion, kind)

	c.mu.Lock()
	defer c.mu.Unlock()

	existing, exists := c.patches[key]
	if !exists {
		c.patches[key] = &StatusPatch{
			Namespace:  namespace,
			Name:       name,
			APIVersion: apiVersion,
			Kind:       kind,
			Variants:   variants,
		}
		return nil
	}

	// Merge variants into existing patch
	maps.Copy(existing.Variants, variants)

	return nil
}

// Patches returns all collected status patches as a slice.
// The returned slice is a snapshot; further Register calls do not affect it.
func (c *StatusPatchCollector) Patches() []StatusPatch {
	c.mu.Lock()
	defer c.mu.Unlock()

	result := make([]StatusPatch, 0, len(c.patches))
	for _, patch := range c.patches {
		result = append(result, *patch)
	}
	return result
}
