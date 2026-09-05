package incremental

import (
	"context"
	"sync/atomic"
	"testing"
)

func TestPreparedGraphCommitRejectsMutatedDraftRoots(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*preparedGraphDraftPoisonFixture)
	}{
		{
			name: "input tree substitution",
			mutate: func(f *preparedGraphDraftPoisonFixture) {
				entry, _ := f.prepared.prepared.generation.inputs.Root().Get([]byte(f.inputKey.value))
				inputs, _, _ := f.prepared.prepared.generation.inputs.Insert([]byte(f.inputKey.value), entry)
				f.prepared.prepared.generation.inputs = inputs
			},
		},
		{
			name: "input value substitution",
			mutate: func(f *preparedGraphDraftPoisonFixture) {
				entry, _ := f.prepared.prepared.generation.inputs.Root().Get([]byte(f.inputKey.value))
				entry.value = "forged"
				inputs, _, _ := f.prepared.prepared.generation.inputs.Insert([]byte(f.inputKey.value), entry)
				f.prepared.prepared.generation.inputs = inputs
			},
		},
		{
			name: "node tree mutation",
			mutate: func(f *preparedGraphDraftPoisonFixture) {
				nodes, _, _ := f.prepared.prepared.generation.nodes.Delete([]byte(f.queryKey.value))
				f.prepared.prepared.generation.nodes = nodes
			},
		},
		{
			name: "node dependency mutation",
			mutate: func(f *preparedGraphDraftPoisonFixture) {
				entry, _ := f.prepared.prepared.generation.nodes.Root().Get([]byte(f.queryKey.value))
				deps, _ := entry.deps.Values(f.graph.dependencyAuthority)
				deps[0].revision = NewRevision("forged-dependency")
				entry.deps, _ = f.graph.dependencyAuthority.Own(deps)
				f.replaceNode(entry)
			},
		},
		{
			name: "node observation mutation",
			mutate: func(f *preparedGraphDraftPoisonFixture) {
				entry, _ := f.prepared.prepared.generation.nodes.Root().Get([]byte(f.queryKey.value))
				inputs, _ := entry.inputs.Values(f.graph.observationAuthority)
				inputs[0].Revision = NewRevision("forged-node-observation")
				entry.inputs, _ = f.graph.observationAuthority.Own(inputs)
				f.replaceNode(entry)
			},
		},
		{
			name: "node value substitution",
			mutate: func(f *preparedGraphDraftPoisonFixture) {
				entry, _ := f.prepared.prepared.generation.nodes.Root().Get([]byte(f.queryKey.value))
				entry.value = newExactValueRoot(f.graph.valueAuthority, f.queryKey, "forged")
				f.replaceNode(entry)
			},
		},
		{
			name: "reverse root mutation",
			mutate: func(f *preparedGraphDraftPoisonFixture) {
				key := dependencyTreeKey(inputDep(f.inputKey))
				reverse, _, _ := f.prepared.prepared.generation.reverse.Insert(
					[]byte(key), f.graph.reverseAuthority.Empty(),
				)
				f.prepared.prepared.generation.reverse = reverse
			},
		},
		{
			name: "dirty map mutation",
			mutate: func(f *preparedGraphDraftPoisonFixture) {
				dirty, _, _ := f.prepared.prepared.generation.dirty.Insert([]byte(f.queryKey.value), struct{}{})
				f.prepared.prepared.generation.dirty = dirty
			},
		},
		{
			name: "counter map mutation",
			mutate: func(f *preparedGraphDraftPoisonFixture) {
				counters, _ := f.prepared.prepared.generation.counters.Root().Get([]byte(f.queryKey.value))
				counters.Executions++
				tree, _, _ := f.prepared.prepared.generation.counters.Insert([]byte(f.queryKey.value), counters)
				f.prepared.prepared.generation.counters = tree
			},
		},
		{
			name: "observation mutation",
			mutate: func(f *preparedGraphDraftPoisonFixture) {
				f.replaceObservation(NewRevision("forged-observation"))
			},
		},
		{
			name: "retired input mutation",
			mutate: func(f *preparedGraphDraftPoisonFixture) {
				f.replaceRetirement(NewInputKey("forged-retirement"))
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newPreparedGraphDraftPoisonFixture(t)
			test.mutate(fixture)
			if err := fixture.prepared.ValidateAuthentication(); err == nil {
				t.Fatal("ValidateAuthentication() accepted a mutated prepared draft")
			}
			var verifierCalls atomic.Int32
			err := fixture.prepared.Publish(t.Context(), func(context.Context, []InputRevision) (bool, error) {
				verifierCalls.Add(1)
				return true, nil
			})
			if err == nil {
				t.Fatal("Publish() accepted a mutated prepared draft")
			}
			if verifierCalls.Load() != 0 {
				t.Fatalf("mutated draft reached the verifier %d times", verifierCalls.Load())
			}
			fixture.assertBase(t)
			if err := fixture.prepared.Abort(); err != nil {
				t.Fatalf("Abort() error = %v", err)
			}
		})
	}
}

