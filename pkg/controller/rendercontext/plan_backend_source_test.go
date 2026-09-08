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
	"fmt"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/renderplan"
)

func preparedBackendFixture(tb testing.TB, name, mode string) *PreparedPlanBackend {
	tb.Helper()
	backend, err := PreparePlanBackend(map[string]any{
		"name": name, "mode": mode, "body": []any{"    timeout server 5s"},
	}, "backend "+name+"\n")
	require.NoError(tb, err)
	return &backend
}

func TestPreparedPlanSnapshotLogsBackendChangesBetweenTwoStates(t *testing.T) {
	base := NewPreparedPlanSnapshot()
	names, ok := base.backendsChangedSince(base.log)
	require.True(t, ok)
	assert.Empty(t, names)

	withApp, err := base.WithBackend(preparedBackendFixture(t, "be_app", "http"))
	require.NoError(t, err)
	profile, err := PreparePlanProfile(map[string]any{"mode": "http"})
	require.NoError(t, err)
	withProfile, err := withApp.WithProfile(profile)
	require.NoError(t, err)
	assert.Same(t, withApp.log, withProfile.log, "a profile step touches no backend")
	withStream, err := withProfile.WithBackend(preparedBackendFixture(t, "be_stream", "tcp"))
	require.NoError(t, err)
	withoutApp, err := withStream.WithoutBackend("be_app")
	require.NoError(t, err)
	unchanged, err := withoutApp.WithoutBackend("be_missing")
	require.NoError(t, err)
	assert.Same(t, withoutApp, unchanged)

	names, ok = withoutApp.backendsChangedSince(base.log)
	require.True(t, ok)
	slices.Sort(names)
	assert.Equal(t, []string{"be_app", "be_app", "be_stream"}, names)
	names, ok = withoutApp.backendsChangedSince(withStream.log)
	require.True(t, ok)
	assert.Equal(t, []string{"be_app"}, names)
	_, ok = base.backendsChangedSince(withoutApp.log)
	assert.False(t, ok, "a descendant is not an ancestor")

	other := NewPreparedPlanSnapshot()
	_, ok = withoutApp.backendsChangedSince(other.log)
	assert.False(t, ok, "another lineage's root is never reached")
	declared, err := NewPreparedPlanSnapshotFromDeclarations(nil, nil)
	require.NoError(t, err)
	_, ok = withoutApp.backendsChangedSince(declared.log)
	assert.False(t, ok)
}

func TestPreparedPlanSnapshotCutsTheLogAtItsDepthBound(t *testing.T) {
	snapshot := NewPreparedPlanSnapshot()
	base := snapshot
	var err error
	for step := range maxPreparedBackendLogDepth + 2 {
		snapshot, err = snapshot.WithBackend(preparedBackendFixture(t, fmt.Sprintf("be%05d", step), "http"))
		require.NoError(t, err)
		if step == maxPreparedBackendLogDepth-1 {
			names, ok := snapshot.backendsChangedSince(base.log)
			require.True(t, ok)
			assert.Len(t, names, maxPreparedBackendLogDepth)
			assert.Equal(t, uint32(maxPreparedBackendLogDepth), snapshot.log.depth)
		}
	}
	_, ok := snapshot.backendsChangedSince(base.log)
	assert.False(t, ok, "the cut chain no longer reaches the original root")
	assert.Equal(t, uint32(2), snapshot.log.depth)
	assert.Nil(t, snapshot.log.parent.parent.parent)
}

func twoRenderRegistries(tb testing.TB) (first, second *PlanRegistry) {
	tb.Helper()
	prepared, err := NewPreparedPlanSnapshotFromDeclarations(nil, []*PreparedPlanBackend{
		preparedBackendFixture(tb, "be_app", "http"),
		preparedBackendFixture(tb, "be_stream", "tcp"),
	})
	require.NoError(tb, err)
	first = &PlanRegistry{
		prepared: prepared,
		backends: map[string]renderplan.Backend{
			"be_declared": {Name: "be_declared", Mode: "http", ContentKnown: true},
		},
		assembled: []renderplan.Section{
			{Kind: renderplan.SectionKindBackend, Name: "be_app", TextDigest: "app-digest"},
			{Kind: renderplan.SectionKindBackend, Name: "be_declared", TextDigest: "declared-digest"},
		},
	}
	next, err := prepared.WithBackend(preparedBackendFixture(tb, "be_new", "http"))
	require.NoError(tb, err)
	next, err = next.WithoutBackend("be_stream")
	require.NoError(tb, err)
	second = &PlanRegistry{
		prepared: next,
		backends: map[string]renderplan.Backend{
			"be_other": {Name: "be_other", Mode: "http", ContentKnown: true},
		},
	}
	return first, second
}

