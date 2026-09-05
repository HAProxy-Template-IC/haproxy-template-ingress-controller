// Copyright 2026 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package rendercontext

import (
	"context"
	"fmt"
	"maps"
	"testing"

	iradix "github.com/hashicorp/go-immutable-radix/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPreparedPlanSnapshotBulkMatchesSequentialDeclarations(t *testing.T) {
	profileHTTP, err := PreparePlanProfile(map[string]any{"mode": "http"})
	require.NoError(t, err)
	profileTCP, err := PreparePlanProfile(map[string]any{"mode": "tcp"})
	require.NoError(t, err)
	backendApp, err := PreparePlanBackend(map[string]any{
		"name": "be_app", "profile": profileHTTP.Name, "mode": "http",
	}, "backend be_app\n")
	require.NoError(t, err)
	backendStream, err := PreparePlanBackend(map[string]any{
		"name": "be_stream", "profile": profileTCP.Name, "mode": "tcp",
	}, "backend be_stream\n")
	require.NoError(t, err)

	profileOrders := [][]PreparedPlanProfile{
		{profileHTTP, profileTCP},
		{profileTCP, profileHTTP},
	}
	backendOrders := [][]*PreparedPlanBackend{
		{&backendApp, &backendStream},
		{&backendStream, &backendApp},
	}
	for profileOrder, profiles := range profileOrders {
		for backendOrder, backends := range backendOrders {
			t.Run(fmt.Sprintf("profiles-%d-backends-%d", profileOrder, backendOrder), func(t *testing.T) {
				bulk, buildErr := NewPreparedPlanSnapshotFromDeclarations(profiles, backends)
				require.NoError(t, buildErr)
				require.NoError(t, bulk.ValidateAuthentication())

				sequential := NewPreparedPlanSnapshot()
				for _, profile := range profiles {
					sequential, buildErr = sequential.WithProfile(profile)
					require.NoError(t, buildErr)
				}
				for _, backend := range backends {
					sequential, buildErr = sequential.WithBackend(backend)
					require.NoError(t, buildErr)
				}
				assertPreparedPlanSnapshotsEquivalent(t, sequential, bulk)
			})
		}
	}

	empty, err := NewPreparedPlanSnapshotFromDeclarations(nil, nil)
	require.NoError(t, err)
	require.NoError(t, empty.ValidateAuthentication())
	assert.Zero(t, empty.sections.Len())
	assert.Zero(t, empty.backends.Len())
}

func TestPreparedPlanSnapshotBulkOwnsDeclarationsAndSupportsReplacement(t *testing.T) {
	profile, err := PreparePlanProfile(map[string]any{"mode": "http"})
	require.NoError(t, err)
	backend, err := PreparePlanBackend(map[string]any{
		"name": "be_app",
		"servers": []any{map[string]any{
			"name":  "server-1",
			"extra": []any{map[string]any{"name": "check", "args": []any{"inter", "2s"}}},
		}},
		"body":     []any{"    timeout server 5s"},
		"comments": []any{"# route default/app"},
	}, "backend be_app\n")
	require.NoError(t, err)
	bulk, err := NewPreparedPlanSnapshotFromDeclarations(
		[]PreparedPlanProfile{profile}, []*PreparedPlanBackend{&backend},
	)
	require.NoError(t, err)

	profile.Body[0] = "poison"
	backend.Backend.Servers[0].Extra[0].Args[0] = "poison"
	backend.Body[0] = "poison"
	backend.Comments[0] = "poison"
	stored, exists, err := bulk.backend("be_app")
	require.NoError(t, err)
	require.True(t, exists)
	assert.Equal(t, []string{"inter", "2s"}, stored.Servers[0].Extra[0].Args)
	assert.Equal(t, []string{"    timeout server 5s"}, stored.Body)
	assert.Equal(t, []string{"# route default/app"}, stored.Comments)

	without, err := bulk.WithoutBackend("be_app")
	require.NoError(t, err)
	_, exists, err = without.backend("be_app")
	require.NoError(t, err)
	assert.False(t, exists)
	_, exists, err = bulk.backend("be_app")
	require.NoError(t, err)
	assert.True(t, exists)
}

