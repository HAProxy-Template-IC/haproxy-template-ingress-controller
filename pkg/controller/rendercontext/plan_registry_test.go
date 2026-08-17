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

package rendercontext

import (
	"context"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/renderplan"
)

func TestPlanRegistrySection(t *testing.T) {
	tests := []struct {
		name    string
		kind    string
		section string
		wantErr string
	}{
		{name: "backend", kind: "backend", section: "be_app"},
		{name: "profile", kind: "profile", section: "haptic-be-0f1e"},
		{name: "name with dots and colons", kind: "backend", section: "ns:svc.local-1"},
		{name: "unknown kind", kind: "frontend", section: "fe", wantErr: `kind must be "profile" or "backend"`},
		{name: "core is not registrable", kind: "core", section: "x", wantErr: "kind must be"},
		{name: "empty name", kind: "backend", section: "", wantErr: "must match"},
		{name: "name with space", kind: "backend", section: "be app", wantErr: "must match"},
		{name: "name with token terminator", kind: "backend", section: "be@", wantErr: "must match"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			registry := NewPlanRegistry()

			token, err := registry.Section(tc.kind, tc.section, "text\n")

			if tc.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.wantErr)
				assert.Empty(t, token)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, "# @haptic:"+registry.nonce+":section:"+tc.kind+":"+tc.section+"@\n", token)
		})
	}
}

func TestPlanRegistrySectionIdempotent(t *testing.T) {
	registry := NewPlanRegistry()

	first, err := registry.Section("profile", "shared", "defaults shared\n")
	require.NoError(t, err)
	second, err := registry.Section("profile", "shared", "defaults shared\n")
	require.NoError(t, err)
	assert.Equal(t, first, second, "re-registering identical text is a no-op")

	_, err = registry.Section("profile", "shared", "defaults shared\n    timeout connect 5s\n")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "registered twice with different text")
}

func TestPlanRegistrySectionConcurrent(t *testing.T) {
	registry := NewPlanRegistry()

	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := registry.Section("profile", "shared", "defaults shared\n")
			assert.NoError(t, err)
		}()
	}
	wg.Wait()

	assert.Len(t, registry.sections, 1)
}

func TestPlanRegistryBackendStrictKeys(t *testing.T) {
	tests := []struct {
		name    string
		record  map[string]any
		wantErr string
	}{
		{name: "minimal record", record: map[string]any{"name": "be_app"}},
		{
			name: "full record",
			record: map[string]any{
				"name": "be_app", "mode": "http", "guid": "be:app", "balance": "roundrobin",
				"hashType": "consistent", "shape": "dynamic", "shapeReason": "",
				"servers": []any{map[string]any{
					"name": "SRV_1", "address": "10.0.0.1", "port": 8080, "weight": 128,
					"disabled": false, "guid": "srv:1", "comment": "pod-a",
					"extra": []any{map[string]any{"name": "check", "args": []any{"inter", "2s"}}},
				}},
				"defaultServer": []any{map[string]any{"name": "check-sni", "args": []any{"example.com"}}},
				"body":          []any{"    stick-table type ip size 1m"},
				"comments":      []any{"# from Ingress default/app"},
			},
		},
		{name: "missing name", record: map[string]any{"mode": "http"}, wantErr: `"name" is required`},
		{
			name:    "unknown key suggests the nearest",
			record:  map[string]any{"name": "be_app", "bodyy": []any{"x"}},
			wantErr: `unknown key "bodyy" (did you mean "body"?)`,
		},
		{
			name:    "unrelated key lists the valid ones",
			record:  map[string]any{"name": "be_app", "frontendish": true},
			wantErr: "valid keys are name, mode, guid",
		},
		{
			name:    "wrong type for name",
			record:  map[string]any{"name": 7},
			wantErr: `"name" must be a string, got int`,
		},
		{
			name:    "body must be strings",
			record:  map[string]any{"name": "be_app", "body": []any{7}},
			wantErr: `"body" must contain strings, got int`,
		},
		{
			name:    "unknown mode",
			record:  map[string]any{"name": "be_app", "mode": "htp"},
			wantErr: `mode "htp", want one of http, tcp, spop`,
		},
		{
			name:    "unknown shape",
			record:  map[string]any{"name": "be_app", "shape": "elastic"},
			wantErr: `shape "elastic", want one of dynamic, structural`,
		},
		{
			name:    "unknown server key",
			record:  map[string]any{"name": "be_app", "servers": []any{map[string]any{"name": "s", "pot": 8080}}},
			wantErr: `unknown key "pot" (did you mean "port"?)`,
		},
		{
			name:    "server port must be a number",
			record:  map[string]any{"name": "be_app", "servers": []any{map[string]any{"name": "s", "port": "8080"}}},
			wantErr: `"port" must be a number, got string`,
		},
		{
			name:    "server without a name",
			record:  map[string]any{"name": "be_app", "servers": []any{map[string]any{"address": "10.0.0.1"}}},
			wantErr: "server without a name",
		},
		{
			name:    "keyword without a name",
			record:  map[string]any{"name": "be_app", "defaultServer": []any{map[string]any{"args": []any{"x"}}}},
			wantErr: `"defaultServer" needs a keyword name`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			registry := NewPlanRegistry()

			token, err := registry.Backend(tc.record, "backend be_app\n")

			if tc.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.wantErr)
				assert.Contains(t, err.Error(), "planRegistry.Backend")
				assert.Empty(t, token)
				return
			}
			require.NoError(t, err)
			assert.Contains(t, token, ":section:backend:be_app@")
			assert.Len(t, registry.backends, 1)
		})
	}
}

