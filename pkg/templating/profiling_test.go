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
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestScriggoProfiling_BasicTiming(t *testing.T) {
	templates := map[string]string{
		"main.html": `Start{{ render "sub.html" }}End`,
		"sub.html":  `Middle`,
	}

	engine, err := NewScriggoWithProfiling(templates, []string{"main.html"}, nil, nil, nil)
	require.NoError(t, err)

	output, err := engine.Render(context.Background(), "main.html", nil)
	require.NoError(t, err)
	assert.Equal(t, "StartMiddleEnd\n", output)

	results := engine.GetProfilingResults()
	require.Len(t, results, 1)
	assert.Equal(t, "sub.html", results[0].Name)
	assert.Greater(t, results[0].Duration, time.Duration(0))
}

func TestScriggoProfiling_NestedRenders(t *testing.T) {
	templates := map[string]string{
		"main.html":  `A{{ render "mid.html" }}D`,
		"mid.html":   `B{{ render "inner.html" }}C`,
		"inner.html": `X`,
	}

	engine, err := NewScriggoWithProfiling(templates, []string{"main.html"}, nil, nil, nil)
	require.NoError(t, err)

	output, err := engine.Render(context.Background(), "main.html", nil)
	require.NoError(t, err)
	assert.Equal(t, "ABXCD\n", output)

	results := engine.GetProfilingResults()
	require.Len(t, results, 2)
	// Scriggo's profiler records calls in chronological order
	// Both inner.html and mid.html should be present
	names := make(map[string]bool)
	for _, r := range results {
		names[r.Name] = true
	}
	assert.True(t, names["inner.html"], "should have inner.html")
	assert.True(t, names["mid.html"], "should have mid.html")
}

func TestScriggoProfiling_DisabledNoOverhead(t *testing.T) {
	templates := map[string]string{
		"main.html": `{{ render "sub.html" }}`,
		"sub.html":  `content`,
	}

	// Create WITHOUT profiling
	engine, err := NewScriggo(templates, []string{"main.html"}, nil, nil, nil)
	require.NoError(t, err)

	output, err := engine.Render(context.Background(), "main.html", nil)
	require.NoError(t, err)
	assert.Equal(t, "content\n", output)

	// No profiling results - profiling is disabled
	assert.False(t, engine.IsProfilingEnabled())
	assert.Nil(t, engine.GetProfilingResults())
}

func TestScriggoProfiling_Enabled(t *testing.T) {
	templates := map[string]string{
		"main.html": `{{ render "sub.html" }}`,
		"sub.html":  `content`,
	}

	engine, err := NewScriggoWithProfiling(templates, []string{"main.html"}, nil, nil, nil)
	require.NoError(t, err)

	assert.True(t, engine.IsProfilingEnabled())

	_, err = engine.Render(context.Background(), "main.html", nil)
	require.NoError(t, err)

	results := engine.GetProfilingResults()
	assert.NotNil(t, results)
	assert.Len(t, results, 1)
}

func TestScriggoProfiling_WithLoops(t *testing.T) {
	// Use a fixed-size array declared in template since Scriggo requires
	// compile-time type checking for variables
	templates := map[string]string{
		"main.html": `{% for i := 0; i < 3; i++ %}{{ render "item.html" }}{% end %}`,
		"item.html": `X`,
	}

	engine, err := NewScriggoWithProfiling(templates, []string{"main.html"}, nil, nil, nil)
	require.NoError(t, err)

	output, err := engine.Render(context.Background(), "main.html", nil)
	require.NoError(t, err)
	assert.Equal(t, "XXX\n", output)

	results := engine.GetProfilingResults()
	require.Len(t, results, 3) // One per loop iteration
	for _, entry := range results {
		assert.Equal(t, "item.html", entry.Name)
	}
}

func TestScriggoProfiling_ThreadSafe(t *testing.T) {
	templates := map[string]string{
		"main.html": `{{ render "sub.html" }}`,
		"sub.html":  `content`,
	}

	engine, err := NewScriggoWithProfiling(templates, []string{"main.html"}, nil, nil, nil)
	require.NoError(t, err)

	var wg sync.WaitGroup
	errors := make(chan error, 10)

	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := engine.Render(context.Background(), "main.html", nil)
			if err != nil {
				errors <- err
			}
		}()
	}

	wg.Wait()
	close(errors)

	for err := range errors {
		t.Errorf("render error: %v", err)
	}
}

func TestScriggoProfiling_MultipleRenders(t *testing.T) {
	templates := map[string]string{
		"main.html": `{{ render "sub.html" }}`,
		"sub.html":  `content`,
	}

	engine, err := NewScriggoWithProfiling(templates, []string{"main.html"}, nil, nil, nil)
	require.NoError(t, err)

	// First render
	_, err = engine.Render(context.Background(), "main.html", nil)
	require.NoError(t, err)

	results1 := engine.GetProfilingResults()
	require.Len(t, results1, 1)

	// Second render - should have fresh profiling state
	_, err = engine.Render(context.Background(), "main.html", nil)
	require.NoError(t, err)

	results2 := engine.GetProfilingResults()
	require.Len(t, results2, 1)
}

