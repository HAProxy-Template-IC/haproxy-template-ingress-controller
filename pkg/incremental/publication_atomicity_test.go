// Copyright 2026 Philipp Hossner
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

package incremental

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func poisonedPublishPublication(state *int) CommitPublication {
	return CommitPublication{
		Publish: func() {
			*state = 1
			panic("publish poison")
		},
		Abort: func() { *state = 0 },
	}
}

func poisonedCompletePublication(state *int) CommitPublication {
	return CommitPublication{
		Publish: func() { *state = 1 },
		Complete: func() {
			*state = 2
			panic("complete poison")
		},
		Abort: func() { *state = 0 },
	}
}

func poisonedAbortPublication(state *int) CommitPublication {
	return CommitPublication{
		Publish: func() {
			*state = 1
			panic("publish poison")
		},
		Abort: func() {
			*state = 0
			panic("abort poison")
		},
	}
}

func TestWarmPublicationPanicRollsBackGraphAndCallerState(t *testing.T) {
	tests := map[string]struct {
		publication func(*int) CommitPublication
		want        []string
	}{
		"publish": {
			publication: poisonedPublishPublication,
			want:        []string{"publication publish panicked", "publish poison"},
		},
		"complete": {
			publication: poisonedCompletePublication,
			want:        []string{"publication complete panicked", "complete poison"},
		},
		"abort": {
			publication: poisonedAbortPublication,
			want:        []string{"publish poison", "publication abort panicked", "abort poison"},
		},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			requireWarmPublicationPanicRollsBack(t, test.publication, test.want)
		})
	}
}

func requireWarmPublicationPanicRollsBack(
	t *testing.T,
	publication func(*int) CommitPublication,
	want []string,
) {
	t.Helper()
	inputKey := NewInputKey("input")
	queryKey := NewQueryKey("query")
	graph := mustGraph(t, Definition{Key: queryKey, Run: readInputQuery(inputKey)})
	initial := mustBegin(t, graph)
	mustApply(t, initial, exactInput(inputKey, "r1", "initial"))
	mustEvaluate(t, initial, queryKey)
	mustCommit(t, initial)
	generation := graph.Generation()
	counters := graph.Counters(queryKey)

	warm := mustBegin(t, graph)
	mustApply(t, warm, exactInput(inputKey, "r2", "candidate"))
	mustEvaluate(t, warm, queryKey)
	state := 0
	err := warm.CommitWithPreparedPublisher(
		context.Background(),
		acceptRevisions,
		func([]InputKey) (CommitPublication, error) { return publication(&state), nil },
	)

	if err == nil {
		t.Fatal("CommitWithPreparedPublisher() accepted a panicking publication")
	}
	for _, wanted := range want {
		if !strings.Contains(err.Error(), wanted) {
			t.Fatalf("CommitWithPreparedPublisher() error = %v, want %q", err, wanted)
		}
	}
	if state != 0 {
		t.Fatalf("caller state = %d after rollback", state)
	}
	if graph.Generation() != generation {
		t.Fatalf("graph generation = %d after rollback, want %d", graph.Generation(), generation)
	}
	if got := stringValue(t, graph, queryKey); got != "initial" {
		t.Fatalf("graph value = %q after rollback", got)
	}
	if got := graph.Counters(queryKey); got != counters {
		t.Fatalf("graph counters = %+v after rollback, want %+v", got, counters)
	}
}

func TestWarmPublicationPreparerPanicPublishesNothing(t *testing.T) {
	inputKey := NewInputKey("input")
	queryKey := NewQueryKey("query")
	graph := mustGraph(t, Definition{Key: queryKey, Run: readInputQuery(inputKey)})
	initial := mustBegin(t, graph)
	mustApply(t, initial, exactInput(inputKey, "r1", "initial"))
	mustEvaluate(t, initial, queryKey)
	mustCommit(t, initial)
	generation := graph.Generation()
	counters := graph.Counters(queryKey)

	warm := mustBegin(t, graph)
	mustApply(t, warm, exactInput(inputKey, "r2", "candidate"))
	mustEvaluate(t, warm, queryKey)
	err := warm.CommitWithPreparedPublisher(t.Context(), acceptRevisions, func([]InputKey) (CommitPublication, error) {
		panic("preparer poison")
	})

	if err == nil || !strings.Contains(err.Error(), "incremental publication preparation panicked: preparer poison") {
		t.Fatalf("CommitWithPreparedPublisher() error = %v, want preparer panic", err)
	}
	if graph.Generation() != generation || stringValue(t, graph, queryKey) != "initial" {
		t.Fatal("panicking warm preparer changed the graph")
	}
	if got := graph.Counters(queryKey); got != counters {
		t.Fatalf("graph counters = %+v after preparer panic, want %+v", got, counters)
	}
	if _, err := warm.Evaluate(t.Context(), queryKey); !errors.Is(err, ErrSessionClosed) {
		t.Fatalf("Evaluate() after preparer panic error = %v, want %v", err, ErrSessionClosed)
	}
}

