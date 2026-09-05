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

package rendercycle

import (
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/renderartifact"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/renderoutput"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/renderplan"
	"gitlab.com/haproxy-haptic/haptic/pkg/templating"
)

type effectSnapshots struct {
	status    *templating.StatusPatchSnapshot
	events    *templating.RenderedEventSnapshot
	resources *templating.RenderedResourceSnapshot
}

type cycleFixture struct {
	outputAuthority   *renderoutput.Authority
	cycleAuthority    *Authority
	artifactAuthority *renderartifact.Authority
	artifacts         *renderartifact.Snapshot
}

func TestAuthorityRejectsCopiesAndForeignOutputLineages(t *testing.T) {
	fixture := newCycleFixture(t)
	require.NoError(t, fixture.cycleAuthority.ValidateAuthentication())

	copyAuthority := *fixture.cycleAuthority
	require.ErrorIs(t, copyAuthority.ValidateAuthentication(), errInvalidAuthority)
	var zero Authority
	require.ErrorIs(t, zero.ValidateAuthentication(), errInvalidAuthority)
	_, err := NewAuthority(nil)
	require.ErrorIs(t, err, errInvalidAuthority)
	require.ErrorIs(t, (*Authority)(nil).ValidateSnapshot(nil), errInvalidAuthority)

	foreign := newCycleFixture(t)
	foreignEffects := newEffectSnapshots(t, "foreign", 1)
	foreignOutput := foreign.newOutput(t, "global\n", nil)
	_, err = NewSnapshot(
		fixture.cycleAuthority, foreignOutput, foreignEffects.status,
		foreignEffects.events, foreignEffects.resources, nil,
	)
	require.Error(t, err)
}

func TestSnapshotBindsExactChildrenAndReusesOnlyTheirRoots(t *testing.T) {
	fixture := newCycleFixture(t)
	output := fixture.newOutput(t, "global\n", nil)
	effects := newEffectSnapshots(t, "a", 1)
	snapshot := mustCycleSnapshot(t, fixture.cycleAuthority, output, effects, nil)
	require.NoError(t, snapshot.ValidateAuthentication())

	boundOutput, err := snapshot.OutputSnapshot()
	require.NoError(t, err)
	assert.Same(t, output, boundOutput)
	boundStatus, err := snapshot.StatusPatchSnapshot()
	require.NoError(t, err)
	assert.Same(t, effects.status, boundStatus)
	boundEvents, err := snapshot.RenderedEventSnapshot()
	require.NoError(t, err)
	assert.Same(t, effects.events, boundEvents)
	boundResources, err := snapshot.RenderedResourceSnapshot()
	require.NoError(t, err)
	assert.Same(t, effects.resources, boundResources)

	checksum, err := snapshot.ContentChecksum()
	require.NoError(t, err)
	outputChecksum, err := output.ContentChecksum()
	require.NoError(t, err)
	assert.Equal(t, outputChecksum, checksum)

	reused := mustCycleSnapshot(t, fixture.cycleAuthority, output, effects, snapshot)
	assert.Same(t, snapshot, reused)

	changedOutput := fixture.newOutput(t, "global\n  daemon\n", output)
	changedEffects := newEffectSnapshots(t, "b", 1)
	tests := []struct {
		name      string
		output    *renderoutput.Snapshot
		effects   effectSnapshots
		wantEqual bool
	}{
		{name: "output", output: changedOutput, effects: effects},
		{name: "status patches", output: output, effects: effectSnapshots{
			status: changedEffects.status, events: effects.events, resources: effects.resources,
		}},
		{name: "events", output: output, effects: effectSnapshots{
			status: effects.status, events: changedEffects.events, resources: effects.resources,
		}},
		{name: "rendered resources", output: output, effects: effectSnapshots{
			status: effects.status, events: effects.events, resources: changedEffects.resources,
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			changed := mustCycleSnapshot(t, fixture.cycleAuthority, test.output, test.effects, snapshot)
			assert.NotSame(t, snapshot, changed)
			same, sameErr := snapshot.SameRoot(changed)
			require.NoError(t, sameErr)
			assert.False(t, same)
			equal, equalErr := snapshot.ExactEqual(changed)
			require.NoError(t, equalErr)
			assert.Equal(t, test.wantEqual, equal)
		})
	}
}

