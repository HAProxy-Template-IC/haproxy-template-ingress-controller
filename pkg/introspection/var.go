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

// Package introspection provides a generic debug variable registry and HTTP server
// for exposing application internal state over HTTP.
//
// This package is inspired by the standard library's expvar package but extends it with:
//   - Instance-based registry (not global) for proper lifecycle management
//   - JSONPath field selection for querying specific fields
//   - Custom HTTP routing with path-based variable access
//
// The core abstraction is the Var interface, which represents any debug variable
// that can be queried. Implementations provide their current value via the Get() method.
//
// Example usage:
//
//	// Create an instance-scoped registry
//	registry := introspection.NewRegistry()
//
//	// Publish variables
//	registry.Publish("config", &ConfigVar{provider})
//	registry.Publish("uptime", introspection.Func(func() (any, error) {
//	    return time.Since(startTime), nil
//	}))
//
//	// Start HTTP server
//	server := introspection.NewServer(":6060", registry)
//	server.Setup()
//	go server.Serve(ctx)
//
//	// Query variables:
//	// GET /debug/vars - list all variables
//	// GET /debug/vars/config - get config variable
//	// GET /debug/vars/config?field={.version} - get specific field
package introspection

// Var represents a debug variable that can be queried for its current value.
//
// Implementations should return their current state when Get() is called.
// The returned value must be JSON-serializable.
//
// Example implementation:
//
//	type ConfigVar struct {
//	    mu     sync.RWMutex
//	    config *Config
//	}
//
//	func (v *ConfigVar) Get() (any, error) {
//	    v.mu.RLock()
//	    defer v.mu.RUnlock()
//	    return v.config, nil
//	}
type Var interface {
	// Get returns the current value of this variable.
	//
	// The returned value should be JSON-serializable.
	// If an error occurs while retrieving the value, it should be returned.
	//
	// Implementations must be thread-safe, as Get() may be called concurrently
	// from multiple HTTP requests.
	Get() (any, error)
}
