package incremental

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"gitlab.com/haproxy-haptic/haptic/pkg/incremental/internal/orderedset"
)

func TestPreparedGraphCommitDefersPublicationUntilExactCAS(t *testing.T) {
	inputKey := NewInputKey("input")
	queryKey := NewQueryKey("query")
	graph := mustGraph(t, Definition{Key: queryKey, Run: readInputQuery(inputKey)})
	session := mustColdSession(t, graph, exactInput(inputKey, "revision-1", "prepared"))
	mustEvaluate(t, session, queryKey)
	prepared, err := session.PrepareGraphCommit(t.Context())
	if err != nil {
		t.Fatalf("PrepareGraphCommit() error = %v", err)
	}
	if err := prepared.ValidateAuthentication(); err != nil {
		t.Fatalf("ValidateAuthentication() error = %v", err)
	}
	if err := prepared.ValidateFor(session); err != nil {
		t.Fatalf("ValidateFor() error = %v", err)
	}
	foreign := mustColdSession(t, graph, exactInput(inputKey, "foreign", "foreign"))
	if err := prepared.ValidateFor(foreign); err == nil || !strings.Contains(err.Error(), "another session") {
		t.Fatalf("foreign ValidateFor() error = %v", err)
	}
	foreign.Abort()
	if graph.Generation() != 0 {
		t.Fatalf("prepared draft changed generation to %d", graph.Generation())
	}
	if _, exists := graph.Value(queryKey); exists {
		t.Fatal("prepared draft published a query")
	}
	if err := session.ApplyInputs(exactInput(inputKey, "revision-2", "poison")); !errors.Is(err, ErrSessionClosed) {
		t.Fatalf("mutation after preparation error = %v, want %v", err, ErrSessionClosed)
	}

	var verifierCalls atomic.Int32
	err = prepared.Publish(t.Context(), func(_ context.Context, observations []InputRevision) (bool, error) {
		verifierCalls.Add(1)
		want := InputRevision{Key: inputKey, Revision: NewRevision("revision-1"), Found: true}
		if len(observations) != 1 || observations[0] != want {
			return false, fmt.Errorf("observations = %#v, want %#v", observations, want)
		}
		return true, nil
	})
	if err != nil {
		t.Fatalf("Publish() error = %v", err)
	}
	if verifierCalls.Load() != 1 || graph.Generation() != 1 || stringValue(t, graph, queryKey) != "prepared" {
		t.Fatal("prepared generation was not published exactly once")
	}
	if err := prepared.ValidateAuthentication(); !errors.Is(err, ErrSessionClosed) {
		t.Fatalf("published ValidateAuthentication() error = %v, want %v", err, ErrSessionClosed)
	}
	if err := prepared.Publish(t.Context(), func(context.Context, []InputRevision) (bool, error) {
		verifierCalls.Add(1)
		return true, nil
	}); !errors.Is(err, ErrSessionClosed) {
		t.Fatalf("second Publish() error = %v, want %v", err, ErrSessionClosed)
	}
	if verifierCalls.Load() != 1 {
		t.Fatalf("second publication called verifier %d times", verifierCalls.Load())
	}
}