func TestSnapshotExactEqualAcrossAuthoritiesRequiresEveryExactChild(t *testing.T) {
	leftFixture := newCycleFixture(t)
	rightFixture := newCycleFixture(t)
	leftEffects := newEffectSnapshots(t, "stable", 2)
	rightEffects := newEffectSnapshots(t, "stable", 2)
	left := mustCycleSnapshot(
		t, leftFixture.cycleAuthority, leftFixture.newOutput(t, "global\n", nil), leftEffects, nil,
	)
	right := mustCycleSnapshot(
		t, rightFixture.cycleAuthority, rightFixture.newOutput(t, "global\n", nil), rightEffects, nil,
	)

	require.ErrorIs(t, leftFixture.cycleAuthority.ValidateSnapshot(right), errForeignSnapshot)
	same, err := left.SameRoot(right)
	require.NoError(t, err)
	assert.False(t, same)
	equal, err := left.ExactEqual(right)
	require.NoError(t, err)
	assert.True(t, equal)

	changedEffects := newEffectSnapshots(t, "changed", 2)
	changed := mustCycleSnapshot(
		t, rightFixture.cycleAuthority, right.root.output, effectSnapshots{
			status: changedEffects.status, events: rightEffects.events, resources: rightEffects.resources,
		}, right,
	)
	changedChecksum, err := changed.ContentChecksum()
	require.NoError(t, err)
	leftChecksum, err := left.ContentChecksum()
	require.NoError(t, err)
	assert.Equal(t, leftChecksum, changedChecksum)
	equal, err = left.ExactEqual(changed)
	require.NoError(t, err)
	assert.False(t, equal, "the output checksum cannot authorize effect equality")
}

func TestNewSnapshotRejectsNilShallowCopiesAndForeignPrevious(t *testing.T) {
	fixture := newCycleFixture(t)
	output := fixture.newOutput(t, "global\n", nil)
	effects := newEffectSnapshots(t, "stable", 1)
	previous := mustCycleSnapshot(t, fixture.cycleAuthority, output, effects, nil)

	tests := []struct {
		name      string
		output    *renderoutput.Snapshot
		status    *templating.StatusPatchSnapshot
		events    *templating.RenderedEventSnapshot
		resources *templating.RenderedResourceSnapshot
	}{
		{name: "nil output", status: effects.status, events: effects.events, resources: effects.resources},
		{name: "nil status", output: output, events: effects.events, resources: effects.resources},
		{name: "nil events", output: output, status: effects.status, resources: effects.resources},
		{name: "nil resources", output: output, status: effects.status, events: effects.events},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := NewSnapshot(
				fixture.cycleAuthority, test.output, test.status,
				test.events, test.resources, nil,
			)
			require.Error(t, err)
		})
	}

	outputCopy := *output
	_, err := NewSnapshot(
		fixture.cycleAuthority, &outputCopy, effects.status, effects.events, effects.resources, nil,
	)
	require.Error(t, err)
	statusCopy := *effects.status
	_, err = NewSnapshot(
		fixture.cycleAuthority, output, &statusCopy, effects.events, effects.resources, nil,
	)
	require.Error(t, err)
	eventCopy := *effects.events
	_, err = NewSnapshot(
		fixture.cycleAuthority, output, effects.status, &eventCopy, effects.resources, nil,
	)
	require.Error(t, err)
	resourceCopy := *effects.resources
	_, err = NewSnapshot(
		fixture.cycleAuthority, output, effects.status, effects.events, &resourceCopy, nil,
	)
	require.Error(t, err)

	previousCopy := *previous
	_, err = NewSnapshot(
		fixture.cycleAuthority, output, effects.status, effects.events, effects.resources, &previousCopy,
	)
	require.ErrorIs(t, err, errInvalidSnapshot)

	foreign := newCycleFixture(t)
	foreignEffects := newEffectSnapshots(t, "stable", 1)
	foreignPrevious := mustCycleSnapshot(
		t, foreign.cycleAuthority, foreign.newOutput(t, "global\n", nil), foreignEffects, nil,
	)
	_, err = NewSnapshot(
		fixture.cycleAuthority, output, effects.status, effects.events, effects.resources, foreignPrevious,
	)
	require.ErrorIs(t, err, errForeignSnapshot)
}