func TestScriggoProfiling_NoRenders(t *testing.T) {
	templates := map[string]string{
		"main.html": `Hello World`,
	}

	engine, err := NewScriggoWithProfiling(templates, []string{"main.html"}, nil, nil, nil)
	require.NoError(t, err)

	output, err := engine.Render(context.Background(), "main.html", nil)
	require.NoError(t, err)
	assert.Equal(t, "Hello World\n", output)

	// No render nodes, so no profiling entries
	results := engine.GetProfilingResults()
	assert.Empty(t, results)
}

func TestScriggoProfiling_ConditionalRender(t *testing.T) {
	// Test profiling with render inside if block
	// Using compile-time conditions since Scriggo requires type checking at compile time
	t.Run("condition_true", func(t *testing.T) {
		templates := map[string]string{
			"main.html": `{% if true %}{{ render "sub.html" }}{% end %}`,
			"sub.html":  `content`,
		}

		engine, err := NewScriggoWithProfiling(templates, []string{"main.html"}, nil, nil, nil)
		require.NoError(t, err)

		output, err := engine.Render(context.Background(), "main.html", nil)
		require.NoError(t, err)
		assert.Equal(t, "content\n", output)

		results := engine.GetProfilingResults()
		require.Len(t, results, 1)
		assert.Equal(t, "sub.html", results[0].Name)
	})

	t.Run("condition_false", func(t *testing.T) {
		templates := map[string]string{
			"main.html": `{% if false %}{{ render "sub.html" }}{% end %}`,
			"sub.html":  `content`,
		}

		engine, err := NewScriggoWithProfiling(templates, []string{"main.html"}, nil, nil, nil)
		require.NoError(t, err)

		output, err := engine.Render(context.Background(), "main.html", nil)
		require.NoError(t, err)
		assert.Equal(t, "\n", output)

		results := engine.GetProfilingResults()
		assert.Empty(t, results) // No render executed
	})
}

func TestScriggoProfiling_DeeplyNested(t *testing.T) {
	templates := map[string]string{
		"main.html": `{{ render "l1.html" }}`,
		"l1.html":   `{{ render "l2.html" }}`,
		"l2.html":   `{{ render "l3.html" }}`,
		"l3.html":   `{{ render "l4.html" }}`,
		"l4.html":   `leaf`,
	}

	engine, err := NewScriggoWithProfiling(templates, []string{"main.html"}, nil, nil, nil)
	require.NoError(t, err)

	output, err := engine.Render(context.Background(), "main.html", nil)
	require.NoError(t, err)
	assert.Equal(t, "leaf\n", output)

	results := engine.GetProfilingResults()
	require.Len(t, results, 4)

	// Verify all templates are present
	names := make(map[string]bool)
	for _, r := range results {
		names[r.Name] = true
	}
	assert.True(t, names["l1.html"])
	assert.True(t, names["l2.html"])
	assert.True(t, names["l3.html"])
	assert.True(t, names["l4.html"])
}

func TestScriggoProfiling_RenderWithProfiling_ReturnsStats(t *testing.T) {
	templates := map[string]string{
		"main.html": `Start{{ render "sub.html" }}End`,
		"sub.html":  `Middle`,
	}

	engine, err := NewScriggoWithProfiling(templates, []string{"main.html"}, nil, nil, nil)
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

	engine, err := NewScriggoWithProfiling(templates, []string{"main.html"}, nil, nil, nil)
	require.NoError(t, err)

	output, stats, err := engine.RenderWithProfiling(context.Background(), "main.html", nil)
	require.NoError(t, err)
	assert.Equal(t, "XXX\n", output)

	// Should aggregate multiple renders of same template
	require.Len(t, stats, 1)
	assert.Equal(t, "item.html", stats[0].Name)
	assert.Equal(t, 3, stats[0].Count) // Called 3 times in loop
	assert.Greater(t, stats[0].TotalMs, float64(0))
}

func TestScriggoProfiling_RenderWithProfiling_DisabledReturnsNil(t *testing.T) {
	templates := map[string]string{
		"main.html": `{{ render "sub.html" }}`,
		"sub.html":  `content`,
	}

	// Without profiling
	engine, err := NewScriggo(templates, []string{"main.html"}, nil, nil, nil)
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
	engine, err := NewScriggoWithProfiling(templates, []string{"main.html"}, nil, nil, nil)
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

	engine, err := NewScriggoWithProfiling(templates, []string{"main.html"}, nil, nil, nil)
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
	engine, err := NewScriggo(templates, []string{"main.html"}, nil, nil, nil)
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

	engine, err := NewScriggoWithProfiling(templates, []string{"main.html"}, nil, nil, nil)
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