func TestPreparedGraphCommitRejectsStaleAndSubstitutedBaseWithoutVerification(t *testing.T) {
	inputKey := NewInputKey("input")
	queryKey := NewQueryKey("query")
	graph := mustGraph(t, Definition{Key: queryKey, Run: readInputQuery(inputKey)})
	firstSession := mustColdSession(t, graph, exactInput(inputKey, "revision-1", "first"))
	secondSession := mustColdSession(t, graph, exactInput(inputKey, "revision-2", "second"))
	mustEvaluate(t, firstSession, queryKey)
	mustEvaluate(t, secondSession, queryKey)
	first, err := firstSession.PrepareGraphCommit(t.Context())
	if err != nil {
		t.Fatalf("first PrepareGraphCommit() error = %v", err)
	}
	second, err := secondSession.PrepareGraphCommit(t.Context())
	if err != nil {
		t.Fatalf("second PrepareGraphCommit() error = %v", err)
	}
	if err := first.Publish(t.Context(), acceptRevisions); err != nil {
		t.Fatalf("first Publish() error = %v", err)
	}
	var verifierCalls atomic.Int32
	err = second.Publish(t.Context(), func(context.Context, []InputRevision) (bool, error) {
		verifierCalls.Add(1)
		return true, nil
	})
	if !errors.Is(err, ErrCommitConflict) {
		t.Fatalf("stale Publish() error = %v, want %v", err, ErrCommitConflict)
	}
	if verifierCalls.Load() != 0 {
		t.Fatalf("stale publication called verifier %d times", verifierCalls.Load())
	}
	if stringValue(t, graph, queryKey) != "first" {
		t.Fatal("stale publication replaced the winner")
	}

	otherGraph := mustGraph(t, Definition{Key: queryKey, Run: readInputQuery(inputKey)})
	otherSession := mustColdSession(t, otherGraph, exactInput(inputKey, "revision", "candidate"))
	mustEvaluate(t, otherSession, queryKey)
	prepared, err := otherSession.PrepareGraphCommit(t.Context())
	if err != nil {
		t.Fatalf("PrepareGraphCommit() error = %v", err)
	}
	otherGraph.mu.Lock()
	substituted, generationErr := newGraphGeneration(
		otherGraph,
		prepared.prepared.baseGeneration,
		map[InputKey]inputEntry{},
		map[QueryKey]nodeEntry{},
		map[dependencyKey]orderedset.Root{},
		map[QueryKey]struct{}{},
		map[QueryKey]NodeCounters{},
	)
	if generationErr == nil {
		otherGraph.installGenerationLocked(substituted)
	}
	otherGraph.mu.Unlock()
	if generationErr != nil {
		t.Fatalf("newGraphGeneration() error = %v", generationErr)
	}
	verifierCalls.Store(0)
	err = prepared.Publish(t.Context(), func(context.Context, []InputRevision) (bool, error) {
		verifierCalls.Add(1)
		return true, nil
	})
	if !errors.Is(err, ErrCommitConflict) {
		t.Fatalf("same-number substituted-base Publish() error = %v, want %v", err, ErrCommitConflict)
	}
	if verifierCalls.Load() != 0 || otherGraph.Generation() != 0 {
		t.Fatal("same-number substituted base passed exact identity CAS")
	}
}

func TestPreparedGraphCommitRejectsSubstitutedGeneration(t *testing.T) {
	inputKey := NewInputKey("input")
	queryKey := NewQueryKey("query")
	firstGraph := mustGraph(t, Definition{Key: queryKey, Run: readInputQuery(inputKey)})
	firstSession := mustColdSession(t, firstGraph, exactInput(inputKey, "first", "first"))
	mustEvaluate(t, firstSession, queryKey)
	first, err := firstSession.PrepareGraphCommit(t.Context())
	if err != nil {
		t.Fatalf("first PrepareGraphCommit() error = %v", err)
	}
	secondGraph := mustGraph(t, Definition{Key: queryKey, Run: readInputQuery(inputKey)})
	secondSession := mustColdSession(t, secondGraph, exactInput(inputKey, "second", "second"))
	mustEvaluate(t, secondSession, queryKey)
	second, err := secondSession.PrepareGraphCommit(t.Context())
	if err != nil {
		t.Fatalf("second PrepareGraphCommit() error = %v", err)
	}
	first.prepared.generation = second.prepared.generation
	if err := first.ValidateAuthentication(); err == nil || !strings.Contains(err.Error(), "transaction provenance") {
		t.Fatalf("substituted generation validation error = %v", err)
	}
	if err := first.Publish(t.Context(), acceptRevisions); err == nil {
		t.Fatal("substituted generation was published")
	}
	if firstGraph.Generation() != 0 {
		t.Fatal("substituted generation changed the first graph")
	}
	firstSession.Abort()
	if err := second.Abort(); err != nil {
		t.Fatalf("second Abort() error = %v", err)
	}
}