func TestSnapshotRejectsABChildSubstitution(t *testing.T) {
	fixture := newCycleFixture(t)
	aEffects := newEffectSnapshots(t, "a", 1)
	bEffects := newEffectSnapshots(t, "b", 1)
	a := mustCycleSnapshot(
		t, fixture.cycleAuthority, fixture.newOutput(t, "global\n", nil), aEffects, nil,
	)
	b := mustCycleSnapshot(
		t, fixture.cycleAuthority, fixture.newOutput(t, "global\n  daemon\n", nil), bEffects, nil,
	)

	assertSubstitutionRejected := func(t *testing.T, substitute func(), restore func()) {
		t.Helper()
		substitute()
		err := a.ValidateAuthentication()
		restore()
		require.ErrorIs(t, err, errInvalidSnapshot)
		require.NoError(t, a.ValidateAuthentication())
	}
	t.Run("output", func(t *testing.T) {
		original := a.root.output
		assertSubstitutionRejected(t, func() { a.root.output = b.root.output }, func() { a.root.output = original })
	})
	t.Run("status patches", func(t *testing.T) {
		original := a.root.statusPatches
		assertSubstitutionRejected(t,
			func() { a.root.statusPatches = b.root.statusPatches },
			func() { a.root.statusPatches = original },
		)
	})
	t.Run("events", func(t *testing.T) {
		original := a.root.events
		assertSubstitutionRejected(t, func() { a.root.events = b.root.events }, func() { a.root.events = original })
	})
	t.Run("rendered resources", func(t *testing.T) {
		original := a.root.renderedResources
		assertSubstitutionRejected(t,
			func() { a.root.renderedResources = b.root.renderedResources },
			func() { a.root.renderedResources = original },
		)
	})
	t.Run("checksum", func(t *testing.T) {
		original := a.root.contentChecksum
		assertSubstitutionRejected(t,
			func() { a.root.contentChecksum = b.root.contentChecksum },
			func() { a.root.contentChecksum = original },
		)
	})

	shallow := *a
	require.ErrorIs(t, shallow.ValidateAuthentication(), errInvalidSnapshot)
	rootCopy := *a.root
	rootCopy.seal = &rootCopy
	wrapper := sealSnapshot(fixture.cycleAuthority, &rootCopy)
	require.ErrorIs(t, wrapper.ValidateAuthentication(), errInvalidSnapshot)
}

func TestSnapshotOwnsCallerSourcesAndReturnsImmutableRoots(t *testing.T) {
	fixture := newCycleFixture(t)
	output := fixture.newOutput(t, "global\n", nil)

	statusValue := map[string]any{"owner": "stable", "nested": []any{map[string]any{"value": 1}}}
	statusVariants := map[string]map[string]any{"rendered": statusValue}
	statusCollector := templating.NewStatusPatchCollector()
	require.NoError(t, statusCollector.Register(
		"default", "route", "example.test/v1", "Route", statusVariants,
	))

	resourceData := map[string]any{"owner": "stable"}
	resourceObject := map[string]any{"data": resourceData}
	resourceCollector := templating.NewRenderedResourceCollector()
	require.NoError(t, resourceCollector.Register(
		"v1", "ConfigMap", "default", "settings", resourceObject,
	))

	eventCollector := templating.NewEventCollector()
	require.NoError(t, eventCollector.Register(
		"default", "route", "example.test/v1", "Route",
		templating.EventTypeWarning, "Conflict", "stable",
	))

	statusValue["owner"] = "caller poison"
	statusValue["nested"].([]any)[0].(map[string]any)["value"] = 2
	resourceData["owner"] = "caller poison"
	resourceObject["data"] = map[string]any{"owner": "replacement poison"}

	status, err := statusCollector.Snapshot()
	require.NoError(t, err)
	events, err := eventCollector.Snapshot()
	require.NoError(t, err)
	resources, err := resourceCollector.Snapshot()
	require.NoError(t, err)
	cycle := mustCycleSnapshot(t, fixture.cycleAuthority, output, effectSnapshots{
		status: status, events: events, resources: resources,
	}, nil)

	boundStatus, err := cycle.StatusPatchSnapshot()
	require.NoError(t, err)
	patches, err := boundStatus.Patches()
	require.NoError(t, err)
	assert.Equal(t, "stable", patches[0].Variants["rendered"]["owner"])
	assert.Equal(t, 1, patches[0].Variants["rendered"]["nested"].([]any)[0].(map[string]any)["value"])
	patches[0].Variants["rendered"]["owner"] = "result poison"

	boundResources, err := cycle.RenderedResourceSnapshot()
	require.NoError(t, err)
	materializedResources, err := boundResources.Resources()
	require.NoError(t, err)
	assert.Equal(t, "stable", materializedResources[0].Object["data"].(map[string]any)["owner"])
	materializedResources[0].Object["data"].(map[string]any)["owner"] = "result poison"

	patches, err = boundStatus.Patches()
	require.NoError(t, err)
	assert.Equal(t, "stable", patches[0].Variants["rendered"]["owner"])
	materializedResources, err = boundResources.Resources()
	require.NoError(t, err)
	assert.Equal(t, "stable", materializedResources[0].Object["data"].(map[string]any)["owner"])
}