func TestPreparedGraphCommitReauthenticatesDraftAfterCallbacks(t *testing.T) {
	tests := []struct {
		name      string
		wantAbort bool
		publish   func(*testing.T, *preparedGraphDraftPoisonFixture, *bool, *bool) error
	}{
		{
			name: "verifier",
			publish: func(t *testing.T, f *preparedGraphDraftPoisonFixture, _, _ *bool) error {
				t.Helper()
				return f.prepared.Publish(t.Context(), func(context.Context, []InputRevision) (bool, error) {
					f.replaceObservation(NewRevision("verifier-poison"))
					return true, nil
				})
			},
		},
		{
			name:      "preparer",
			wantAbort: true,
			publish: func(
				t *testing.T,
				f *preparedGraphDraftPoisonFixture,
				visible, aborted *bool,
			) error {
				t.Helper()
				return f.prepared.PublishWithPreparedPublisher(
					t.Context(),
					acceptRevisions,
					func([]InputKey) (CommitPublication, error) {
						f.replaceRetirement(NewInputKey("preparer-poison"))
						return poisonDraftPublication(visible, aborted), nil
					},
				)
			},
		},
		{
			name:      "publish",
			wantAbort: true,
			publish: func(
				t *testing.T,
				f *preparedGraphDraftPoisonFixture,
				visible, aborted *bool,
			) error {
				t.Helper()
				publication := poisonDraftPublication(visible, aborted)
				publication.Publish = func() {
					*visible = true
					nodes, _, _ := f.prepared.prepared.generation.nodes.Delete([]byte(f.queryKey.value))
					f.prepared.prepared.generation.nodes = nodes
				}
				return f.prepared.PublishWithPreparedPublisher(
					t.Context(), acceptRevisions,
					func([]InputKey) (CommitPublication, error) { return publication, nil },
				)
			},
		},
		{
			name:      "complete",
			wantAbort: true,
			publish: func(
				t *testing.T,
				f *preparedGraphDraftPoisonFixture,
				visible, aborted *bool,
			) error {
				t.Helper()
				publication := poisonDraftPublication(visible, aborted)
				publication.Complete = func() {
					counters, _, _ := f.prepared.prepared.generation.counters.Insert(
						[]byte(f.queryKey.value), NodeCounters{Executions: 99},
					)
					f.prepared.prepared.generation.counters = counters
				}
				return f.prepared.PublishWithPreparedPublisher(
					t.Context(), acceptRevisions,
					func([]InputKey) (CommitPublication, error) { return publication, nil },
				)
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newPreparedGraphDraftPoisonFixture(t)
			visible := false
			aborted := false
			err := test.publish(t, fixture, &visible, &aborted)
			if err == nil {
				t.Fatal("PublishWithPreparedPublisher() accepted callback draft mutation")
			}
			if visible {
				t.Fatal("failed draft retained caller publication")
			}
			if aborted != test.wantAbort {
				t.Fatalf("caller abort = %t, want %t", aborted, test.wantAbort)
			}
			fixture.assertBase(t)
			if err := fixture.prepared.ValidateAuthentication(); err == nil {
				t.Fatal("failed draft remained live")
			}
		})
	}
}

func TestPreparedGraphCommitOwnsSpeculativeLeafStorage(t *testing.T) {
	fixture := newPreparedGraphDraftPoisonFixture(t)
	session := fixture.prepared.prepared.session

	input := session.inputChanges[fixture.inputKey]
	input.value[0] = 'X'
	session.inputChanges[fixture.inputKey] = input
	node := session.nodeChanges[fixture.queryKey]
	node.deps[0].revision = NewRevision("poison-dependency")
	node.inputs[0].Revision = NewRevision("poison-node-observation")
	session.nodeChanges[fixture.queryKey] = node
	observation := session.observations[fixture.inputKey]
	observation.Revision = NewRevision("poison-observation")
	session.observations[fixture.inputKey] = observation

	if err := fixture.prepared.ValidateAuthentication(); err != nil {
		t.Fatalf("ValidateAuthentication() after source mutation = %v", err)
	}
	var verified []InputRevision
	err := fixture.prepared.Publish(t.Context(), func(_ context.Context, observations []InputRevision) (bool, error) {
		verified = append([]InputRevision(nil), observations...)
		return true, nil
	})
	if err != nil {
		t.Fatalf("Publish() error = %v", err)
	}
	verifiedRevision := Revision{}
	for _, current := range verified {
		if current.Key == fixture.inputKey {
			verifiedRevision = current.Revision
		}
	}
	if verifiedRevision != NewRevision("candidate-revision") {
		t.Fatalf("verified observations = %#v", verified)
	}
	if got := stringValue(t, fixture.graph, fixture.queryKey); got != "candidate" {
		t.Fatalf("published query = %q", got)
	}
	if !fixture.graph.HasInputDependents(fixture.inputKey) {
		t.Fatal("published query lost its exact input dependency")
	}
}

