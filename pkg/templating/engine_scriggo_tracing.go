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
	"fmt"
	"strings"
	"sync"
	"time"

	"gitlab.com/haproxy-haptic/scriggo"
)

// scriggoTracingConfig holds template tracing configuration for Scriggo.
type scriggoTracingConfig struct {
	enabled bool
	// debugFilters is reserved for future filter debug logging implementation.
	// Currently set by EnableFilterDebug()/DisableFilterDebug() but not yet used.
	// Scriggo filters are standalone globals without access to per-render state.
	// Implementing this would require restructuring filter registration to use
	// closures that capture the tracing config.
	debugFilters bool
	mu           sync.RWMutex
	traces       []string
}

// EnableTracing enables template execution tracing.
func (e *ScriggoEngine) EnableTracing() {
	e.tracing.mu.Lock()
	defer e.tracing.mu.Unlock()
	e.tracing.enabled = true
}

// DisableTracing disables template execution tracing.
func (e *ScriggoEngine) DisableTracing() {
	e.tracing.mu.Lock()
	defer e.tracing.mu.Unlock()
	e.tracing.enabled = false
}

// IsTracingEnabled returns whether tracing is currently enabled.
func (e *ScriggoEngine) IsTracingEnabled() bool {
	e.tracing.mu.RLock()
	defer e.tracing.mu.RUnlock()
	return e.tracing.enabled
}

// GetTraceOutput returns accumulated trace output and clears the buffer.
func (e *ScriggoEngine) GetTraceOutput() string {
	e.tracing.mu.Lock()
	defer e.tracing.mu.Unlock()

	if len(e.tracing.traces) == 0 {
		return ""
	}

	output := strings.Join(e.tracing.traces, "")
	e.tracing.traces = make([]string, 0)
	return output
}

// EnableFilterDebug enables detailed filter operation logging.
func (e *ScriggoEngine) EnableFilterDebug() {
	e.tracing.mu.Lock()
	defer e.tracing.mu.Unlock()
	e.tracing.debugFilters = true
}

// DisableFilterDebug disables detailed filter operation logging.
func (e *ScriggoEngine) DisableFilterDebug() {
	e.tracing.mu.Lock()
	defer e.tracing.mu.Unlock()
	e.tracing.debugFilters = false
}

// IsFilterDebugEnabled returns true if filter debug logging is currently enabled.
func (e *ScriggoEngine) IsFilterDebugEnabled() bool {
	e.tracing.mu.RLock()
	defer e.tracing.mu.RUnlock()
	return e.tracing.debugFilters
}

// AppendTraces appends traces from another engine to this engine's trace buffer.
// This is useful for aggregating traces from multiple worker engines.
func (e *ScriggoEngine) AppendTraces(other Engine) {
	if other == nil {
		return
	}

	// Get traces from the other engine (this clears its buffer)
	traces := other.GetTraceOutput()
	if traces == "" {
		return
	}

	// Append to this engine's trace buffer
	e.tracing.mu.Lock()
	e.tracing.traces = append(e.tracing.traces, traces)
	e.tracing.mu.Unlock()
}

// storeTraceOutput stores the accumulated trace output from a render operation.
// When profile is available (profiling enabled), it builds nested output from the call tree.
// Otherwise falls back to basic tracing with traceBuilder.
func (e *ScriggoEngine) storeTraceOutput(templateName string, profile *scriggo.Profile, traceBuilder *strings.Builder, duration time.Duration) {
	e.tracing.mu.Lock()
	defer e.tracing.mu.Unlock()

	if profile != nil && len(profile.Calls) > 0 {
		// Build nested trace output from profile call tree
		var fullTrace strings.Builder
		fmt.Fprintf(&fullTrace, "Rendering: %s\n", templateName)

		// Build trace from call tree
		tree := profile.Tree()
		if tree != nil {
			e.writeCallTreeTrace(&fullTrace, tree.Children, 1)
		}

		fmt.Fprintf(&fullTrace, "Completed: %s (%s)\n", templateName, formatDuration(duration))
		e.tracing.traces = append(e.tracing.traces, fullTrace.String())
	} else if traceBuilder != nil {
		// Fall back to basic tracing (profiling not enabled or no calls)
		fmt.Fprintf(traceBuilder, "Completed: %s (%s)\n", templateName, formatDuration(duration))
		e.tracing.traces = append(e.tracing.traces, traceBuilder.String())
	}
}

// writeCallTreeTrace recursively writes call tree nodes to the trace output.
func (e *ScriggoEngine) writeCallTreeTrace(builder *strings.Builder, nodes []*scriggo.CallNode, depth int) {
	indent := strings.Repeat("  ", depth)
	for _, node := range nodes {
		if node.Call == nil {
			continue
		}
		// Trace macro/render operations
		// Note: In Scriggo, `render` statements compile to CallKindMacro
		if node.Call.Kind == scriggo.CallKindMacro {
			name := node.Call.TemplatePath
			if name == "" {
				name = node.Call.Name
			}
			fmt.Fprintf(builder, "%sRendering: %s\n", indent, name)
			// Recurse into children
			e.writeCallTreeTrace(builder, node.Children, depth+1)
			fmt.Fprintf(builder, "%sCompleted: %s (%s)\n", indent, name, formatDuration(node.Call.Duration))
		} else {
			// For non-include calls, still recurse into children to find nested includes
			e.writeCallTreeTrace(builder, node.Children, depth)
		}
	}
}

// formatDuration formats a duration as milliseconds with 3 decimal places.
func formatDuration(d time.Duration) string {
	ms := float64(d.Microseconds()) / 1000.0
	return fmt.Sprintf("%.3fms", ms)
}
