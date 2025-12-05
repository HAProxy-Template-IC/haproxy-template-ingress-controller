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
	"testing"
)

// Package-level result sinks prevent compiler dead code elimination.
// These variables ensure the benchmark results are actually used.
var (
	benchResultString string
	benchResultErr    error
)

// BenchmarkEngine_Render benchmarks template rendering with various configurations.
// Run with: go test -bench=BenchmarkEngine_Render -benchmem -count=6.
func BenchmarkEngine_Render(b *testing.B) {
	b.Run("size=small", func(b *testing.B) {
		benchmarkRenderSimple(b)
	})

	b.Run("size=medium", func(b *testing.B) {
		benchmarkRenderMedium(b)
	})

	b.Run("size=large", func(b *testing.B) {
		benchmarkRenderLarge(b)
	})
}

// BenchmarkEngine_Render_Filters benchmarks template rendering with custom filters.
func BenchmarkEngine_Render_Filters(b *testing.B) {
	b.Run("filter=sort_by", func(b *testing.B) {
		benchmarkFilterSortBy(b)
	})

	b.Run("filter=group_by", func(b *testing.B) {
		benchmarkFilterGroupBy(b)
	})

	b.Run("filter=extract", func(b *testing.B) {
		benchmarkFilterExtract(b)
	})

	b.Run("filter=transform", func(b *testing.B) {
		benchmarkFilterTransform(b)
	})
}

// BenchmarkEngine_Render_Scale benchmarks rendering with varying data sizes.
func BenchmarkEngine_Render_Scale(b *testing.B) {
	for _, itemCount := range []int{10, 100, 1000} {
		b.Run(fmt.Sprintf("items=%d", itemCount), func(b *testing.B) {
			benchmarkRenderScale(b, itemCount)
		})
	}
}

// BenchmarkEngine_Compile benchmarks template compilation.
func BenchmarkEngine_Compile(b *testing.B) {
	b.Run("size=small", func(b *testing.B) {
		benchmarkCompileSmall(b)
	})

	b.Run("size=medium", func(b *testing.B) {
		benchmarkCompileMedium(b)
	})

	b.Run("size=large", func(b *testing.B) {
		benchmarkCompileLarge(b)
	})
}

// benchmarkRenderSimple benchmarks simple variable substitution.
func benchmarkRenderSimple(b *testing.B) {
	b.Helper()
	templates := map[string]string{
		"simple": "Hello {{ name }}! Welcome to {{ location }}.",
	}

	engine, err := New(EngineTypeGonja, templates, nil, nil, nil)
	if err != nil {
		b.Fatalf("failed to create engine: %v", err)
	}

	ctx := map[string]interface{}{
		"name":     "World",
		"location": "HAProxy",
	}

	b.ResetTimer()
	b.ReportAllocs()

	var r string
	for i := 0; i < b.N; i++ {
		r, _ = engine.Render("simple", ctx)
	}
	benchResultString = r
}

// benchmarkRenderMedium benchmarks templates with loops and conditionals.
func benchmarkRenderMedium(b *testing.B) {
	b.Helper()
	templates := map[string]string{
		"medium": `
{%- for server in servers %}
server {{ server.name }} {{ server.address }}:{{ server.port }}
{%- if server.weight %} weight {{ server.weight }}{% endif %}
{%- if server.check %} check{% endif %}
{%- endfor %}
`,
	}

	engine, err := New(EngineTypeGonja, templates, nil, nil, nil)
	if err != nil {
		b.Fatalf("failed to create engine: %v", err)
	}

	ctx := map[string]interface{}{
		"servers": []map[string]interface{}{
			{"name": "srv1", "address": "192.168.1.1", "port": 8080, "weight": 100, "check": true},
			{"name": "srv2", "address": "192.168.1.2", "port": 8080, "weight": 50, "check": true},
			{"name": "srv3", "address": "192.168.1.3", "port": 8080, "weight": 25, "check": false},
			{"name": "srv4", "address": "192.168.1.4", "port": 8080, "check": true},
			{"name": "srv5", "address": "192.168.1.5", "port": 8080, "weight": 75, "check": true},
		},
	}

	b.ResetTimer()
	b.ReportAllocs()

	var r string
	for i := 0; i < b.N; i++ {
		r, _ = engine.Render("medium", ctx)
	}
	benchResultString = r
}