func TestPreparedGraphCommitFailureCancellationAndPanicsPublishNothing(t *testing.T) {
	tests := []struct {
		name string
		run  func(context.Context, context.CancelFunc, PreparedGraphCommit, *atomic.Bool) error
		want string
	}{
		{
			name: "revision conflict",
			run: func(ctx context.Context, _ context.CancelFunc, prepared PreparedGraphCommit, _ *atomic.Bool) error {
				return prepared.Publish(ctx, func(context.Context, []InputRevision) (bool, error) {
					return false, nil
				})
			},
			want: ErrRevisionConflict.Error(),
		},
		{
			name: "verifier panic",
			run: func(ctx context.Context, _ context.CancelFunc, prepared PreparedGraphCommit, _ *atomic.Bool) error {
				return prepared.Publish(ctx, func(context.Context, []InputRevision) (bool, error) {
					panic("verifier")
				})
			},
			want: "verifier panicked",
		},
		{
			name: "preparer panic",
			run: func(ctx context.Context, _ context.CancelFunc, prepared PreparedGraphCommit, _ *atomic.Bool) error {
				return prepared.PublishWithPreparedPublisher(ctx, acceptRevisions, func([]InputKey) (CommitPublication, error) {
					panic("preparer")
				})
			},
			want: "preparation panicked",
		},
		{
			name: "canceled preparation",
			run: func(ctx context.Context, cancel context.CancelFunc, prepared PreparedGraphCommit, aborted *atomic.Bool) error {
				return prepared.PublishWithPreparedPublisher(ctx, acceptRevisions, func([]InputKey) (CommitPublication, error) {
					cancel()
					return CommitPublication{
						Publish: func() { panic("published after cancellation") },
						Abort:   func() { aborted.Store(true) },
					}, nil
				})
			},
			want: context.Canceled.Error(),
		},
		{
			name: "publisher panic",
			run: func(ctx context.Context, _ context.CancelFunc, prepared PreparedGraphCommit, aborted *atomic.Bool) error {
				return prepared.PublishWithPreparedPublisher(ctx, acceptRevisions, func([]InputKey) (CommitPublication, error) {
					return CommitPublication{
						Publish: func() { panic("publisher") },
						Abort:   func() { aborted.Store(true) },
					}, nil
				})
			},
			want: "publication publish panicked",
		},
		{
			name: "completion panic",
			run: func(ctx context.Context, _ context.CancelFunc, prepared PreparedGraphCommit, aborted *atomic.Bool) error {
				return prepared.PublishWithPreparedPublisher(ctx, acceptRevisions, func([]InputKey) (CommitPublication, error) {
					return CommitPublication{
						Complete: func() { panic("complete") },
						Abort:    func() { aborted.Store(true) },
					}, nil
				})
			},
			want: "publication complete panicked",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			runPreparedGraphCommitFailure(t, test.name, test.run, test.want)
		})
	}
}

func runPreparedGraphCommitFailure(
	t *testing.T,
	name string,
	run func(context.Context, context.CancelFunc, PreparedGraphCommit, *atomic.Bool) error,
	want string,
) {
	t.Helper()
	inputKey := NewInputKey("input")
	queryKey := NewQueryKey("query")
	graph := mustGraph(t, Definition{Key: queryKey, Run: readInputQuery(inputKey)})
	session := mustColdSession(t, graph, exactInput(inputKey, "revision", "candidate"))
	mustEvaluate(t, session, queryKey)
	prepared, err := session.PrepareGraphCommit(t.Context())
	if err != nil {
		t.Fatalf("PrepareGraphCommit() error = %v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	var aborted atomic.Bool
	err = run(ctx, cancel, prepared, &aborted)
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("Publish() error = %v, want %q", err, want)
	}
	if graph.Generation() != 0 {
		t.Fatalf("failed publication changed generation to %d", graph.Generation())
	}
	if _, exists := graph.Value(queryKey); exists {
		t.Fatal("failed publication stored a query")
	}
	if strings.Contains(name, "panic") || name == "canceled preparation" {
		if !aborted.Load() && name != "verifier panic" && name != "preparer panic" {
			t.Fatal("prepared caller state was not aborted")
		}
	}
	if err := prepared.ValidateAuthentication(); !errors.Is(err, ErrSessionClosed) {
		t.Fatalf("failed draft validation error = %v, want %v", err, ErrSessionClosed)
	}
}

func TestPreparedGraphCommitAbortAndWarmRejection(t *testing.T) {
	inputKey := NewInputKey("input")
	queryKey := NewQueryKey("query")
	graph := mustGraph(t, Definition{Key: queryKey, Run: readInputQuery(inputKey)})
	session := mustColdSession(t, graph, exactInput(inputKey, "revision-1", "candidate"))
	mustEvaluate(t, session, queryKey)
	prepared, err := session.PrepareGraphCommit(t.Context())
	if err != nil {
		t.Fatalf("PrepareGraphCommit() error = %v", err)
	}
	if err := prepared.Abort(); err != nil {
		t.Fatalf("Abort() error = %v", err)
	}
	if graph.Generation() != 0 {
		t.Fatal("aborted draft changed the graph")
	}
	if err := prepared.Publish(t.Context(), acceptRevisions); !errors.Is(err, ErrSessionClosed) {
		t.Fatalf("aborted Publish() error = %v, want %v", err, ErrSessionClosed)
	}

	initial := mustBegin(t, graph)
	mustApply(t, initial, exactInput(inputKey, "revision-2", "initial"))
	mustEvaluate(t, initial, queryKey)
	mustCommit(t, initial)
	warm := mustBegin(t, graph)
	mustApply(t, warm, exactInput(inputKey, "revision-3", "warm"))
	mustEvaluate(t, warm, queryKey)
	if _, err := warm.PrepareGraphCommit(t.Context()); err == nil ||
		!strings.Contains(err.Error(), "requires a replacement transaction") {
		t.Fatalf("warm PrepareGraphCommit() error = %v", err)
	}
	if graph.Generation() != 1 || stringValue(t, graph, queryKey) != "initial" {
		t.Fatal("rejected warm draft changed the graph")
	}
}

type sessionAbortReentrancyPublisher func(
	context.Context,
	*Session,
	PreparedGraphCommit,
	*atomic.Bool,
) error

func publishSessionAbortInVerifier(
	ctx context.Context, session *Session, prepared PreparedGraphCommit, _ *atomic.Bool,
) error {
	return prepared.Publish(ctx, func(context.Context, []InputRevision) (bool, error) {
		session.Abort()
		return true, nil
	})
}

func publishSessionAbortInPreparer(
	ctx context.Context, session *Session, prepared PreparedGraphCommit, visible *atomic.Bool,
) error {
	return prepared.PublishWithPreparedPublisher(ctx, acceptRevisions, func([]InputKey) (CommitPublication, error) {
		visible.Store(true)
		session.Abort()
		return CommitPublication{Abort: func() { visible.Store(false) }}, nil
	})
}

func publishSessionAbortInPublish(
	ctx context.Context, session *Session, prepared PreparedGraphCommit, visible *atomic.Bool,
) error {
	return prepared.PublishWithPreparedPublisher(ctx, acceptRevisions, func([]InputKey) (CommitPublication, error) {
		return CommitPublication{
			Publish: func() {
				visible.Store(true)
				session.Abort()
			},
			Abort: func() { visible.Store(false) },
		}, nil
	})
}

func publishSessionAbortInComplete(
	ctx context.Context, session *Session, prepared PreparedGraphCommit, visible *atomic.Bool,
) error {
	return prepared.PublishWithPreparedPublisher(ctx, acceptRevisions, func([]InputKey) (CommitPublication, error) {
		return CommitPublication{
			Publish: func() { visible.Store(true) },
			Complete: func() {
				session.Abort()
			},
			Abort: func() { visible.Store(false) },
		}, nil
	})
}

func publishSessionAbortInAbort(
	ctx context.Context, session *Session, prepared PreparedGraphCommit, visible *atomic.Bool,
) error {
	return prepared.PublishWithPreparedPublisher(ctx, acceptRevisions, func([]InputKey) (CommitPublication, error) {
		return CommitPublication{
			Publish: func() {
				visible.Store(true)
				panic("publish")
			},
			Abort: func() {
				visible.Store(false)
				session.Abort()
			},
		}, nil
	})
}

func TestPreparedGraphCommitSessionAbortIsCallbackReentrant(t *testing.T) {
	tests := []struct {
		name    string
		publish sessionAbortReentrancyPublisher
		panic   bool
	}{
		{name: "verifier", publish: publishSessionAbortInVerifier},
		{name: "preparer", publish: publishSessionAbortInPreparer},
		{name: "publish", publish: publishSessionAbortInPublish},
		{name: "complete", publish: publishSessionAbortInComplete},
		{name: "abort", publish: publishSessionAbortInAbort, panic: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			requireSessionAbortIsCallbackReentrant(t, test.publish, test.panic)
		})
	}
}

