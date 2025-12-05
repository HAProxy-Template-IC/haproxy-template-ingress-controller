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

package comparator

import (
	"context"
	"fmt"
	"testing"

	"haproxy-template-ic/pkg/dataplane/client"
	"haproxy-template-ic/pkg/dataplane/comparator/sections"
	"haproxy-template-ic/pkg/dataplane/parser"
)

// Package-level result sink prevents compiler dead code elimination.
var benchResultDiff *ConfigDiff

// BenchmarkCompare benchmarks configuration comparison with various sizes.
// Run with: go test -bench=BenchmarkCompare -benchmem -count=6.
func BenchmarkCompare(b *testing.B) {
	b.Run("size=small", func(b *testing.B) {
		benchmarkCompareSmall(b)
	})

	b.Run("size=medium", func(b *testing.B) {
		benchmarkCompareMedium(b)
	})

	b.Run("size=large", func(b *testing.B) {
		benchmarkCompareLarge(b)
	})
}

// BenchmarkCompare_NoChanges benchmarks comparison when configs are identical.
func BenchmarkCompare_NoChanges(b *testing.B) {
	config := generateMediumConfig()

	p, err := parser.New()
	if err != nil {
		b.Fatalf("failed to create parser: %v", err)
	}

	current, err := p.ParseFromString(config)
	if err != nil {
		b.Fatalf("failed to parse config: %v", err)
	}

	// Use same config for current and desired
	desired := current

	comp := New()

	b.ResetTimer()
	b.ReportAllocs()

	var r *ConfigDiff
	for i := 0; i < b.N; i++ {
		r, _ = comp.Compare(current, desired)
	}
	benchResultDiff = r
}

// BenchmarkCompare_OnlyAdditions benchmarks comparison when only adding resources.
func BenchmarkCompare_OnlyAdditions(b *testing.B) {
	p, err := parser.New()
	if err != nil {
		b.Fatalf("failed to create parser: %v", err)
	}

	current, err := p.ParseFromString(generateSmallConfig())
	if err != nil {
		b.Fatalf("failed to parse current config: %v", err)
	}

	desired, err := p.ParseFromString(generateMediumConfig())
	if err != nil {
		b.Fatalf("failed to parse desired config: %v", err)
	}

	comp := New()

	b.ResetTimer()
	b.ReportAllocs()

	var r *ConfigDiff
	for i := 0; i < b.N; i++ {
		r, _ = comp.Compare(current, desired)
	}
	benchResultDiff = r
}

// BenchmarkCompare_OnlyDeletions benchmarks comparison when only removing resources.
func BenchmarkCompare_OnlyDeletions(b *testing.B) {
	p, err := parser.New()
	if err != nil {
		b.Fatalf("failed to create parser: %v", err)
	}

	current, err := p.ParseFromString(generateMediumConfig())
	if err != nil {
		b.Fatalf("failed to parse current config: %v", err)
	}

	desired, err := p.ParseFromString(generateSmallConfig())
	if err != nil {
		b.Fatalf("failed to parse desired config: %v", err)
	}

	comp := New()

	b.ResetTimer()
	b.ReportAllocs()

	var r *ConfigDiff
	for i := 0; i < b.N; i++ {
		r, _ = comp.Compare(current, desired)
	}
	benchResultDiff = r
}

// BenchmarkCompare_MixedChanges benchmarks comparison with adds, updates, and deletes.
func BenchmarkCompare_MixedChanges(b *testing.B) {
	p, err := parser.New()
	if err != nil {
		b.Fatalf("failed to create parser: %v", err)
	}

	current, err := p.ParseFromString(generateMediumConfig())
	if err != nil {
		b.Fatalf("failed to parse current config: %v", err)
	}

	desired, err := p.ParseFromString(generateMediumConfigWithChanges())
	if err != nil {
		b.Fatalf("failed to parse desired config: %v", err)
	}

	comp := New()

	b.ResetTimer()
	b.ReportAllocs()

	var r *ConfigDiff
	for i := 0; i < b.N; i++ {
		r, _ = comp.Compare(current, desired)
	}
	benchResultDiff = r
}

// BenchmarkCompare_Scale benchmarks comparison with varying backend counts.
func BenchmarkCompare_Scale(b *testing.B) {
	for _, backendCount := range []int{10, 50, 100} {
		b.Run(fmt.Sprintf("backends=%d", backendCount), func(b *testing.B) {
			benchmarkCompareBackendScale(b, backendCount)
		})
	}
}

// BenchmarkOrderOperations benchmarks operation ordering.
func BenchmarkOrderOperations(b *testing.B) {
	b.Run("size=small", func(b *testing.B) {
		benchmarkOrderSmall(b)
	})

	b.Run("size=medium", func(b *testing.B) {
		benchmarkOrderMedium(b)
	})

	b.Run("size=large", func(b *testing.B) {
		benchmarkOrderLarge(b)
	})
}