// benchmarkRenderLarge benchmarks complex templates with nested loops and includes.
func benchmarkRenderLarge(b *testing.B) {
	b.Helper()
	templates := map[string]string{
		"large": `
{%- include "globals" %}
{%- include "defaults" %}
{%- for frontend in frontends %}
{%- include "frontend" %}
{%- endfor %}
{%- for backend in backends %}
{%- include "backend" %}
{%- endfor %}
`,
		"globals": `
global
    maxconn {{ global.maxconn | default(4096) }}
    log stdout format raw local0
`,
		"defaults": `
defaults
    mode http
    timeout connect {{ defaults.connect_timeout | default("5s") }}
    timeout client {{ defaults.client_timeout | default("50s") }}
    timeout server {{ defaults.server_timeout | default("50s") }}
`,
		"frontend": `
frontend {{ frontend.name }}
    bind *:{{ frontend.port }}
{%- for acl in frontend.acls %}
    acl {{ acl.name }} {{ acl.condition }}
{%- endfor %}
{%- for rule in frontend.rules %}
    use_backend {{ rule.backend }} if {{ rule.condition }}
{%- endfor %}
    default_backend {{ frontend.default_backend }}
`,
		"backend": `
backend {{ backend.name }}
    balance {{ backend.balance | default("roundrobin") }}
{%- for server in backend.servers %}
    server {{ server.name }} {{ server.address }}:{{ server.port }}{% if server.weight %} weight {{ server.weight }}{% endif %}{% if server.check %} check{% endif %}

{%- endfor %}
`,
	}

	engine, err := New(EngineTypeGonja, templates, nil, nil, nil)
	if err != nil {
		b.Fatalf("failed to create engine: %v", err)
	}

	ctx := map[string]interface{}{
		"global": map[string]interface{}{
			"maxconn": 10000,
		},
		"defaults": map[string]interface{}{
			"connect_timeout": "10s",
			"client_timeout":  "30s",
			"server_timeout":  "30s",
		},
		"frontends": []map[string]interface{}{
			{
				"name": "http_front",
				"port": 80,
				"acls": []map[string]interface{}{
					{"name": "is_api", "condition": "path_beg /api"},
					{"name": "is_static", "condition": "path_beg /static"},
				},
				"rules": []map[string]interface{}{
					{"backend": "api_backend", "condition": "is_api"},
					{"backend": "static_backend", "condition": "is_static"},
				},
				"default_backend": "main_backend",
			},
			{
				"name": "https_front",
				"port": 443,
				"acls": []map[string]interface{}{
					{"name": "is_admin", "condition": "path_beg /admin"},
				},
				"rules": []map[string]interface{}{
					{"backend": "admin_backend", "condition": "is_admin"},
				},
				"default_backend": "main_backend",
			},
		},
		"backends": []map[string]interface{}{
			{
				"name":    "api_backend",
				"balance": "leastconn",
				"servers": []map[string]interface{}{
					{"name": "api1", "address": "10.0.0.1", "port": 8080, "weight": 100, "check": true},
					{"name": "api2", "address": "10.0.0.2", "port": 8080, "weight": 100, "check": true},
				},
			},
			{
				"name": "main_backend",
				"servers": []map[string]interface{}{
					{"name": "main1", "address": "10.0.1.1", "port": 8080, "check": true},
					{"name": "main2", "address": "10.0.1.2", "port": 8080, "check": true},
					{"name": "main3", "address": "10.0.1.3", "port": 8080, "check": true},
				},
			},
			{
				"name":    "static_backend",
				"balance": "roundrobin",
				"servers": []map[string]interface{}{
					{"name": "static1", "address": "10.0.2.1", "port": 80},
				},
			},
			{
				"name": "admin_backend",
				"servers": []map[string]interface{}{
					{"name": "admin1", "address": "10.0.3.1", "port": 9000, "check": true},
				},
			},
		},
	}

	b.ResetTimer()
	b.ReportAllocs()

	var r string
	for i := 0; i < b.N; i++ {
		r, _ = engine.Render("large", ctx)
	}
	benchResultString = r
}