type preparedGraphDraftPoisonFixture struct {
	graph      *Graph
	prepared   PreparedGraphCommit
	base       *graphGeneration
	inputKey   InputKey
	retiredKey InputKey
	queryKey   QueryKey
}

func (f *preparedGraphDraftPoisonFixture) replaceNode(entry committedNodeEntry) {
	nodes, _, _ := f.prepared.prepared.generation.nodes.Insert([]byte(f.queryKey.value), entry)
	f.prepared.prepared.generation.nodes = nodes
}

func (f *preparedGraphDraftPoisonFixture) replaceObservation(revision Revision) {
	observations, _ := f.prepared.prepared.observations.Values(f.graph.observationAuthority)
	observations[0].Revision = revision
	f.prepared.prepared.observations, _ = f.graph.observationAuthority.Own(observations)
}

func (f *preparedGraphDraftPoisonFixture) replaceRetirement(key InputKey) {
	retiredInputs, _ := f.prepared.prepared.retiredInputs.Values(f.graph.retiredInputAuthority)
	retiredInputs[0] = key
	f.prepared.prepared.retiredInputs, _ = f.graph.retiredInputAuthority.Own(retiredInputs)
}

func newPreparedGraphDraftPoisonFixture(t *testing.T) *preparedGraphDraftPoisonFixture {
	t.Helper()
	inputKey := NewInputKey("input")
	retiredKey := NewInputKey("retired")
	queryKey := NewQueryKey("query")
	graph := mustRetiringGraph(t, Definition{Key: queryKey, Run: readInputQuery(inputKey)})
	initial := mustBeginWithResolver(t, graph, failingResolver(t))
	mustApply(t, initial, exactInput(inputKey, "base-revision", "base"))
	mustEvaluate(t, initial, queryKey)
	mustCommit(t, initial)
	base := graph.current

	replacement, err := graph.BeginColdResetWithResolver(
		failingResolver(t),
		exactInput(inputKey, "candidate-revision", "candidate"),
		exactInput(retiredKey, "retired-revision", "retired"),
	)
	if err != nil {
		t.Fatalf("BeginColdResetWithResolver() error = %v", err)
	}
	mustEvaluate(t, replacement, queryKey)
	prepared, err := replacement.PrepareGraphCommit(t.Context())
	if err != nil {
		t.Fatalf("PrepareGraphCommit() error = %v", err)
	}
	observationCount, err := prepared.prepared.observations.Len(graph.observationAuthority)
	if err != nil || observationCount == 0 {
		t.Fatal("prepared draft has no observations")
	}
	retiredInputs, err := prepared.prepared.retiredInputs.Values(graph.retiredInputAuthority)
	if err != nil || len(retiredInputs) != 1 || retiredInputs[0] != retiredKey {
		t.Fatalf("prepared retired inputs = %#v, want %#v", retiredInputs, []InputKey{retiredKey})
	}
	entry, exists := prepared.prepared.generation.nodes.Root().Get([]byte(queryKey.value))
	deps, depsErr := entry.deps.Len(graph.dependencyAuthority)
	inputs, inputsErr := entry.inputs.Len(graph.observationAuthority)
	if !exists || depsErr != nil || inputsErr != nil || deps == 0 || inputs == 0 {
		t.Fatal("prepared query has no exact dependency frame")
	}
	return &preparedGraphDraftPoisonFixture{
		graph: graph, prepared: prepared, base: base,
		inputKey: inputKey, retiredKey: retiredKey, queryKey: queryKey,
	}
}

func (f *preparedGraphDraftPoisonFixture) assertBase(t *testing.T) {
	t.Helper()
	if f.graph.current != f.base || f.graph.Generation() != f.base.number {
		t.Fatalf("failed draft changed graph generation from %d to %d", f.base.number, f.graph.Generation())
	}
	if got := stringValue(t, f.graph, f.queryKey); got != "base" {
		t.Fatalf("failed draft changed base query to %q", got)
	}
}

func poisonDraftPublication(visible, aborted *bool) CommitPublication {
	return CommitPublication{
		Publish: func() { *visible = true },
		Abort: func() {
			*visible = false
			*aborted = true
		},
	}
}
