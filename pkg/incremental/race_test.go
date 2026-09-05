package incremental

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
)

func TestConcurrentSessionsRace(t *testing.T) {
	inputKey := NewInputKey("input")
	queryKey := NewQueryKey("query")
	graph := mustGraph(t, Definition{Key: queryKey, Run: func(_ context.Context, reader Reader) ([]byte, error) {
		value, _, err := reader.Input(inputKey)
		return value, err
	}})
	initial := mustBegin(t, graph)
	mustApply(t, initial, exactInput(inputKey, "initial", "initial"))
	mustEvaluate(t, initial, queryKey)
	mustCommit(t, initial)

	const goroutines = 8
	const commitsPerGoroutine = 12
	errorsCh := make(chan error, goroutines)
	var wait sync.WaitGroup
	for worker := range goroutines {
		wait.Add(1)
		go func() {
			defer wait.Done()
			for iteration := range commitsPerGoroutine {
				value := fmt.Sprintf("%d/%d", worker, iteration)
				if err := commitConcurrentValue(graph, inputKey, queryKey, value); err != nil {
					errorsCh <- err
					return
				}
			}
		}()
	}
	wait.Wait()
	close(errorsCh)
	for err := range errorsCh {
		t.Errorf("concurrent session error = %v", err)
	}
	if t.Failed() {
		return
	}
	wantCommits := uint64(1 + goroutines*commitsPerGoroutine)
	if got := graph.Generation(); got != wantCommits {
		t.Fatalf("generation = %d, want %d", got, wantCommits)
	}
	if got := graph.Counters(queryKey).Executions; got != wantCommits {
		t.Fatalf("executions = %d, want %d", got, wantCommits)
	}
}

func commitConcurrentValue(graph *Graph, inputKey InputKey, queryKey QueryKey, value string) error {
	for {
		session, err := graph.Begin()
		if err != nil {
			return err
		}
		err = session.ApplyInputs(exactInput(inputKey, "revision/"+value, value))
		if errors.Is(err, ErrCommitConflict) {
			session.Abort()
			continue
		}
		if err != nil {
			return err
		}
		if _, err = session.Evaluate(context.Background(), queryKey); errors.Is(err, ErrCommitConflict) {
			session.Abort()
			continue
		}
		if err != nil {
			return err
		}
		err = session.Commit(context.Background(), acceptRevisions)
		if errors.Is(err, ErrCommitConflict) {
			continue
		}
		return err
	}
}