// benchmarkCompareSmall benchmarks comparison with a small config (2 backends, 1 frontend).
func benchmarkCompareSmall(b *testing.B) {
	b.Helper()
	p, err := parser.New()
	if err != nil {
		b.Fatalf("failed to create parser: %v", err)
	}

	current, err := p.ParseFromString(generateSmallConfig())
	if err != nil {
		b.Fatalf("failed to parse current config: %v", err)
	}

	desired, err := p.ParseFromString(generateSmallConfigModified())
	if err != nil {
		b.Fatalf("failed to parse desired config: %v", err)
	}

	comp := New()

	b.ResetTimer()
	b.ReportAllocs()

	var r *ConfigDiff
	for i := 0; i < b.N; i++ {
		r, _ = comp.Compare(current, desired)
	}
	benchResultDiff = r
}

// benchmarkCompareMedium benchmarks comparison with a medium config (5 backends, 2 frontends).
func benchmarkCompareMedium(b *testing.B) {
	b.Helper()
	p, err := parser.New()
	if err != nil {
		b.Fatalf("failed to create parser: %v", err)
	}

	current, err := p.ParseFromString(generateMediumConfig())
	if err != nil {
		b.Fatalf("failed to parse current config: %v", err)
	}

	desired, err := p.ParseFromString(generateMediumConfigWithChanges())
	if err != nil {
		b.Fatalf("failed to parse desired config: %v", err)
	}

	comp := New()

	b.ResetTimer()
	b.ReportAllocs()

	var r *ConfigDiff
	for i := 0; i < b.N; i++ {
		r, _ = comp.Compare(current, desired)
	}
	benchResultDiff = r
}

// benchmarkCompareLarge benchmarks comparison with a large config (20 backends, 5 frontends).
func benchmarkCompareLarge(b *testing.B) {
	b.Helper()
	p, err := parser.New()
	if err != nil {
		b.Fatalf("failed to create parser: %v", err)
	}

	current, err := p.ParseFromString(generateLargeConfig())
	if err != nil {
		b.Fatalf("failed to parse current config: %v", err)
	}

	desired, err := p.ParseFromString(generateLargeConfigWithChanges())
	if err != nil {
		b.Fatalf("failed to parse desired config: %v", err)
	}

	comp := New()

	b.ResetTimer()
	b.ReportAllocs()

	var r *ConfigDiff
	for i := 0; i < b.N; i++ {
		r, _ = comp.Compare(current, desired)
	}
	benchResultDiff = r
}

// benchmarkCompareBackendScale benchmarks comparison with varying backend counts.
func benchmarkCompareBackendScale(b *testing.B, backendCount int) {
	b.Helper()
	p, err := parser.New()
	if err != nil {
		b.Fatalf("failed to create parser: %v", err)
	}

	current, err := p.ParseFromString(generateScaledConfig(backendCount, 3))
	if err != nil {
		b.Fatalf("failed to parse current config: %v", err)
	}

	desired, err := p.ParseFromString(generateScaledConfigWithChanges(backendCount, 3))
	if err != nil {
		b.Fatalf("failed to parse desired config: %v", err)
	}

	comp := New()

	b.ResetTimer()
	b.ReportAllocs()

	var r *ConfigDiff
	for i := 0; i < b.N; i++ {
		r, _ = comp.Compare(current, desired)
	}
	benchResultDiff = r
}

// benchmarkOrderSmall benchmarks ordering a small number of operations.
func benchmarkOrderSmall(b *testing.B) {
	b.Helper()
	ops := generateMockOperations(10)

	b.ResetTimer()
	b.ReportAllocs()

	var r []Operation
	for i := 0; i < b.N; i++ {
		r = OrderOperations(ops)
	}
	_ = r
}

// benchmarkOrderMedium benchmarks ordering a medium number of operations.
func benchmarkOrderMedium(b *testing.B) {
	b.Helper()
	ops := generateMockOperations(50)

	b.ResetTimer()
	b.ReportAllocs()

	var r []Operation
	for i := 0; i < b.N; i++ {
		r = OrderOperations(ops)
	}
	_ = r
}

// benchmarkOrderLarge benchmarks ordering a large number of operations.
func benchmarkOrderLarge(b *testing.B) {
	b.Helper()
	ops := generateMockOperations(200)

	b.ResetTimer()
	b.ReportAllocs()

	var r []Operation
	for i := 0; i < b.N; i++ {
		r = OrderOperations(ops)
	}
	_ = r
}

// Helper functions to generate test configs.

func generateSmallConfig() string {
	return `
global
    daemon

defaults
    mode http
    timeout connect 5000ms
    timeout client 50000ms
    timeout server 50000ms

frontend http_front
    bind *:80
    default_backend main_backend

backend main_backend
    balance roundrobin
    server srv1 127.0.0.1:8080 check

backend api_backend
    balance leastconn
    server srv1 127.0.0.1:8081 check
`
}

func generateSmallConfigModified() string {
	return `
global
    daemon

defaults
    mode http
    timeout connect 5000ms
    timeout client 50000ms
    timeout server 50000ms

frontend http_front
    bind *:80
    default_backend main_backend

backend main_backend
    balance leastconn
    server srv1 127.0.0.1:8080 check weight 100
    server srv2 127.0.0.1:8082 check

backend api_backend
    balance leastconn
    server srv1 127.0.0.1:8081 check
`
}