func TestPlanRegistryBackendDefaultsAndDigests(t *testing.T) {
	registry := NewPlanRegistry()

	_, err := registry.Backend(map[string]any{
		"name":     "be_app",
		"body":     []string{"    stick-table type ip size 1m"},
		"comments": []string{"# from Ingress default/app"},
	}, "backend be_app\n")
	require.NoError(t, err)

	backend := registry.backends["be_app"]
	assert.Equal(t, renderplan.ShapeStructural, backend.Shape, "shape defaults to structural")
	assert.Empty(t, backend.Mode, "an undeclared mode stays undeclared rather than guessed")
	assert.Equal(t, renderplan.DigestString("    stick-table type ip size 1m"), backend.BodyDigest)
	assert.Equal(t, renderplan.DigestString("# from Ingress default/app"), backend.CommentsDigest)
	assert.Len(t, backend.RecordDigest, 16)
	assert.Empty(t, backend.TextDigest, "the text digest only exists once the section is assembled")
}

func TestPlanRegistryBackendConflict(t *testing.T) {
	registry := NewPlanRegistry()
	record := map[string]any{"name": "be_app", "mode": "http"}
	_, err := registry.Backend(record, "backend be_app\n")
	require.NoError(t, err)

	_, err = registry.Backend(map[string]any{"name": "be_app", "mode": "tcp"}, "backend be_app\n")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "declared twice with different values")

	_, err = registry.Backend(record, "backend be_app\n    server s1 10.0.0.1:80\n")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "registered twice with different text")
}

func TestPlanRegistryMapMeta(t *testing.T) {
	registry := NewPlanRegistry()

	require.NoError(t, registry.MapMeta("host.map", false))
	require.NoError(t, registry.MapMeta("host.map", false))

	err := registry.MapMeta("host.map", true)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "both ordered and unordered")

	require.Error(t, registry.MapMeta("", true))
}

func TestPlanRegistryPlan(t *testing.T) {
	registry := NewPlanRegistry()
	profileToken, err := registry.Section("profile", "haptic-be-1", "defaults haptic-be-1\n")
	require.NoError(t, err)
	backendToken, err := registry.Backend(map[string]any{
		"name":    "be_app",
		"servers": []any{map[string]any{"name": "SRV_1", "address": "10.0.0.1", "port": 8080}},
	}, "backend be_app\n    server SRV_1 10.0.0.1:8080\n")
	require.NoError(t, err)
	require.NoError(t, registry.MapMeta("host.map", false))

	rendered := "global\n    daemon\n" + profileToken + backendToken
	config, _, err := registry.Assemble(context.Background(), rendered, nil)
	require.NoError(t, err)

	files := []renderplan.File{
		{Path: "maps/host.map", Kind: renderplan.FileKindMap},
		{Path: "haproxy.cfg", Kind: renderplan.FileKindConfig},
	}
	plan := registry.Plan(files, map[string]string{
		"host.map":  "example.com be_app\nexample.com be_app2\n",
		"other.map": "# empty\n",
	})

	assert.Equal(t, renderplan.SchemaVersion, plan.SchemaVersion)
	assert.Len(t, plan.ID, 16)
	assert.Equal(t, []string{"haproxy.cfg", "maps/host.map"}, []string{plan.Files[0].Path, plan.Files[1].Path})

	require.Contains(t, plan.Profiles, "haptic-be-1")
	assert.Equal(t, renderplan.DigestString("defaults haptic-be-1\n"), plan.Profiles["haptic-be-1"].BodyDigest)

	require.Contains(t, plan.Backends, "be_app")
	assert.Equal(t, renderplan.DigestString("backend be_app\n    server SRV_1 10.0.0.1:8080\n"),
		plan.Backends["be_app"].TextDigest)

	assert.False(t, plan.Maps["host.map"].Ordered, "declared unordered")
	assert.True(t, plan.Maps["other.map"].Ordered, "maps are ordered unless declared otherwise")
	assert.Len(t, plan.Maps["host.map"].Entries, 2, "duplicate keys are kept")
	assert.Empty(t, plan.Maps["other.map"].Entries)

	assert.Equal(t, "global\n    daemon\ndefaults haptic-be-1\nbackend be_app\n    server SRV_1 10.0.0.1:8080\n", config)
}

func TestPlanRegistryPlanCurrentConfigRoundTrip(t *testing.T) {
	registry := NewPlanRegistry()
	token, err := registry.Backend(map[string]any{
		"name":    "be_app",
		"servers": []any{map[string]any{"name": "SRV_1", "address": "10.0.0.1", "port": 8080}},
	}, "backend be_app\n")
	require.NoError(t, err)
	_, _, err = registry.Assemble(context.Background(), token, nil)
	require.NoError(t, err)

	current := registry.Plan(nil, nil).CurrentConfig()

	require.Contains(t, current.ServerIndex, "be_app")
	server := current.ServerIndex["be_app"]["SRV_1"]
	assert.Equal(t, "10.0.0.1", server.Address)
	require.NotNil(t, server.Port)
	assert.Equal(t, int64(8080), *server.Port)
}

func TestNearestKey(t *testing.T) {
	tests := []struct {
		name    string
		unknown string
		want    string
	}{
		{name: "typo", unknown: "bodyy", want: "body"},
		{name: "case", unknown: "Servers", want: "servers"},
		{name: "transposition", unknown: "hasType", want: "hashType"},
		{name: "nothing close", unknown: "frontendish", want: ""},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, nearestKey(tc.unknown, backendRecordKeys))
		})
	}
}