func TestSnapshotABAReturnsToEqualContentWithoutReusingStaleRoot(t *testing.T) {
	fixture := newCycleFixture(t)
	outputA := fixture.newOutput(t, "global\n", nil)
	outputB := fixture.newOutput(t, "global\n  daemon\n", outputA)
	outputAAgain := fixture.newOutput(t, "global\n", outputB)
	aEffects := newEffectSnapshots(t, "a", 2)
	bEffects := newEffectSnapshots(t, "b", 2)
	aEffectsAgain := newEffectSnapshots(t, "a", 2)

	a := mustCycleSnapshot(t, fixture.cycleAuthority, outputA, aEffects, nil)
	b := mustCycleSnapshot(t, fixture.cycleAuthority, outputB, bEffects, a)
	aAgain := mustCycleSnapshot(t, fixture.cycleAuthority, outputAAgain, aEffectsAgain, b)

	same, err := a.SameRoot(aAgain)
	require.NoError(t, err)
	assert.False(t, same)
	equal, err := a.ExactEqual(aAgain)
	require.NoError(t, err)
	assert.True(t, equal)
	equal, err = a.ExactEqual(b)
	require.NoError(t, err)
	assert.False(t, equal)

	reused := mustCycleSnapshot(t, fixture.cycleAuthority, outputAAgain, aEffectsAgain, aAgain)
	assert.Same(t, aAgain, reused)
}

func TestSnapshotConcurrentReaders(t *testing.T) {
	fixture := newCycleFixture(t)
	output := fixture.newOutput(t, "global\n", nil)
	effects := newEffectSnapshots(t, "stable", 256)
	snapshot := mustCycleSnapshot(t, fixture.cycleAuthority, output, effects, nil)

	const workers = 32
	const iterations = 50
	errorsByWorker := make(chan error, workers)
	var done sync.WaitGroup
	for range workers {
		done.Add(1)
		go func() {
			defer done.Done()
			for range iterations {
				if err := verifySnapshotRead(snapshot, output, effects); err != nil {
					errorsByWorker <- err
					return
				}
			}
			errorsByWorker <- nil
		}()
	}
	done.Wait()
	close(errorsByWorker)
	for err := range errorsByWorker {
		require.NoError(t, err)
	}
}

func verifySnapshotRead(
	snapshot *Snapshot,
	output *renderoutput.Snapshot,
	effects effectSnapshots,
) error {
	if err := snapshot.ValidateAuthentication(); err != nil {
		return err
	}
	same, err := snapshot.SameRoot(snapshot)
	if err != nil {
		return fmt.Errorf("same root: %w", err)
	}
	if !same {
		return errors.New("same root returned false")
	}
	boundOutput, err := snapshot.OutputSnapshot()
	if err != nil {
		return fmt.Errorf("output root: %w", err)
	}
	if boundOutput != output {
		return errors.New("output root changed")
	}
	boundStatus, err := snapshot.StatusPatchSnapshot()
	if err != nil {
		return fmt.Errorf("status root: %w", err)
	}
	if boundStatus != effects.status {
		return errors.New("status root changed")
	}
	boundEvents, err := snapshot.RenderedEventSnapshot()
	if err != nil {
		return fmt.Errorf("event root: %w", err)
	}
	if boundEvents != effects.events {
		return errors.New("event root changed")
	}
	boundResources, err := snapshot.RenderedResourceSnapshot()
	if err != nil {
		return fmt.Errorf("resource root: %w", err)
	}
	if boundResources != effects.resources {
		return errors.New("resource root changed")
	}
	return nil
}

