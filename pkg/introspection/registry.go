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

package introspection

import (
	"fmt"
	"sort"
	"sync"
)

// Registry manages a collection of debug variables.
//
// Unlike expvar which uses a global registry, this implementation is instance-based,
// allowing for proper lifecycle management. Each Registry instance is independent
// and can be garbage collected when no longer needed.
//
// This is especially important for applications that reinitialize components
// (like a Kubernetes controller that reloads configuration), as it prevents
// stale references to old component instances.
//
// Registry is thread-safe and can be accessed from multiple goroutines.
type Registry struct {
	mu   sync.RWMutex
	vars map[string]Var
}

// NewRegistry creates a new empty registry.
//
// Each registry instance is independent and manages its own set of variables.
//
// Example:
//
//	registry := introspection.NewRegistry()
func NewRegistry() *Registry {
	return &Registry{
		vars: make(map[string]Var),
	}
}

// Publish registers a variable at the specified path.
//
// The path is used to access the variable via HTTP (e.g., /debug/vars/{path}).
// If a variable already exists at the given path, it is replaced.
//
// Path format:
//   - Use simple names: "config", "uptime", "stats"
//   - Or hierarchical paths: "resources/ingresses", "cache/hits"
//
// Example:
//
//	registry.Publish("config", &ConfigVar{provider})
//	registry.Publish("resources/ingresses", &IngressVar{store})
func (r *Registry) Publish(path string, v Var) {
	if path == "" {
		panic("introspection: empty path not allowed")
	}
	if v == nil {
		panic("introspection: nil Var not allowed")
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	r.vars[path] = v
}

// Get retrieves the value of the variable at the specified path.
//
// Returns an error if the path does not exist or if the variable's Get() method fails.
//
// Example:
//
//	value, err := registry.Get("config")
//	if err != nil {
//	    slog.Error("Failed to get config", "error", err)
//	}
func (r *Registry) Get(path string) (interface{}, error) {
	r.mu.RLock()
	v, ok := r.vars[path]
	r.mu.RUnlock()

	if !ok {
		return nil, fmt.Errorf("variable %q not found", path)
	}

	return v.Get()
}

// GetWithField retrieves a specific field from the variable at the specified path
// using JSONPath syntax.
//
// The field parameter should use kubectl-style JSONPath syntax (e.g., "{.version}").
// If field is empty, the full variable value is returned.
//
// This method is used internally by the HTTP handlers to support field selection
// via query parameters.
//
// Example:
//
//	// Get full config
//	value, err := registry.GetWithField("config", "")
//
//	// Get just the version field
//	version, err := registry.GetWithField("config", "{.version}")
func (r *Registry) GetWithField(path, field string) (interface{}, error) {
	value, err := r.Get(path)
	if err != nil {
		return nil, err
	}

	// If no field specified, return full value
	if field == "" {
		return value, nil
	}

	// Extract field using JSONPath
	return ExtractField(value, field)
}

// All returns all variables as a map of path → value.
//
// If any variable's Get() method fails, the error is returned and the map
// may be incomplete.
//
// This is used by the /debug/vars endpoint to list all variables.
//
// Example:
//
//	all, err := registry.All()
//	for path, value := range all {
//	    fmt.Printf("%s = %v\n", path, value)
//	}
func (r *Registry) All() (map[string]interface{}, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	result := make(map[string]interface{}, len(r.vars))

	for path, v := range r.vars {
		value, err := v.Get()
		if err != nil {
			return nil, fmt.Errorf("failed to get variable %q: %w", path, err)
		}
		result[path] = value
	}

	return result, nil
}

// Paths returns a sorted list of all registered variable paths.
//
// This is used by the /debug/vars endpoint to provide an index of available variables.
//
// Example:
//
//	paths := registry.Paths()
//	// Returns: ["config", "resources/ingresses", "uptime"]
func (r *Registry) Paths() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	paths := make([]string, 0, len(r.vars))
	for path := range r.vars {
		paths = append(paths, path)
	}

	sort.Strings(paths)
	return paths
}

// Len returns the number of registered variables.
//
// Example:
//
//	count := registry.Len()  // Returns number of published variables
func (r *Registry) Len() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.vars)
}

// Clear removes all published variables from the registry.
//
// This is used between controller iterations to prevent stale references
// to components from previous iterations. The registry can then be reused
// with new variables without restarting the HTTP server.
//
// Example:
//
//	// Between iterations
//	registry.Clear()
//	registry.Publish("config", newConfigVar)
func (r *Registry) Clear() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.vars = make(map[string]Var)
}