func TestPreparedPlanSnapshotBulkRejectsLateInvalidDeclarations(t *testing.T) {
	profile, err := PreparePlanProfile(map[string]any{"mode": "http"})
	require.NoError(t, err)
	backend, err := PreparePlanBackend(map[string]any{"name": "be_app"}, "backend be_app\n")
	require.NoError(t, err)

	invalidProfile := profile.Clone()
	invalidProfile.Digest = "poison"
	snapshot, err := NewPreparedPlanSnapshotFromDeclarations(
		[]PreparedPlanProfile{profile, invalidProfile}, []*PreparedPlanBackend{&backend},
	)
	require.ErrorContains(t, err, "profile 1")
	assert.Nil(t, snapshot)
	require.NoError(t, profile.Validate())
	require.NoError(t, backend.Validate())

	invalidBackend := backend.Clone()
	invalidBackend.Digest = "poison"
	snapshot, err = NewPreparedPlanSnapshotFromDeclarations(
		[]PreparedPlanProfile{profile}, []*PreparedPlanBackend{&backend, &invalidBackend},
	)
	require.ErrorContains(t, err, "backend 1")
	assert.Nil(t, snapshot)
	require.NoError(t, profile.Validate())
	require.NoError(t, backend.Validate())
}

func TestPreparedPlanSnapshotBulkRejectsDuplicateAndMissingDeclarations(t *testing.T) {
	profile, err := PreparePlanProfile(map[string]any{"mode": "http"})
	require.NoError(t, err)
	backend, err := PreparePlanBackend(map[string]any{"name": "be_app"}, "backend be_app\n")
	require.NoError(t, err)

	tests := []struct {
		name     string
		profiles []PreparedPlanProfile
		backends []*PreparedPlanBackend
		want     string
	}{
		{name: "profile", profiles: []PreparedPlanProfile{profile, profile}, want: "repeats profile"},
		{name: "backend", backends: []*PreparedPlanBackend{&backend, &backend}, want: "repeats backend"},
		{name: "nil backend", backends: []*PreparedPlanBackend{&backend, nil}, want: "backend 1"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			snapshot, buildErr := NewPreparedPlanSnapshotFromDeclarations(test.profiles, test.backends)
			require.ErrorContains(t, buildErr, test.want)
			assert.Nil(t, snapshot)
		})
	}
}

func TestPreparedPlanSnapshotBulkRejectsEquivalentRootSubstitution(t *testing.T) {
	backend, err := PreparePlanBackend(map[string]any{"name": "be_app"}, "backend be_app\n")
	require.NoError(t, err)
	snapshot, err := NewPreparedPlanSnapshotFromDeclarations(nil, []*PreparedPlanBackend{&backend})
	require.NoError(t, err)

	poisoned := *snapshot
	poisoned.backends = clonePreparedPlanTree(snapshot.backends)
	require.ErrorContains(t, poisoned.ValidateAuthentication(), "authentication seal")
	require.NoError(t, snapshot.ValidateAuthentication())
}

func TestPreparedPlanSnapshotAttachesWithFreshTokens(t *testing.T) {
	profile, err := PreparePlanProfile(map[string]any{"mode": "http"})
	require.NoError(t, err)
	backend, err := PreparePlanBackend(map[string]any{
		"name": "be_app", "profile": profile.Name, "mode": "http",
	}, "backend be_app from "+profile.Name+"\n")
	require.NoError(t, err)
	snapshot := NewPreparedPlanSnapshot()
	snapshot, err = snapshot.WithProfile(profile)
	require.NoError(t, err)
	snapshot, err = snapshot.WithBackend(&backend)
	require.NoError(t, err)

	tokens := make([]string, 0, 2)
	configs := make([]string, 0, 2)
	for range 2 {
		registry := NewPlanRegistry(nil)
		require.NoError(t, registry.AttachPreparedPlan(snapshot))
		token, tokenErr := registry.PreparedBackendToken("be_app")
		require.NoError(t, tokenErr)
		config, _, assembleErr := registry.Assemble(
			context.Background(), registry.ProfileGroup()+token, nil,
		)
		require.NoError(t, assembleErr)
		tokens = append(tokens, token)
		configs = append(configs, config)
	}

	assert.NotEqual(t, tokens[0], tokens[1])
	assert.Equal(t, configs[0], configs[1])
	assert.Equal(t, profile.Text+backend.Text, configs[0])
}