func TestSnapshotAccessorsAreNilSafe(t *testing.T) {
	var snapshot *Snapshot
	require.ErrorIs(t, snapshot.ValidateAuthentication(), errInvalidSnapshot)
	_, err := snapshot.SameRoot(nil)
	require.ErrorIs(t, err, errInvalidSnapshot)
	_, err = snapshot.ExactEqual(nil)
	require.ErrorIs(t, err, errInvalidSnapshot)
	_, err = snapshot.OutputSnapshot()
	require.ErrorIs(t, err, errInvalidSnapshot)
	_, err = snapshot.StatusPatchSnapshot()
	require.ErrorIs(t, err, errInvalidSnapshot)
	_, err = snapshot.RenderedEventSnapshot()
	require.ErrorIs(t, err, errInvalidSnapshot)
	_, err = snapshot.RenderedResourceSnapshot()
	require.ErrorIs(t, err, errInvalidSnapshot)
	_, err = snapshot.ContentChecksum()
	require.ErrorIs(t, err, errInvalidSnapshot)
}

func newCycleFixture(tb testing.TB) cycleFixture {
	tb.Helper()
	planAuthority := renderplan.NewAuthority()
	artifactAuthority := renderartifact.NewAuthority()
	outputAuthority, err := renderoutput.NewAuthority(planAuthority, artifactAuthority)
	require.NoError(tb, err)
	cycleAuthority, err := NewAuthority(outputAuthority)
	require.NoError(tb, err)
	builder, err := renderartifact.NewBuilder(artifactAuthority, nil)
	require.NoError(tb, err)
	artifacts, err := builder.Build()
	require.NoError(tb, err)
	return cycleFixture{
		outputAuthority: outputAuthority, cycleAuthority: cycleAuthority,
		artifactAuthority: artifactAuthority, artifacts: artifacts,
	}
}

func (f cycleFixture) newOutput(
	tb testing.TB,
	config string,
	previous *renderoutput.Snapshot,
) *renderoutput.Snapshot {
	tb.Helper()
	plan := &renderplan.Plan{
		SchemaVersion: renderplan.SchemaVersion,
		Sections: []renderplan.Section{{
			Kind: renderplan.SectionKindCore, Name: "core#0",
			TextDigest: renderplan.DigestString(config), Length: len(config),
			Text: config, TextKnown: true,
		}},
		Files: []renderplan.File{{
			Path: renderplan.ConfigFilePath, Kind: renderplan.FileKindConfig,
			ReloadOnChange: true, Digest: renderplan.DigestString(config),
			Size: int64(len(config)), Content: config, ContentKnown: true,
		}},
	}
	plan.ComputeID()
	output, err := renderoutput.NewSnapshot(
		f.outputAuthority, config, plan, f.artifacts, previous,
	)
	require.NoError(tb, err)
	return output
}

func newEffectSnapshots(tb testing.TB, value string, count int) effectSnapshots {
	tb.Helper()
	statusCollector := templating.NewStatusPatchCollector()
	eventCollector := templating.NewEventCollector()
	resourceCollector := templating.NewRenderedResourceCollector()
	for index := range count {
		name := fmt.Sprintf("route-%06d", index)
		require.NoError(tb, statusCollector.Register(
			"default", name, "example.test/v1", "Route",
			map[string]map[string]any{
				"rendered": {"owner": value, "index": index},
				"deployed": {"owner": value, "index": index},
			},
		))
		require.NoError(tb, eventCollector.Register(
			"default", name, "example.test/v1", "Route",
			templating.EventTypeWarning, "Conflict", value,
		))
		require.NoError(tb, resourceCollector.Register(
			"v1", "ConfigMap", "default", name,
			map[string]any{"data": map[string]any{"owner": value, "index": index}},
		))
	}
	status, err := statusCollector.Snapshot()
	require.NoError(tb, err)
	events, err := eventCollector.Snapshot()
	require.NoError(tb, err)
	resources, err := resourceCollector.Snapshot()
	require.NoError(tb, err)
	return effectSnapshots{status: status, events: events, resources: resources}
}

func mustCycleSnapshot(
	tb testing.TB,
	authority *Authority,
	output *renderoutput.Snapshot,
	effects effectSnapshots,
	previous *Snapshot,
) *Snapshot {
	tb.Helper()
	snapshot, err := NewSnapshot(
		authority, output, effects.status, effects.events, effects.resources, previous,
	)
	require.NoError(tb, err)
	return snapshot
}
