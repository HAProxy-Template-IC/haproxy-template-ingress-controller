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
	"fmt"
	"runtime"
	"testing"
)

// BenchmarkVMPool measures allocation and GC impact of ClearVMPool after parallel rendering.
//
// This benchmark simulates production-like parallel rendering using `go MacroName()` in
// templates and compares performance with and without ClearVMPool between renders.
//
// Production fidelity:
//   - 6 independent sharded operations per render (~114 VMs total), matching the 6
//     sharded ingress template call sites in ingress.yaml.
//   - 20 shards per operation, matching ~1903 ingresses / 100 items per shard.
//
// Known limitations vs production:
//   - Templates are simpler (no dig/sort_by/render_glob/shared context).
//   - Tight render loop keeps sync.Pool warm; production renders every ~5s, so GC
//     may evict idle pool entries between renders (making ClearVMPool even more redundant).
//
// Run with: make bench PKG=./pkg/templating/ BENCH=BenchmarkVMPool COUNT=6.
func BenchmarkVMPool(b *testing.B) {
	const (
		shardCount     = 20 // ~production shard count (1903 ingresses / 100)
		operationCount = 6  // number of independent sharded operations per render
	)

	// Generate test data: 2000 items to be sharded across ~20 goroutines per operation.
	items := make([]interface{}, 2000)
	for i := range items {
		items[i] = map[string]interface{}{
			"name":    fmt.Sprintf("srv%d", i),
			"address": fmt.Sprintf("10.0.%d.%d", i/256, i%256),
			"port":    8080 + (i % 10),
		}
	}

	// Template structure mirrors production ingress.yaml which has multiple independent
	// sharded operations (backends, host-map, path-exact, path-prefix, path-regex,
	// status-patches), each spawning ~20 goroutines via `go ProcessShard(...)`.
	//
	// The "main" template calls ShardedRender N times (once per operation) to simulate
	// the full VM pool pressure of a production render (~114 VMs).
	templates := map[string]string{
		"main": `{% import "sharding" for ShardedRender %}
{%- for i := 0; i < operations; i++ -%}
{{ ShardedRender(shards) }}
{%- end -%}
`,
		"sharding": `{% import "worker" for ProcessShard %}
{% macro ShardedRender(shards []any) string %}
{%- for _, shard := range shards -%}
{{ go ProcessShard(shard.([]any)) }}
{%- end -%}
{% end %}
`,
		"worker": `{% macro ProcessShard(shard []any) string %}
{%- for _, item := range shard -%}
server {{ item.(map[string]any)["name"] }} {{ item.(map[string]any)["address"] }}:{{ item.(map[string]any)["port"] }} check
{% end -%}
{% end %}
`,
	}

	// Declare runtime variables so templates can reference them by bare name.
	// The nil pointer tells Scriggo the type at compile time; the actual value is
	// provided at render time via the context map.
	declarations := map[string]any{
		"shards":     (*[]interface{})(nil),
		"operations": (*int)(nil),
	}
	engine, err := NewScriggoWithDeclarations(templates, []string{"main"}, nil, nil, nil, declarations)
	if err != nil {
		b.Fatalf("failed to create engine: %v", err)
	}

	// Split items into shardCount chunks, each passed as a separate shard.
	// This causes `go ProcessShard(...)` to spawn shardCount goroutines per operation.
	shards := make([]interface{}, shardCount)
	chunkSize := (len(items) + shardCount - 1) / shardCount
	for i := range shards {
		start := i * chunkSize
		end := start + chunkSize
		if end > len(items) {
			end = len(items)
		}
		shards[i] = items[start:end]
	}

	ctx := map[string]interface{}{
		"shards":     shards,
		"operations": operationCount,
	}

	// Verify template renders correctly before benchmarking.
	output, err := engine.Render(context.Background(), "main", ctx)
	if err != nil {
		b.Fatalf("template render failed: %v", err)
	}
	// Each operation produces ~2000 server lines; 6 operations should produce substantial output.
	expectedMinBytes := 1000 * operationCount
	if len(output) < expectedMinBytes {
		b.Fatalf("template output too short (%d bytes, expected >%d), parallel rendering may not be working",
			len(output), expectedMinBytes)
	}

	b.Run("with_clear", func(b *testing.B) {
		b.ReportAllocs()
		var r string
		for i := 0; i < b.N; i++ {
			r, _ = engine.Render(context.Background(), "main", ctx)
			engine.ClearVMPool()
		}
		benchResultString = r
	})

	b.Run("without_clear", func(b *testing.B) {
		b.ReportAllocs()
		var r string
		for i := 0; i < b.N; i++ {
			r, _ = engine.Render(context.Background(), "main", ctx)
		}
		benchResultString = r
	})

	// Memory measurement sub-benchmark: compare heap stats after N iterations.
	b.Run("memory_profile", func(b *testing.B) {
		b.ReportAllocs()

		// Warm up pool.
		for i := 0; i < 5; i++ {
			engine.Render(context.Background(), "main", ctx)
		}

		var mBefore, mAfter runtime.MemStats
		runtime.GC()
		runtime.ReadMemStats(&mBefore)

		for i := 0; i < b.N; i++ {
			engine.Render(context.Background(), "main", ctx)
			// No ClearVMPool — measure steady-state pool memory.
		}

		runtime.GC()
		runtime.ReadMemStats(&mAfter)

		var heapDelta float64
		if mAfter.HeapInuse > mBefore.HeapInuse {
			heapDelta = float64(mAfter.HeapInuse-mBefore.HeapInuse) / float64(b.N)
		}
		b.ReportMetric(heapDelta, "heap-delta-B/op")
		b.ReportMetric(float64(mAfter.PauseTotalNs-mBefore.PauseTotalNs)/float64(b.N), "gc-pause-ns/op")
	})
}