func generateMediumConfig() string {
	return `
global
    daemon

defaults
    mode http
    timeout connect 5000ms
    timeout client 50000ms
    timeout server 50000ms

frontend http_front
    bind *:80
    acl is_api path_beg /api
    use_backend api_backend if is_api
    default_backend main_backend

frontend https_front
    bind *:443
    default_backend main_backend

backend main_backend
    balance roundrobin
    server srv1 10.0.0.1:8080 check
    server srv2 10.0.0.2:8080 check
    server srv3 10.0.0.3:8080 check

backend api_backend
    balance leastconn
    server api1 10.0.1.1:8080 check
    server api2 10.0.1.2:8080 check

backend static_backend
    balance roundrobin
    server static1 10.0.2.1:80

backend admin_backend
    balance roundrobin
    server admin1 10.0.3.1:9000 check

backend cache_backend
    balance roundrobin
    server cache1 10.0.4.1:6379
`
}

func generateMediumConfigWithChanges() string {
	return `
global
    daemon

defaults
    mode http
    timeout connect 10000ms
    timeout client 50000ms
    timeout server 50000ms

frontend http_front
    bind *:80
    acl is_api path_beg /api
    acl is_static path_beg /static
    use_backend api_backend if is_api
    use_backend static_backend if is_static
    default_backend main_backend

frontend https_front
    bind *:443
    default_backend main_backend

backend main_backend
    balance leastconn
    server srv1 10.0.0.1:8080 check weight 100
    server srv2 10.0.0.2:8080 check weight 50
    server srv4 10.0.0.4:8080 check

backend api_backend
    balance leastconn
    server api1 10.0.1.1:8080 check
    server api2 10.0.1.2:8080 check
    server api3 10.0.1.3:8080 check

backend static_backend
    balance roundrobin
    server static1 10.0.2.1:80
    server static2 10.0.2.2:80

backend admin_backend
    balance roundrobin
    server admin1 10.0.3.1:9000 check

backend new_backend
    balance roundrobin
    server new1 10.0.5.1:8080 check
`
}

func generateLargeConfig() string {
	return generateScaledConfig(20, 5)
}

func generateLargeConfigWithChanges() string {
	return generateScaledConfigWithChanges(20, 5)
}

func generateScaledConfig(backendCount, serversPerBackend int) string {
	config := `
global
    daemon

defaults
    mode http
    timeout connect 5000ms
    timeout client 50000ms
    timeout server 50000ms

frontend http_front
    bind *:80
    default_backend backend_0

`
	for i := 0; i < backendCount; i++ {
		config += fmt.Sprintf("backend backend_%d\n", i)
		config += "    balance roundrobin\n"
		for j := 0; j < serversPerBackend; j++ {
			config += fmt.Sprintf("    server srv%d_%d 10.%d.%d.1:%d check\n", i, j, i/256, i%256, 8080+j)
		}
		config += "\n"
	}
	return config
}

func generateScaledConfigWithChanges(backendCount, serversPerBackend int) string {
	config := `
global
    daemon

defaults
    mode http
    timeout connect 10000ms
    timeout client 50000ms
    timeout server 50000ms

frontend http_front
    bind *:80
    default_backend backend_0

`
	// Change balance method on half the backends, add servers to others
	for i := 0; i < backendCount; i++ {
		config += fmt.Sprintf("backend backend_%d\n", i)
		if i%2 == 0 {
			config += "    balance leastconn\n"
		} else {
			config += "    balance roundrobin\n"
		}
		serverCount := serversPerBackend
		if i%3 == 0 {
			serverCount++ // Add extra server to every 3rd backend
		}
		for j := 0; j < serverCount; j++ {
			weight := ""
			if j == 0 && i%2 == 0 {
				weight = " weight 100"
			}
			config += fmt.Sprintf("    server srv%d_%d 10.%d.%d.1:%d check%s\n", i, j, i/256, i%256, 8080+j, weight)
		}
		config += "\n"
	}
	return config
}

// mockOperation implements the Operation interface for benchmarking OrderOperations.
type mockOperation struct {
	opType   sections.OperationType
	section  string
	priority int
}

func (m *mockOperation) Type() sections.OperationType { return m.opType }
func (m *mockOperation) Section() string              { return m.section }
func (m *mockOperation) Priority() int                { return m.priority }
func (m *mockOperation) Describe() string             { return "mock operation" }

func (m *mockOperation) Execute(_ context.Context, _ *client.DataplaneClient, _ string) error {
	return nil
}

func generateMockOperations(count int) []Operation {
	ops := make([]Operation, count)
	types := []sections.OperationType{sections.OperationCreate, sections.OperationUpdate, sections.OperationDelete}

	for i := 0; i < count; i++ {
		ops[i] = &mockOperation{
			opType:   types[i%3],
			section:  fmt.Sprintf("section_%d", i%10),
			priority: i % 100,
		}
	}
	return ops
}