// benchmarkFilterSortBy benchmarks the sort_by filter.
func benchmarkFilterSortBy(b *testing.B) {
	b.Helper()
	templates := map[string]string{
		"sort": `
{%- set sorted = items | sort_by(["$.priority:desc", "$.name"]) %}
{%- for item in sorted %}
{{ item.name }}: {{ item.priority }}
{%- endfor %}
`,
	}

	engine, err := New(EngineTypeGonja, templates, nil, nil, nil)
	if err != nil {
		b.Fatalf("failed to create engine: %v", err)
	}

	ctx := map[string]interface{}{
		"items": []map[string]interface{}{
			{"name": "alpha", "priority": 1},
			{"name": "beta", "priority": 3},
			{"name": "gamma", "priority": 2},
			{"name": "delta", "priority": 3},
			{"name": "epsilon", "priority": 1},
			{"name": "zeta", "priority": 2},
			{"name": "eta", "priority": 4},
			{"name": "theta", "priority": 1},
		},
	}

	b.ResetTimer()
	b.ReportAllocs()

	var r string
	for i := 0; i < b.N; i++ {
		r, _ = engine.Render("sort", ctx)
	}
	benchResultString = r
}

// benchmarkFilterGroupBy benchmarks the group_by filter.
func benchmarkFilterGroupBy(b *testing.B) {
	b.Helper()
	templates := map[string]string{
		"group": `
{%- set grouped = items | group_by("$.category") %}
{%- if "A" in grouped %}Category A: {{ grouped["A"] | length }} items{% endif %}
{%- if "B" in grouped %}
Category B: {{ grouped["B"] | length }} items{% endif %}
{%- if "C" in grouped %}
Category C: {{ grouped["C"] | length }} items{% endif %}
`,
	}

	engine, err := New(EngineTypeGonja, templates, nil, nil, nil)
	if err != nil {
		b.Fatalf("failed to create engine: %v", err)
	}

	ctx := map[string]interface{}{
		"items": []map[string]interface{}{
			{"name": "item1", "category": "A"},
			{"name": "item2", "category": "B"},
			{"name": "item3", "category": "A"},
			{"name": "item4", "category": "C"},
			{"name": "item5", "category": "B"},
			{"name": "item6", "category": "A"},
			{"name": "item7", "category": "C"},
			{"name": "item8", "category": "B"},
		},
	}

	b.ResetTimer()
	b.ReportAllocs()

	var r string
	for i := 0; i < b.N; i++ {
		r, _ = engine.Render("group", ctx)
	}
	benchResultString = r
}

// benchmarkFilterExtract benchmarks the extract filter.
func benchmarkFilterExtract(b *testing.B) {
	b.Helper()
	templates := map[string]string{
		"extract": `
{%- set names = items | extract("$.metadata.name") %}
{%- for name in names %}
{{ name }}
{%- endfor %}
`,
	}

	engine, err := New(EngineTypeGonja, templates, nil, nil, nil)
	if err != nil {
		b.Fatalf("failed to create engine: %v", err)
	}

	ctx := map[string]interface{}{
		"items": []map[string]interface{}{
			{"metadata": map[string]interface{}{"name": "resource1", "namespace": "default"}},
			{"metadata": map[string]interface{}{"name": "resource2", "namespace": "kube-system"}},
			{"metadata": map[string]interface{}{"name": "resource3", "namespace": "default"}},
			{"metadata": map[string]interface{}{"name": "resource4", "namespace": "production"}},
			{"metadata": map[string]interface{}{"name": "resource5", "namespace": "default"}},
		},
	}

	b.ResetTimer()
	b.ReportAllocs()

	var r string
	for i := 0; i < b.N; i++ {
		r, _ = engine.Render("extract", ctx)
	}
	benchResultString = r
}

// benchmarkFilterTransform benchmarks the transform filter.
func benchmarkFilterTransform(b *testing.B) {
	b.Helper()
	templates := map[string]string{
		"transform": `
{%- set transformed = items | transform("{ name: $.metadata.name, ns: $.metadata.namespace }") %}
{%- for item in transformed %}
{{ item.name }} in {{ item.ns }}
{%- endfor %}
`,
	}

	engine, err := New(EngineTypeGonja, templates, nil, nil, nil)
	if err != nil {
		b.Fatalf("failed to create engine: %v", err)
	}

	ctx := map[string]interface{}{
		"items": []map[string]interface{}{
			{"metadata": map[string]interface{}{"name": "resource1", "namespace": "default"}},
			{"metadata": map[string]interface{}{"name": "resource2", "namespace": "kube-system"}},
			{"metadata": map[string]interface{}{"name": "resource3", "namespace": "default"}},
			{"metadata": map[string]interface{}{"name": "resource4", "namespace": "production"}},
			{"metadata": map[string]interface{}{"name": "resource5", "namespace": "default"}},
		},
	}

	b.ResetTimer()
	b.ReportAllocs()

	var r string
	for i := 0; i < b.N; i++ {
		r, _ = engine.Render("transform", ctx)
	}
	benchResultString = r
}