// publicationPhaseHarness blocks one named publication phase so the test can
// abort concurrently while that phase is still inside its callback.
type publicationPhaseHarness struct {
	entered     chan struct{}
	release     chan struct{}
	finished    chan error
	callerState atomic.Int32
	abortCalls  atomic.Int32
}

func newPublicationPhaseHarness() *publicationPhaseHarness {
	return &publicationPhaseHarness{
		entered:  make(chan struct{}),
		release:  make(chan struct{}),
		finished: make(chan error, 1),
	}
}

func (h *publicationPhaseHarness) verifier(phase string) RevisionVerifier {
	return func(context.Context, []InputRevision) (bool, error) {
		blockPublicationPhase(phase, "verifier", h.entered, h.release)
		return true, nil
	}
}

func (h *publicationPhaseHarness) preparer(phase string) CommitPublicationPreparer {
	return func([]InputKey) (CommitPublication, error) {
		blockPublicationPhase(phase, "preparer", h.entered, h.release)
		return CommitPublication{
			Publish: func() {
				h.callerState.Store(1)
				blockPublicationPhase(phase, "publish", h.entered, h.release)
			},
			Complete: func() {
				h.callerState.Store(2)
				blockPublicationPhase(phase, "complete", h.entered, h.release)
			},
			Abort: func() {
				h.abortCalls.Add(1)
				h.callerState.Store(0)
			},
		}, nil
	}
}

func TestConcurrentSessionAbortDuringWarmPublicationPublishesNothing(t *testing.T) {
	for _, phase := range []string{"verifier", "preparer", "publish", "complete"} {
		t.Run(phase, func(t *testing.T) {
			requireConcurrentWarmAbortPublishesNothing(t, phase)
		})
	}
}

func requireConcurrentWarmAbortPublishesNothing(t *testing.T, phase string) {
	t.Helper()
	inputKey := NewInputKey("input")
	queryKey := NewQueryKey("query")
	graph := mustGraph(t, Definition{Key: queryKey, Run: readInputQuery(inputKey)})
	initial := mustBegin(t, graph)
	mustApply(t, initial, exactInput(inputKey, "r1", "initial"))
	mustEvaluate(t, initial, queryKey)
	mustCommit(t, initial)
	generation := graph.Generation()
	counters := graph.Counters(queryKey)

	warm := mustBegin(t, graph)
	mustApply(t, warm, exactInput(inputKey, "r2", "candidate"))
	mustEvaluate(t, warm, queryKey)
	harness := newPublicationPhaseHarness()
	go func() {
		harness.finished <- warm.CommitWithPreparedPublisher(
			t.Context(),
			harness.verifier(phase),
			harness.preparer(phase),
		)
	}()
	awaitPublicationPhase(t, harness.entered, harness.release)
	abortDone := make(chan struct{})
	go func() {
		warm.Abort()
		close(abortDone)
	}()
	awaitPublicationAbort(t, abortDone, harness.release)
	close(harness.release)
	err := awaitPublicationResult(t, harness.finished)

	if !errors.Is(err, ErrSessionClosed) {
		t.Fatalf("CommitWithPreparedPublisher() error = %v, want %v", err, ErrSessionClosed)
	}
	if graph.Generation() != generation || stringValue(t, graph, queryKey) != "initial" {
		t.Fatal("concurrent warm abort changed the graph")
	}
	if got := graph.Counters(queryKey); got != counters {
		t.Fatalf("graph counters = %+v after concurrent abort, want %+v", got, counters)
	}
	if harness.callerState.Load() != 0 {
		t.Fatalf("caller state = %d after concurrent abort", harness.callerState.Load())
	}
	wantAbortCalls := int32(1)
	if phase == "verifier" {
		wantAbortCalls = 0
	}
	if harness.abortCalls.Load() != wantAbortCalls {
		t.Fatalf("caller abort calls = %d, want %d", harness.abortCalls.Load(), wantAbortCalls)
	}
	warm.Abort()
}

