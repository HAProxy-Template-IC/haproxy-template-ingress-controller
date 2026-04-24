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

import "context"

// Engine is the contract every template engine implementation satisfies.
// The methods are grouped visually by concern — rendering, introspection,
// tracing, filter debug, and resource management — but they all live on a
// single interface because no current caller needs a narrower view and
// every implementation ends up providing the full surface anyway.
type Engine interface {
	// --- Rendering ---

	// Render executes a template with the given context and returns the output.
	// The ctx parameter is used for cancellation and timeout control.
	// Returns RenderError if template execution fails, RenderTimeoutError if
	// the context deadline is exceeded, or TemplateNotFoundError if the
	// template doesn't exist.
	Render(ctx context.Context, templateName string, templateContext map[string]any) (string, error)

	// RenderWithProfiling renders a template and returns profiling statistics
	// for included templates. Useful for performance debugging.
	//
	// Use NewScriggoWithProfiling to enable profiling. Returns nil
	// stats when profiling is disabled (NewScriggo).
	//
	// Stats are aggregated by template name — multiple renders of the same
	// template are combined into a single IncludeStats entry with count > 1.
	RenderWithProfiling(ctx context.Context, templateName string, templateContext map[string]any) (string, []IncludeStats, error)

	// --- Template introspection ---

	// EngineType returns the type of this engine.
	EngineType() EngineType

	// TemplateNames returns the names of all available templates, sorted alphabetically.
	TemplateNames() []string

	// HasTemplate checks if a template with the given name exists.
	HasTemplate(templateName string) bool

	// GetRawTemplate returns the original template string for the given name.
	// Returns TemplateNotFoundError if the template doesn't exist.
	GetRawTemplate(templateName string) (string, error)

	// TemplateCount returns the number of templates in the engine.
	TemplateCount() int

	// --- Execution tracing ---

	// EnableTracing enables template execution tracing for debugging.
	EnableTracing()

	// DisableTracing disables template execution tracing.
	DisableTracing()

	// IsTracingEnabled returns whether tracing is currently enabled.
	IsTracingEnabled() bool

	// GetTraceOutput returns accumulated trace output and clears the buffer.
	GetTraceOutput() string

	// AppendTraces appends traces from another engine to this engine's trace buffer.
	// This is useful for aggregating traces from multiple worker engines.
	AppendTraces(other Engine)

	// --- Filter debug logging ---

	// EnableFilterDebug enables detailed filter operation logging.
	EnableFilterDebug()

	// DisableFilterDebug disables detailed filter operation logging.
	DisableFilterDebug()

	// IsFilterDebugEnabled returns whether filter debug logging is enabled.
	IsFilterDebugEnabled() bool

	// --- Resource management ---

	// ClearVMPool releases pooled VMs to allow garbage collection.
	// Call after rendering completes to reduce memory from parallel rendering spikes.
	// No-op for engines that don't use VM pooling.
	ClearVMPool()
}