// benchmarkRenderScale benchmarks rendering with different data sizes.
func benchmarkRenderScale(b *testing.B, itemCount int) {
	b.Helper()
	templates := map[string]string{
		"scale": `
{%- for server in servers %}
server {{ server.name }} {{ server.address }}:{{ server.port }} check
{%- endfor %}
`,
	}

	engine, err := New(EngineTypeGonja, templates, nil, nil, nil)
	if err != nil {
		b.Fatalf("failed to create engine: %v", err)
	}

	// Generate test data
	servers := make([]map[string]interface{}, itemCount)
	for i := 0; i < itemCount; i++ {
		servers[i] = map[string]interface{}{
			"name":    fmt.Sprintf("srv%d", i),
			"address": fmt.Sprintf("10.0.%d.%d", i/256, i%256),
			"port":    8080 + (i % 10),
		}
	}

	ctx := map[string]interface{}{
		"servers": servers,
	}

	b.ResetTimer()
	b.ReportAllocs()

	var r string
	for i := 0; i < b.N; i++ {
		r, _ = engine.Render("scale", ctx)
	}
	benchResultString = r
}

// benchmarkCompileSmall benchmarks compiling a small template.
func benchmarkCompileSmall(b *testing.B) {
	b.Helper()
	templates := map[string]string{
		"small": "Hello {{ name }}!",
	}

	b.ResetTimer()
	b.ReportAllocs()

	var engine *TemplateEngine
	var err error
	for i := 0; i < b.N; i++ {
		engine, err = New(EngineTypeGonja, templates, nil, nil, nil)
		if err != nil {
			b.Fatalf("failed to create engine: %v", err)
		}
	}
	_ = engine
	benchResultErr = err
}

// benchmarkCompileMedium benchmarks compiling a medium-sized template.
func benchmarkCompileMedium(b *testing.B) {
	b.Helper()
	templates := map[string]string{
		"medium": `
{%- for server in servers %}
server {{ server.name }} {{ server.address }}:{{ server.port }}
{%- if server.weight %} weight {{ server.weight }}{% endif %}
{%- if server.check %} check{% endif %}
{%- endfor %}
`,
	}

	b.ResetTimer()
	b.ReportAllocs()

	var engine *TemplateEngine
	var err error
	for i := 0; i < b.N; i++ {
		engine, err = New(EngineTypeGonja, templates, nil, nil, nil)
		if err != nil {
			b.Fatalf("failed to create engine: %v", err)
		}
	}
	_ = engine
	benchResultErr = err
}

// benchmarkCompileLarge benchmarks compiling multiple templates.
func benchmarkCompileLarge(b *testing.B) {
	b.Helper()
	templates := map[string]string{
		"main": `
{%- include "globals" %}
{%- include "defaults" %}
{%- for frontend in frontends %}
{%- include "frontend" %}
{%- endfor %}
`,
		"globals": `
global
    maxconn {{ global.maxconn | default(4096) }}
    log stdout format raw local0
`,
		"defaults": `
defaults
    mode http
    timeout connect {{ defaults.connect_timeout | default("5s") }}
    timeout client {{ defaults.client_timeout | default("50s") }}
    timeout server {{ defaults.server_timeout | default("50s") }}
`,
		"frontend": `
frontend {{ frontend.name }}
    bind *:{{ frontend.port }}
{%- for acl in frontend.acls %}
    acl {{ acl.name }} {{ acl.condition }}
{%- endfor %}
{%- for rule in frontend.rules %}
    use_backend {{ rule.backend }} if {{ rule.condition }}
{%- endfor %}
    default_backend {{ frontend.default_backend }}
`,
	}

	b.ResetTimer()
	b.ReportAllocs()

	var engine *TemplateEngine
	var err error
	for i := 0; i < b.N; i++ {
		engine, err = New(EngineTypeGonja, templates, nil, nil, nil)
		if err != nil {
			b.Fatalf("failed to create engine: %v", err)
		}
	}
	_ = engine
	benchResultErr = err
}