func TestConcurrentAbortDuringPreparedReplacementPublicationPublishesNothing(t *testing.T) {
	for _, abortWith := range []string{"session", "prepared"} {
		for _, phase := range []string{"verifier", "preparer", "publish", "complete"} {
			t.Run(abortWith+"/"+phase, func(t *testing.T) {
				requireConcurrentPreparedAbortPublishesNothing(t, abortWith, phase)
			})
		}
	}
}

func requireConcurrentPreparedAbortPublishesNothing(t *testing.T, abortWith, phase string) {
	t.Helper()
	inputKey := NewInputKey("input")
	queryKey := NewQueryKey("query")
	graph := mustGraph(t, Definition{Key: queryKey, Run: readInputQuery(inputKey)})
	initial := mustBegin(t, graph)
	mustApply(t, initial, exactInput(inputKey, "r1", "initial"))
	mustEvaluate(t, initial, queryKey)
	mustCommit(t, initial)
	generation := graph.Generation()
	counters := graph.Counters(queryKey)

	replacement := mustColdSession(t, graph, exactInput(inputKey, "r2", "candidate"))
	mustEvaluate(t, replacement, queryKey)
	prepared, err := replacement.PrepareGraphCommit(t.Context())
	if err != nil {
		t.Fatalf("PrepareGraphCommit() error = %v", err)
	}
	harness := newPublicationPhaseHarness()
	go func() {
		harness.finished <- prepared.PublishWithPreparedPublisher(
			t.Context(),
			harness.verifier(phase),
			harness.preparer(phase),
		)
	}()
	awaitPublicationPhase(t, harness.entered, harness.release)
	abortDone := make(chan error, 1)
	go func() {
		if abortWith == "prepared" {
			abortDone <- prepared.Abort()
			return
		}
		replacement.Abort()
		abortDone <- nil
	}()
	awaitPreparedPublicationAbort(t, abortDone, harness.release)
	close(harness.release)
	err = awaitPublicationResult(t, harness.finished)

	if !errors.Is(err, ErrSessionClosed) {
		t.Fatalf("PublishWithPreparedPublisher() error = %v, want %v", err, ErrSessionClosed)
	}
	if graph.Generation() != generation || stringValue(t, graph, queryKey) != "initial" {
		t.Fatal("concurrent replacement abort changed the graph")
	}
	if got := graph.Counters(queryKey); got != counters {
		t.Fatalf("graph counters = %+v after concurrent abort, want %+v", got, counters)
	}
	if harness.callerState.Load() != 0 {
		t.Fatalf("caller state = %d after concurrent abort", harness.callerState.Load())
	}
	wantAbortCalls := int32(1)
	if phase == "verifier" {
		wantAbortCalls = 0
	}
	if harness.abortCalls.Load() != wantAbortCalls {
		t.Fatalf("caller abort calls = %d, want %d", harness.abortCalls.Load(), wantAbortCalls)
	}
	if err := prepared.Abort(); !errors.Is(err, ErrSessionClosed) {
		t.Fatalf("second Abort() error = %v, want %v", err, ErrSessionClosed)
	}
	if err := prepared.ValidateAuthentication(); !errors.Is(err, ErrSessionClosed) {
		t.Fatalf("ValidateAuthentication() error = %v, want %v", err, ErrSessionClosed)
	}
}

func blockPublicationPhase(target, current string, entered, release chan struct{}) {
	if target != current {
		return
	}
	close(entered)
	<-release
}

func awaitPublicationPhase(t *testing.T, entered, release chan struct{}) {
	t.Helper()
	select {
	case <-entered:
	case <-time.After(time.Second):
		close(release)
		t.Fatal("publication did not reach the selected phase")
	}
}

func awaitPublicationAbort(t *testing.T, aborted <-chan struct{}, release chan struct{}) {
	t.Helper()
	select {
	case <-aborted:
	case <-time.After(time.Second):
		close(release)
		t.Fatal("Session.Abort() blocked during publication")
	}
}

func awaitPreparedPublicationAbort(t *testing.T, aborted <-chan error, release chan struct{}) {
	t.Helper()
	select {
	case err := <-aborted:
		if err != nil {
			close(release)
			t.Fatalf("Abort() error = %v", err)
		}
	case <-time.After(time.Second):
		close(release)
		t.Fatal("Abort() blocked during prepared publication")
	}
}

func awaitPublicationResult(t *testing.T, finished <-chan error) error {
	t.Helper()
	select {
	case err := <-finished:
		return err
	case <-time.After(time.Second):
		t.Fatal("publication did not finish after abort")
		return nil
	}
}