func TestPlanBackendSourceServesPreparedAndDeclaredBackendsWithTheirDigests(t *testing.T) {
	first, _ := twoRenderRegistries(t)
	source, err := first.planBackendSource()
	require.NoError(t, err)
	assert.Equal(t, 3, source.Len())
	app, exists := source.Get("be_app")
	require.True(t, exists)
	assert.Equal(t, "app-digest", app.TextDigest)
	assert.True(t, app.ContentKnown)
	assert.Equal(t, []string{"    timeout server 5s"}, app.Body)
	app.TextDigest = ""
	first.backends["be_app"] = app
	overlapping, err := first.planBackendSource()
	require.NoError(t, err)
	assert.Equal(t, 3, overlapping.Len(), "a name declared and prepared at once counts once, as Walk visits it")
	delete(first.backends, "be_app")
	declared, exists := source.Get("be_declared")
	require.True(t, exists)
	assert.Equal(t, "declared-digest", declared.TextDigest)
	stream, exists := source.Get("be_stream")
	require.True(t, exists)
	assert.Empty(t, stream.TextDigest)
	_, exists = source.Get("be_missing")
	assert.False(t, exists)
	walked := map[string]renderplan.Backend{}
	require.NoError(t, source.Walk(func(name string, backend renderplan.Backend) error {
		walked[name] = backend
		return nil
	}))
	assert.Len(t, walked, 3)
	assert.Equal(t, "declared-digest", walked["be_declared"].TextDigest)
}

func TestPlanBackendSourceReportsPreparedAndDeclaredChanges(t *testing.T) {
	first, second := twoRenderRegistries(t)
	firstSource, err := first.planBackendSource()
	require.NoError(t, err)
	secondSource, err := second.planBackendSource()
	require.NoError(t, err)
	names, ok := secondSource.ChangedSince(firstSource.Token())
	require.True(t, ok)
	slices.Sort(names)
	assert.Equal(t, []string{"be_declared", "be_new", "be_other", "be_stream"}, names,
		"both renders' declared names and every logged prepared step")
	_, ok = secondSource.ChangedSince(nil)
	assert.False(t, ok)
	_, ok = secondSource.ChangedSince("not a token")
	assert.False(t, ok)

	unrelated, err := NewPreparedPlanSnapshotFromDeclarations(nil, nil)
	require.NoError(t, err)
	third := &PlanRegistry{prepared: unrelated}
	thirdSource, err := third.planBackendSource()
	require.NoError(t, err)
	_, ok = thirdSource.ChangedSince(firstSource.Token())
	assert.False(t, ok, "another lineage compares every backend")

	withoutPrepared := &PlanRegistry{backends: map[string]renderplan.Backend{"be_only": {Name: "be_only", ContentKnown: true}}}
	bareSource, err := withoutPrepared.planBackendSource()
	require.NoError(t, err)
	_, ok = bareSource.ChangedSince(firstSource.Token())
	assert.False(t, ok)
	_, ok = secondSource.ChangedSince(bareSource.Token())
	assert.False(t, ok, "a token without a prepared log never matches")
}

func TestPlanBackendSourceRejectsADeclarationThatContradictsThePreparedBackend(t *testing.T) {
	prepared, err := NewPreparedPlanSnapshotFromDeclarations(nil, []*PreparedPlanBackend{
		preparedBackendFixture(t, "be_app", "http"),
	})
	require.NoError(t, err)
	registry := &PlanRegistry{
		prepared: prepared,
		backends: map[string]renderplan.Backend{"be_app": {Name: "be_app", Mode: "tcp", ContentKnown: true}},
	}
	_, err = registry.planBackendSource()
	require.ErrorContains(t, err, `backend "be_app" declared twice with different values`)
}