func TestPreparedPlanSnapshotRejectsEquivalentRootSubstitution(t *testing.T) {
	profile, err := PreparePlanProfile(map[string]any{"mode": "http"})
	require.NoError(t, err)
	backend, err := PreparePlanBackend(map[string]any{"name": "be_app"}, "backend be_app\n")
	require.NoError(t, err)
	snapshot := NewPreparedPlanSnapshot()
	snapshot, err = snapshot.WithProfile(profile)
	require.NoError(t, err)
	snapshot, err = snapshot.WithBackend(&backend)
	require.NoError(t, err)

	tests := map[string]func(*PreparedPlanSnapshot){
		"sections": func(poisoned *PreparedPlanSnapshot) {
			poisoned.sections = clonePreparedPlanTree(snapshot.sections)
		},
		"backends": func(poisoned *PreparedPlanSnapshot) {
			poisoned.backends = clonePreparedPlanTree(snapshot.backends)
		},
	}
	for name, poison := range tests {
		t.Run(name, func(t *testing.T) {
			poisoned := *snapshot
			poison(&poisoned)
			require.ErrorContains(t, poisoned.ValidateAuthentication(), "authentication seal")
			require.ErrorContains(t, NewPlanRegistry(nil).AttachPreparedPlan(&poisoned), "authentication seal")
		})
	}
}

func TestPreparedPlanSnapshotOwnsTypedBackendState(t *testing.T) {
	backend, err := PreparePlanBackend(map[string]any{
		"name": "be_app",
		"servers": []any{map[string]any{
			"name":  "server-1",
			"extra": []any{map[string]any{"name": "check", "args": []any{"inter", "2s"}}},
		}},
		"body":     []any{"    timeout server 5s"},
		"comments": []any{"# route default/app"},
	}, "backend be_app\n")
	require.NoError(t, err)
	snapshot, err := NewPreparedPlanSnapshot().WithBackend(&backend)
	require.NoError(t, err)

	backend.Backend.Servers[0].Extra[0].Args[0] = "poison"
	backend.Body[0] = "poison"
	backend.Comments[0] = "poison"

	first, exists, err := snapshot.backend("be_app")
	require.NoError(t, err)
	require.True(t, exists)
	assert.Equal(t, []string{"inter", "2s"}, first.Servers[0].Extra[0].Args)
	assert.Equal(t, []string{"    timeout server 5s"}, first.Body)
	assert.Equal(t, []string{"# route default/app"}, first.Comments)

	first.Servers[0].Extra[0].Args[0] = "poison"
	first.Body[0] = "poison"
	first.Comments[0] = "poison"
	second, exists, err := snapshot.backend("be_app")
	require.NoError(t, err)
	require.True(t, exists)
	assert.Equal(t, []string{"inter", "2s"}, second.Servers[0].Extra[0].Args)
	assert.Equal(t, []string{"    timeout server 5s"}, second.Body)
	assert.Equal(t, []string{"# route default/app"}, second.Comments)
}

func TestPreparedPlanSnapshotConflictsWithRenderDeclarations(t *testing.T) {
	backend, err := PreparePlanBackend(map[string]any{
		"name": "be_app", "mode": "http",
	}, "backend be_app\n    mode http\n")
	require.NoError(t, err)
	snapshot, err := NewPreparedPlanSnapshot().WithBackend(&backend)
	require.NoError(t, err)

	before := NewPlanRegistry(nil)
	_, err = before.Section("backend", "be_app", "backend be_app\n    mode tcp\n")
	require.NoError(t, err)
	require.ErrorContains(t, before.AttachPreparedPlan(snapshot), "different text")

	after := NewPlanRegistry(nil)
	require.NoError(t, after.AttachPreparedPlan(snapshot))
	_, err = after.Section("backend", "be_app", "backend be_app\n    mode tcp\n")
	require.ErrorContains(t, err, "different text")
}

func clonePreparedPlanTree[T any](source *iradix.Tree[T]) *iradix.Tree[T] {
	clone := iradix.New[T]()
	source.Root().Walk(func(key []byte, value T) bool {
		clone, _, _ = clone.Insert(key, value)
		return false
	})
	return clone
}

func assertPreparedPlanSnapshotsEquivalent(
	t *testing.T,
	want, got *PreparedPlanSnapshot,
) {
	t.Helper()
	require.NoError(t, want.ValidateAuthentication())
	require.NoError(t, got.ValidateAuthentication())
	wantSections := make(map[string]string, want.sections.Len())
	want.sections.Root().Walk(func(key []byte, value string) bool {
		wantSections[string(key)] = value
		return false
	})
	gotSections := make(map[string]string, got.sections.Len())
	got.sections.Root().Walk(func(key []byte, value string) bool {
		gotSections[string(key)] = value
		return false
	})
	assert.True(t, maps.Equal(wantSections, gotSections))
	assert.Equal(t, want.backends.Len(), got.backends.Len())
	want.backends.Root().Walk(func(key []byte, value PreparedPlanBackend) bool {
		actual, exists := got.backends.Root().Get(key)
		assert.True(t, exists)
		assert.Equal(t, value, actual)
		return false
	})
}
