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
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestScriggoProfiling_ThreadSafe(t *testing.T) {
	templates := map[string]string{
		"main.html": `{{ render "sub.html" }}`,
		"sub.html":  `content`,
	}

	engine, err := New(templates, &Options{EntryPoints: []string{"main.html"}, Profiling: true})
	require.NoError(t, err)

	var wg sync.WaitGroup
	errors := make(chan error, 10)

	for range 10 {
		wg.Go(func() {
			_, err := engine.Render(context.Background(), "main.html", nil)
			if err != nil {
				errors <- err
			}
		})
	}

	wg.Wait()
	close(errors)

	for err := range errors {
		t.Errorf("render error: %v", err)
	}
}

func TestScriggoProfiling_RenderWithProfiling_ReturnsStats(t *testing.T) {
	templates := map[string]string{
		"main.html": `Start{{ render "sub.html" }}End`,
		"sub.html":  `Middle`,
	}

	engine, err := New(templates, &Options{EntryPoints: []string{"main.html"}, Profiling: true})
	require.NoError(t, err)

	output, stats, err := engine.RenderWithProfiling(context.Background(), "main.html", nil)
	require.NoError(t, err)
	assert.Equal(t, "StartMiddleEnd\n", output)

	// Should return aggregated IncludeStats
	require.Len(t, stats, 1)
	assert.Equal(t, "sub.html", stats[0].Name)
	assert.Equal(t, 1, stats[0].Count)
	// Note: Very fast template executions may have sub-microsecond durations
	// that round to 0ms. We verify non-negative values rather than strictly positive.
	assert.GreaterOrEqual(t, stats[0].TotalMs, float64(0))
	assert.GreaterOrEqual(t, stats[0].AvgMs, float64(0))
	assert.GreaterOrEqual(t, stats[0].MaxMs, float64(0))
}

func TestScriggoProfiling_RenderWithProfiling_AggregatesLoopIterations(t *testing.T) {
	templates := map[string]string{
		"main.html": `{% for i := 0; i < 3; i++ %}{{ render "item.html" }}{% end %}`,
		"item.html": `X`,
	}

	engine, err := New(templates, &Options{EntryPoints: []string{"main.html"}, Profiling: true})
	require.NoError(t, err)

	output, stats, err := engine.RenderWithProfiling(context.Background(), "main.html", nil)
	require.NoError(t, err)
	assert.Equal(t, "XXX\n", output)

	// Should aggregate multiple renders of same template
	require.Len(t, stats, 1)
	assert.Equal(t, "item.html", stats[0].Name)
	assert.Equal(t, 3, stats[0].Count) // Called 3 times in loop
	// Note: Very fast template executions may measure as 0 on platforms with
	// coarse monotonic timers (e.g. ~0.5ms on Windows). We verify non-negative
	// values rather than strictly positive.
	assert.GreaterOrEqual(t, stats[0].TotalMs, float64(0))
}

func TestScriggoProfiling_RenderWithProfiling_DisabledReturnsNil(t *testing.T) {
	templates := map[string]string{
		"main.html": `{{ render "sub.html" }}`,
		"sub.html":  `content`,
	}

	// Without profiling
	engine, err := New(templates, &Options{EntryPoints: []string{"main.html"}})
	require.NoError(t, err)

	output, stats, err := engine.RenderWithProfiling(context.Background(), "main.html", nil)
	require.NoError(t, err)
	assert.Equal(t, "content\n", output)
	assert.Nil(t, stats) // No stats when profiling disabled
}

// Tests for tracing with nesting (requires profiling-enabled engine)

func TestScriggoTracing_NestedOutput(t *testing.T) {
	templates := map[string]string{
		"main.html": `A{{ render "sub.html" }}B`,
		"sub.html":  `X`,
	}

	// Must use profiling-enabled engine for nested tracing
	engine, err := New(templates, &Options{EntryPoints: []string{"main.html"}, Profiling: true})
	require.NoError(t, err)

	engine.EnableTracing()
	output, err := engine.Render(context.Background(), "main.html", nil)
	require.NoError(t, err)
	assert.Equal(t, "AXB\n", output)

	trace := engine.GetTraceOutput()
	// Should show nested indentation
	assert.Contains(t, trace, "Rendering: main.html")
	assert.Contains(t, trace, "  Rendering: sub.html") // Indented
	assert.Contains(t, trace, "  Completed: sub.html") // Indented
	assert.Contains(t, trace, "Completed: main.html")
}

func TestScriggoTracing_DeeplyNestedOutput(t *testing.T) {
	templates := map[string]string{
		"main.html": `{{ render "l1.html" }}`,
		"l1.html":   `{{ render "l2.html" }}`,
		"l2.html":   `{{ render "l3.html" }}`,
		"l3.html":   `leaf`,
	}

	engine, err := New(templates, &Options{EntryPoints: []string{"main.html"}, Profiling: true})
	require.NoError(t, err)

	engine.EnableTracing()
	output, err := engine.Render(context.Background(), "main.html", nil)
	require.NoError(t, err)
	assert.Equal(t, "leaf\n", output)

	trace := engine.GetTraceOutput()
	// Verify increasing indentation levels
	assert.Contains(t, trace, "Rendering: main.html")
	assert.Contains(t, trace, "  Rendering: l1.html")     // 1 level
	assert.Contains(t, trace, "    Rendering: l2.html")   // 2 levels
	assert.Contains(t, trace, "      Rendering: l3.html") // 3 levels
	assert.Contains(t, trace, "      Completed: l3.html")
	assert.Contains(t, trace, "    Completed: l2.html")
	assert.Contains(t, trace, "  Completed: l1.html")
	assert.Contains(t, trace, "Completed: main.html")
}

func TestScriggoTracing_NoProfilingFlatOutput(t *testing.T) {
	templates := map[string]string{
		"main.html": `A{{ render "sub.html" }}B`,
		"sub.html":  `X`,
	}

	// Without profiling - should get flat trace (only main template)
	engine, err := New(templates, &Options{EntryPoints: []string{"main.html"}})
	require.NoError(t, err)

	engine.EnableTracing()
	output, err := engine.Render(context.Background(), "main.html", nil)
	require.NoError(t, err)
	assert.Equal(t, "AXB\n", output)

	trace := engine.GetTraceOutput()
	// Should have main template but NOT nested sub.html trace
	assert.Contains(t, trace, "Rendering: main.html")
	assert.Contains(t, trace, "Completed: main.html")
	// No nested indentation for sub.html (profiling not enabled)
	assert.NotContains(t, trace, "  Rendering: sub.html")
}

func TestScriggoTracing_LoopWithNesting(t *testing.T) {
	templates := map[string]string{
		"main.html": `{% for i := 0; i < 2; i++ %}{{ render "item.html" }}{% end %}`,
		"item.html": `X`,
	}

	engine, err := New(templates, &Options{EntryPoints: []string{"main.html"}, Profiling: true})
	require.NoError(t, err)

	engine.EnableTracing()
	output, err := engine.Render(context.Background(), "main.html", nil)
	require.NoError(t, err)
	assert.Equal(t, "XX\n", output)

	trace := engine.GetTraceOutput()
	// Should show both iterations with indentation
	assert.Contains(t, trace, "Rendering: main.html")
	// Count indented item.html entries (should be 2)
	count := strings.Count(trace, "  Rendering: item.html")
	assert.Equal(t, 2, count, "should have 2 indented item.html entries")
}