func requireSessionAbortIsCallbackReentrant(
	t *testing.T,
	publish sessionAbortReentrancyPublisher,
	wantPanic bool,
) {
	t.Helper()
	inputKey := NewInputKey("input")
	queryKey := NewQueryKey("query")
	graph := mustGraph(t, Definition{Key: queryKey, Run: readInputQuery(inputKey)})
	session := mustColdSession(t, graph, exactInput(inputKey, "revision", "candidate"))
	mustEvaluate(t, session, queryKey)
	prepared, err := session.PrepareGraphCommit(t.Context())
	if err != nil {
		t.Fatalf("PrepareGraphCommit() error = %v", err)
	}
	var visible atomic.Bool
	finished := make(chan error, 1)
	go func() {
		finished <- publish(t.Context(), session, prepared, &visible)
	}()
	select {
	case err = <-finished:
	case <-time.After(time.Second):
		t.Fatal("Session.Abort() callback deadlocked")
	}
	if wantPanic {
		if err == nil || !strings.Contains(err.Error(), "publication publish panicked") {
			t.Fatalf("Publish() error = %v, want publisher panic", err)
		}
	} else if !errors.Is(err, ErrSessionClosed) {
		t.Fatalf("Publish() error = %v, want %v", err, ErrSessionClosed)
	}
	if graph.Generation() != 0 {
		t.Fatalf("aborted callback changed generation to %d", graph.Generation())
	}
	if _, exists := graph.Value(queryKey); exists {
		t.Fatal("aborted callback published a query")
	}
	if visible.Load() {
		t.Fatal("aborted callback retained caller publication")
	}
	if err := prepared.ValidateAuthentication(); !errors.Is(err, ErrSessionClosed) {
		t.Fatalf("aborted draft validation error = %v, want %v", err, ErrSessionClosed)
	}
}
