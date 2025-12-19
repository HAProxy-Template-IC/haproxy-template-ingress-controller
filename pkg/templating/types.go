// Package templating provides template rendering capabilities using the Scriggo
// template engine.
//
// This package offers a unified interface for compiling and rendering templates
// using Go template syntax via the Scriggo engine.
//
// Templates are pre-compiled at initialization for optimal runtime performance
// and early detection of syntax errors.
package templating

import (
	"fmt"
	"strings"
)

// Engine name constants used for parsing and display.
const (
	// EngineNameScriggo is the string identifier for the Scriggo engine.
	EngineNameScriggo = "scriggo"

	// EngineNameUnknown is used when an engine type is not recognized.
	EngineNameUnknown = "unknown"
)

// EngineType represents the template engine to use for rendering.
type EngineType int

const (
	// EngineTypeScriggo uses the Scriggo template engine (Go template syntax).
	// This is the default and only supported engine for HAProxy configuration templating.
	EngineTypeScriggo EngineType = iota
)

// String returns the string representation of the engine type.
func (e EngineType) String() string {
	switch e {
	case EngineTypeScriggo:
		return EngineNameScriggo
	default:
		return EngineNameUnknown
	}
}

// ParseEngineType converts a string to an EngineType.
// Empty string defaults to EngineTypeScriggo.
func ParseEngineType(s string) (EngineType, error) {
	switch strings.ToLower(s) {
	case EngineNameScriggo, "":
		return EngineTypeScriggo, nil
	default:
		return 0, fmt.Errorf("unknown engine type: %q (valid: %s)", s, EngineNameScriggo)
	}
}

// FileRegistrar is an interface for dynamic file registration during template rendering.
// This interface is implemented by rendercontext.FileRegistry, allowing templates to
// register auxiliary files (certificates, maps, etc.) without creating import cycles.
//
// The Register method signature matches the variadic calling convention used in templates:
//
//	file_registry.Register("cert", "filename.pem", "content...")
//
// Arguments:
//   - args[0]: file type (string) - "cert", "map", "file", or "crt-list"
//   - args[1]: filename (string) - base filename
//   - args[2]: content (string) - file content
//
// Returns:
//   - Predicted absolute path where the file will be located
//   - Error if validation fails or content conflict detected
type FileRegistrar interface {
	Register(args ...interface{}) (string, error)
}

// ResourceStore defines the interface for resource stores accessible from templates.
// This interface enables direct method calls in Scriggo templates:
//
//	{% for _, ing := range resources.ingresses.List() %}
//	{% var secret = resources.secrets.GetSingle(namespace, name) %}
//	{% for _, ep := range resources.endpoints.Fetch(serviceName) %}
//
// Scriggo supports dot notation for map access, so `resources.ingresses` is equivalent
// to `resources["ingresses"]`.
//
// Implementations are provided by pkg/controller/rendercontext.StoreWrapper.
type ResourceStore interface {
	// List returns all resources from the store.
	List() []interface{}

	// Fetch returns resources matching the given keys (typically namespace, name).
	Fetch(keys ...interface{}) []interface{}

	// GetSingle returns a single resource matching the keys, or nil if not found.
	GetSingle(keys ...interface{}) interface{}
}

// HTTPFetcher defines the interface for HTTP resource fetching accessible from templates.
// This interface enables the http.Fetch() method in Scriggo templates:
//
//	{% var content = http.Fetch("https://example.com/blocklist.txt") %}
//	{% var content = http.Fetch(url, map[string]any{"delay": "60s", "critical": true}) %}
//
// Implementations are provided by pkg/controller/httpstore.HTTPStoreWrapper.
type HTTPFetcher interface {
	// Fetch fetches content from a URL with optional options and authentication.
	// Arguments:
	//   - args[0]: URL (string, required)
	//   - args[1]: options (map, optional) - {"delay": "60s", "timeout": "30s", "retries": 3, "critical": true}
	//   - args[2]: auth (map, optional) - {"type": "bearer"|"basic", "token": "...", ...}
	Fetch(args ...interface{}) (interface{}, error)
}

// RuntimeEnvironment holds runtime information available to templates.
// This enables templates to adapt behavior based on the execution environment.
//
// Templates access this via the runtimeEnvironment variable:
//
//	{%- var maxShards = runtimeEnvironment.GOMAXPROCS * 2 %}
//
// Fields use exported names for direct template access.
type RuntimeEnvironment struct {
	// GOMAXPROCS is the maximum number of OS threads for parallel execution.
	// Used by sharding logic to calculate optimal shard count.
	GOMAXPROCS int
}
