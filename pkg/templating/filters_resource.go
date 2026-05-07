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

	"gitlab.com/haproxy-haptic/scriggo/native"
)

// getRenderedResourceCollector retrieves the RenderedResourceCollector from
// the template render context. Mirrors getStatusPatchCollector exactly.
func getRenderedResourceCollector(env native.Env) *RenderedResourceCollector {
	ctx := env.Context()
	if ctx == nil {
		return nil
	}
	renderCtx, ok := ctx.Value(RenderContextContextKey).(map[string]any)
	if !ok {
		return nil
	}
	collector, ok := renderCtx["renderedResourceCollector"].(*RenderedResourceCollector)
	if !ok {
		return nil
	}
	return collector
}

// scriggoRenderResource is the template-callable function that registers a
// desired Kubernetes resource for the controller to apply.
//
// Resource-agnostic by design: the controller does not hardcode any
// kind / apiVersion. Templates emit whatever they need; the generic
// applier reconciles via SSA and prunes orphaned resources owned by the
// controller's field manager that no longer appear in subsequent renders.
//
// Usage in Scriggo templates:
//
//	{% renderResource("v1", "Service", "default", "my-svc",
//	    map[string]any{
//	        "spec": map[string]any{
//	            "type":     "LoadBalancer",
//	            "selector": map[string]any{"app": "my-app"},
//	            "ports":    []any{
//	                map[string]any{"port": 80, "protocol": "TCP", "targetPort": 8080},
//	            },
//	        },
//	    }) %}
//
// apiVersion / kind / metadata.name / metadata.namespace are injected
// automatically — the template author only supplies spec / data / etc.
//
// Calling with the same (namespace, name, apiVersion, kind) tuple multiple
// times during one render is last-write-wins on the supplied object. The
// applier compares a SHA-256 checksum of the final payload against the
// last-applied checksum and skips the API call when they match — so even
// idempotent re-renders of identical resources don't hammer kube-api.
func scriggoRenderResource(env native.Env, apiVersion, kind, namespace, name string, object map[string]any) string {
	collector := getRenderedResourceCollector(env)
	if collector == nil {
		env.Stop(errors.New("renderResource: renderedResourceCollector not available in render context"))
		return ""
	}
	if err := collector.Register(apiVersion, kind, namespace, name, object); err != nil {
		env.Stop(fmt.Errorf("renderResource: %w", err))
		return ""
	}
	return "" // side-effect only, no output (mirrors statusPatch)
}
