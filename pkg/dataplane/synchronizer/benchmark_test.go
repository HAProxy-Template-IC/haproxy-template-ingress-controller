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

package synchronizer

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"testing"

	"haptic/pkg/dataplane/parser"
)

// Package-level result sink prevents compiler dead code elimination.
var benchResultSync *SyncResult

// TestMain sets up a discard logger for benchmarks to avoid noisy output.
func TestMain(m *testing.M) {
	// Save original logger
	origLogger := slog.Default()

	// Set up discard logger for benchmarks
	discardHandler := slog.NewTextHandler(io.Discard, nil)
	slog.SetDefault(slog.New(discardHandler))

	// Run tests
	code := m.Run()

	// Restore original logger
	slog.SetDefault(origLogger)

	os.Exit(code)
}

// BenchmarkSync_DryRun benchmarks synchronization in dry-run mode.
// This tests the comparison and operation planning phase without actually
// executing operations against HAProxy.
// Run with: go test -bench=BenchmarkSync -benchmem -count=6.
func BenchmarkSync_DryRun(b *testing.B) {
	b.Run("size=small", func(b *testing.B) {
		benchmarkSyncDryRunSmall(b)
	})

	b.Run("size=medium", func(b *testing.B) {
		benchmarkSyncDryRunMedium(b)
	})

	b.Run("size=large", func(b *testing.B) {
		benchmarkSyncDryRunLarge(b)
	})
}

// BenchmarkSync_NoChanges benchmarks synchronization when no changes are needed.
func BenchmarkSync_NoChanges(b *testing.B) {
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

	// Create synchronizer without client (not needed for no-changes)
	sync := New(nil)
	opts := DefaultSyncOptions()

	b.ResetTimer()
	b.ReportAllocs()

	var r *SyncResult
	for i := 0; i < b.N; i++ {
		r, _ = sync.Sync(context.Background(), current, desired, opts)
	}
	benchResultSync = r
}

// BenchmarkSync_Scale benchmarks sync dry-run with varying backend counts.
func BenchmarkSync_Scale(b *testing.B) {
	for _, backendCount := range []int{10, 50, 100} {
		b.Run(fmt.Sprintf("backends=%d", backendCount), func(b *testing.B) {
			benchmarkSyncDryRunScale(b, backendCount)
		})
	}
}

// benchmarkSyncDryRunSmall benchmarks dry-run with small config.
func benchmarkSyncDryRunSmall(b *testing.B) {
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

	sync := New(nil)
	opts := DryRunOptions()

	b.ResetTimer()
	b.ReportAllocs()

	var r *SyncResult
	for i := 0; i < b.N; i++ {
		r, _ = sync.Sync(context.Background(), current, desired, opts)
	}
	benchResultSync = r
}

// benchmarkSyncDryRunMedium benchmarks dry-run with medium config.
func benchmarkSyncDryRunMedium(b *testing.B) {
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

	sync := New(nil)
	opts := DryRunOptions()

	b.ResetTimer()
	b.ReportAllocs()

	var r *SyncResult
	for i := 0; i < b.N; i++ {
		r, _ = sync.Sync(context.Background(), current, desired, opts)
	}
	benchResultSync = r
}

// benchmarkSyncDryRunLarge benchmarks dry-run with large config.
func benchmarkSyncDryRunLarge(b *testing.B) {
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

	sync := New(nil)
	opts := DryRunOptions()

	b.ResetTimer()
	b.ReportAllocs()

	var r *SyncResult
	for i := 0; i < b.N; i++ {
		r, _ = sync.Sync(context.Background(), current, desired, opts)
	}
	benchResultSync = r
}

// benchmarkSyncDryRunScale benchmarks dry-run with varying backend counts.
func benchmarkSyncDryRunScale(b *testing.B, backendCount int) {
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

	sync := New(nil)
	opts := DryRunOptions()

	b.ResetTimer()
	b.ReportAllocs()

	var r *SyncResult
	for i := 0; i < b.N; i++ {
		r, _ = sync.Sync(context.Background(), current, desired, opts)
	}
	benchResultSync = r
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
