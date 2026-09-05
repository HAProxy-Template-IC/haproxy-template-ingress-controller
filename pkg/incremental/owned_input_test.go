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

package incremental

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
)

func TestOwnedInputReaderTransfersDetachedDependencySnapshot(t *testing.T) {
	inputKey := NewInputKey("input")
	queryKey := NewQueryKey("query")
	var transferred []byte
	graph := mustGraph(t, Definition{Key: queryKey, Run: func(_ context.Context, reader Reader) ([]byte, error) {
		owned, ok := reader.(OwnedInputReader)
		if !ok {
			t.Fatal("query reader has no owned-input protocol")
		}
		input, err := owned.ExactInputOwned(inputKey)
		if err != nil {
			return nil, err
		}
		if transferred == nil {
			transferred = input.Value
			transferred[0] = 'X'
		}
		return []byte("done"), nil
	}})

	session := mustBegin(t, graph)
	mustApply(t, session, exactInput(inputKey, "r1", "original"))
	mustEvaluate(t, session, queryKey)
	mustCommit(t, session)

	readback := mustBegin(t, graph)
	stored, exists, err := readback.ExactInput(inputKey)
	if err != nil || !exists {
		t.Fatalf("ExactInput() = %#v, %v, %v", stored, exists, err)
	}
	if string(stored.Value) != "original" {
		t.Fatalf("owned caller poisoned graph input: %q", stored.Value)
	}
	transferredBeforeChange := append([]byte(nil), transferred...)
	readback.Abort()

	changed := mustBegin(t, graph)
	mustApply(t, changed, exactInput(inputKey, "r2", "changed"))
	mustEvaluate(t, changed, queryKey)
	mustCommit(t, changed)
	if !bytes.Equal(transferred, transferredBeforeChange) {
		t.Fatalf("later session mutation changed transferred input: %q", transferred)
	}
}

func TestOwnedInputReaderIsAvailableInBatchQueries(t *testing.T) {
	var _ OwnedInputReader = (*queryReader)(nil)
	var _ OwnedInputReader = (*batchQueryReader)(nil)
	var _ ExactInputObserver = (*queryReader)(nil)
	var _ ExactInputObserver = (*batchQueryReader)(nil)
	var _ ExactInputValueObserver = (*queryReader)(nil)
	var _ ExactInputValueObserver = (*batchQueryReader)(nil)
	var _ ExactImmutableInputObserver = (*queryReader)(nil)
	var _ ExactImmutableInputObserver = (*batchQueryReader)(nil)
	var _ ExactImmutableInputObserver = ColdExactBatchQuery{}
}

func TestExactInputObserverRecordsOnlyMatchingDependency(t *testing.T) {
	inputKey := NewInputKey("input")
	fullKey := NewQueryKey("full")
	observedKey := NewQueryKey("observed")
	expected := InputRevision{Key: inputKey, Revision: NewRevision("r1"), Found: true}
	graph := mustGraph(t,
		Definition{Key: fullKey, Run: func(_ context.Context, reader Reader) ([]byte, error) {
			input, err := reader.ExactInput(inputKey)
			return input.Value, err
		}},
		Definition{Key: observedKey, Run: func(_ context.Context, reader Reader) ([]byte, error) {
			observer, ok := reader.(ExactInputObserver)
			if !ok {
				t.Fatal("query reader has no exact-input observation protocol")
			}
			if err := observer.ObserveExactInput(expected); err != nil {
				return nil, err
			}
			return []byte("observed"), nil
		}},
	)

	session := mustBegin(t, graph)
	mustApply(t, session, exactInput(inputKey, "r1", "value"))
	mustEvaluate(t, session, fullKey)
	mustEvaluate(t, session, observedKey)
	var verified []InputRevision
	err := session.Commit(context.Background(), func(_ context.Context, inputs []InputRevision) (bool, error) {
		verified = append([]InputRevision(nil), inputs...)
		return true, nil
	})
	if err != nil {
		t.Fatalf("Commit() error = %v", err)
	}
	if len(verified) != 1 || verified[0] != expected {
		t.Fatalf("verified inputs = %#v, want %#v", verified, expected)
	}
}

func TestExactInputObserverRejectsMismatchedIdentity(t *testing.T) {
	inputKey := NewInputKey("input")
	queryKey := NewQueryKey("query")
	expected := InputRevision{Key: inputKey, Revision: NewRevision("stale"), Found: true}
	graph := mustGraph(t, Definition{Key: queryKey, Run: func(_ context.Context, reader Reader) ([]byte, error) {
		observer := reader.(ExactInputObserver)
		return nil, observer.ObserveExactInput(expected)
	}})

	session := mustBegin(t, graph)
	mustApply(t, session, exactInput(inputKey, "current", "value"))
	if _, err := session.Evaluate(context.Background(), queryKey); !errors.Is(err, ErrRevisionConflict) {
		t.Fatalf("Evaluate() error = %v, want %v", err, ErrRevisionConflict)
	}
	if err := session.Commit(context.Background(), acceptRevisions); !errors.Is(err, ErrRevisionConflict) {
		t.Fatalf("Commit() error = %v, want failed observation", err)
	}
}

func TestExactInputValueObserverRejectsSameRevisionWithDifferentBytes(t *testing.T) {
	inputKey := NewInputKey("input")
	queryKey := NewQueryKey("query")
	expected := exactInput(inputKey, "revision", "poison")
	graph := mustGraph(t, Definition{Key: queryKey, Run: func(_ context.Context, reader Reader) ([]byte, error) {
		observer := reader.(ExactInputValueObserver)
		return nil, observer.ObserveExactInputValue(expected)
	}})

	session := mustBegin(t, graph)
	mustApply(t, session, exactInput(inputKey, "revision", "value"))
	if _, err := session.Evaluate(context.Background(), queryKey); !errors.Is(err, ErrRevisionConflict) {
		t.Fatalf("Evaluate() error = %v, want %v", err, ErrRevisionConflict)
	}
}

func TestExactInputValueObserverRecordsMatchingBatchDependency(t *testing.T) {
	inputKey := NewInputKey("input")
	queryKey := NewQueryKey("query")
	expected := exactInput(inputKey, "revision", "value")
	graph := mustGraph(t, Definition{Key: queryKey, Run: func(context.Context, Reader) ([]byte, error) {
		return nil, errors.New("batch query ran serially")
	}})
	session := mustBegin(t, graph)
	mustApply(t, session, expected)

	results, err := session.EvaluateAllExactBatch(t.Context(), func(
		_ context.Context,
		queries []BatchQuery,
	) ([]ExactBatchValue, error) {
		observer := queries[0].Reader.(ExactInputValueObserver)
		if err := observer.ObserveExactInputValue(expected); err != nil {
			return nil, err
		}
		root, err := queries[0].NewExactValue("result")
		return []ExactBatchValue{{Value: root, Err: err}}, nil
	}, queryKey)
	if err != nil || len(results) != 1 {
		t.Fatalf("EvaluateAllExactBatch() = %#v, %v", results, err)
	}
	if err := session.Commit(context.Background(), acceptRevisions); err != nil {
		t.Fatalf("Commit() error = %v", err)
	}
}

func TestExactImmutableInputObserverValidatesIdentityAndValue(t *testing.T) {
	inputKey := NewInputKey("input")
	revision := NewRevision("revision")
	tests := []struct {
		name         string
		expected     ImmutableInput
		wantConflict bool
		wantError    string
	}{
		{
			name: "matching binary value",
			expected: ImmutableInput{
				Key: inputKey, Revision: revision, Found: true, Value: "value\x00\xff",
			},
		},
		{
			name: "empty key",
			expected: ImmutableInput{
				Revision: revision, Found: true, Value: "value\x00\xff",
			},
			wantError: "input key is empty",
		},
		{
			name: "empty revision",
			expected: ImmutableInput{
				Key: inputKey, Found: true, Value: "value\x00\xff",
			},
			wantError: "input revision is empty",
		},
		{
			name: "negative value has bytes",
			expected: ImmutableInput{
				Key: inputKey, Revision: revision, Value: "poison",
			},
			wantError: "negative input has bytes",
		},
		{
			name: "stale revision",
			expected: ImmutableInput{
				Key: inputKey, Revision: NewRevision("stale"), Found: true, Value: "value\x00\xff",
			},
			wantConflict: true,
		},
		{
			name: "different bytes under exact revision",
			expected: ImmutableInput{
				Key: inputKey, Revision: revision, Found: true, Value: "poison",
			},
			wantConflict: true,
		},
		{
			name: "different presence under exact revision",
			expected: ImmutableInput{
				Key: inputKey, Revision: revision,
			},
			wantConflict: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			queryKey := NewQueryKey("query")
			graph := mustGraph(t, Definition{Key: queryKey, Run: func(_ context.Context, reader Reader) ([]byte, error) {
				observer := reader.(ExactImmutableInputObserver)
				return nil, observer.ObserveExactImmutableInput(test.expected)
			}})
			session := mustBegin(t, graph)
			mustApply(t, session, Input{
				Key: inputKey, Revision: revision, Found: true, Value: []byte("value\x00\xff"),
			})
			_, err := session.Evaluate(t.Context(), queryKey)
			switch {
			case test.wantConflict:
				if !errors.Is(err, ErrRevisionConflict) {
					t.Fatalf("Evaluate() error = %v, want %v", err, ErrRevisionConflict)
				}
			case test.wantError != "":
				if err == nil || !strings.Contains(err.Error(), test.wantError) {
					t.Fatalf("Evaluate() error = %v, want %q", err, test.wantError)
				}
			default:
				if err != nil {
					t.Fatalf("Evaluate() error = %v", err)
				}
			}
			session.Abort()
		})
	}
}

func TestExactImmutableInputObserverAcceptsExactNegativeInput(t *testing.T) {
	inputKey := NewInputKey("input")
	revision := NewRevision("revision")
	queryKey := NewQueryKey("query")
	graph := mustGraph(t, Definition{Key: queryKey, Run: func(_ context.Context, reader Reader) ([]byte, error) {
		observer := reader.(ExactImmutableInputObserver)
		return nil, observer.ObserveExactImmutableInput(ImmutableInput{
			Key: inputKey, Revision: revision,
		})
	}})
	session := mustBegin(t, graph)
	mustApply(t, session, Input{Key: inputKey, Revision: revision})
	if _, err := session.Evaluate(t.Context(), queryKey); err != nil {
		t.Fatalf("Evaluate() error = %v", err)
	}
	session.Abort()
}

func TestExactImmutableInputObserverRecordsMatchingBatchDependency(t *testing.T) {
	inputKey := NewInputKey("input")
	queryKey := NewQueryKey("query")
	revision := NewRevision("revision")
	expected := ImmutableInput{Key: inputKey, Revision: revision, Found: true, Value: "value"}
	graph := mustGraph(t, Definition{Key: queryKey, Run: func(context.Context, Reader) ([]byte, error) {
		return nil, errors.New("batch query ran serially")
	}})
	session := mustBegin(t, graph)
	mustApply(t, session, Input{Key: inputKey, Revision: revision, Found: true, Value: []byte("value")})

	results, err := session.EvaluateAllExactBatch(t.Context(), func(
		_ context.Context,
		queries []BatchQuery,
	) ([]ExactBatchValue, error) {
		observer := queries[0].Reader.(ExactImmutableInputObserver)
		if err := observer.ObserveExactImmutableInput(expected); err != nil {
			return nil, err
		}
		root, err := queries[0].NewExactValue("result")
		return []ExactBatchValue{{Value: root, Err: err}}, nil
	}, queryKey)
	if err != nil || len(results) != 1 {
		t.Fatalf("EvaluateAllExactBatch() = %#v, %v", results, err)
	}
	if err := session.Commit(context.Background(), acceptRevisions); err != nil {
		t.Fatalf("Commit() error = %v", err)
	}
	if !graph.HasInputDependents(inputKey) {
		t.Fatal("immutable input observation did not record the batch dependency")
	}
}

func TestExactImmutableInputObserverAllocatesNoSnapshot(t *testing.T) {
	inputKey := NewInputKey("input")
	revision := NewRevision("revision")
	session := &Session{inputChanges: map[InputKey]inputEntry{
		inputKey: {revision: revision, found: true, value: make([]byte, 32*1024)},
	}}
	reader := &queryReader{session: session, frame: newDependencyFrame(), ctx: t.Context()}
	expected := ImmutableInput{Key: inputKey, Revision: revision, Found: true, Value: string(make([]byte, 32*1024))}
	if err := reader.ObserveExactImmutableInput(expected); err != nil {
		t.Fatalf("ObserveExactImmutableInput() error = %v", err)
	}
	allocations := testing.AllocsPerRun(1000, func() {
		if err := reader.ObserveExactImmutableInput(expected); err != nil {
			panic(err)
		}
	})
	if allocations != 0 {
		t.Fatalf("ObserveExactImmutableInput() allocations = %v, want 0", allocations)
	}
}

func TestSessionMatchesExactInputWithoutSharingBytes(t *testing.T) {
	inputKey := NewInputKey("input")
	session := mustBegin(t, mustGraph(t))
	mustApply(t, session, exactInput(inputKey, "revision", "value"))

	expected := exactInput(inputKey, "revision", "value")
	matched, err := session.MatchesExactInput(expected)
	if err != nil || !matched {
		t.Fatalf("MatchesExactInput() = %t, %v", matched, err)
	}
	expected.Value[0] = 'X'
	matched, err = session.MatchesExactInput(expected)
	if err != nil || matched {
		t.Fatalf("MatchesExactInput(mutated) = %t, %v", matched, err)
	}
	stored, exists, err := session.ExactInput(inputKey)
	if err != nil || !exists || string(stored.Value) != "value" {
		t.Fatalf("ExactInput() = %#v, %t, %v", stored, exists, err)
	}
	session.Abort()
}

func BenchmarkExactInputObservation(b *testing.B) {
	inputKey := NewInputKey("input")
	revision := NewRevision("revision")
	session := &Session{inputChanges: map[InputKey]inputEntry{
		inputKey: {
			revision: revision,
			found:    true,
			value:    make([]byte, 32*1024),
		},
	}}
	expected := InputRevision{Key: inputKey, Revision: revision, Found: true}

	b.Run("detached bytes", func(b *testing.B) {
		reader := &queryReader{session: session, frame: newDependencyFrame(), ctx: b.Context()}
		b.ReportAllocs()
		for range b.N {
			input, err := reader.ExactInputOwned(inputKey)
			if err != nil || len(input.Value) == 0 {
				b.Fatal(err)
			}
		}
	})
	b.Run("exact identity", func(b *testing.B) {
		reader := &queryReader{session: session, frame: newDependencyFrame(), ctx: b.Context()}
		b.ReportAllocs()
		for range b.N {
			if err := reader.ObserveExactInput(expected); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("exact value", func(b *testing.B) {
		benchmarkObserveExactInputValue(b, session, inputKey, revision)
	})
	b.Run("exact immutable value", func(b *testing.B) {
		benchmarkObserveExactImmutableInput(b, session, inputKey, revision)
	})
}

func benchmarkObserveExactInputValue(b *testing.B, session *Session, inputKey InputKey, revision Revision) {
	b.Helper()
	reader := &queryReader{session: session, frame: newDependencyFrame(), ctx: b.Context()}
	expectedValue := Input{Key: inputKey, Revision: revision, Found: true, Value: make([]byte, 32*1024)}
	b.ReportAllocs()
	for range b.N {
		if err := reader.ObserveExactInputValue(expectedValue); err != nil {
			b.Fatal(err)
		}
	}
}

func benchmarkObserveExactImmutableInput(b *testing.B, session *Session, inputKey InputKey, revision Revision) {
	b.Helper()
	reader := &queryReader{session: session, frame: newDependencyFrame(), ctx: b.Context()}
	expectedValue := ImmutableInput{
		Key: inputKey, Revision: revision, Found: true, Value: string(make([]byte, 32*1024)),
	}
	b.ReportAllocs()
	for range b.N {
		if err := reader.ObserveExactImmutableInput(expectedValue); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkSessionExactInputMatch(b *testing.B) {
	inputKey := NewInputKey("input")
	revision := NewRevision("revision")
	value := make([]byte, 32*1024)
	session := &Session{inputChanges: map[InputKey]inputEntry{
		inputKey: {revision: revision, found: true, value: value},
	}}
	expected := Input{Key: inputKey, Revision: revision, Found: true, Value: append([]byte(nil), value...)}

	b.Run("detached bytes", func(b *testing.B) {
		b.ReportAllocs()
		for range b.N {
			input, exists, err := session.ExactInput(inputKey)
			if err != nil || !exists || len(input.Value) == 0 {
				b.Fatal(err)
			}
		}
	})
	b.Run("exact match", func(b *testing.B) {
		b.ReportAllocs()
		for range b.N {
			matched, err := session.MatchesExactInput(expected)
			if err != nil || !matched {
				b.Fatal(err)
			}
		}
	})
}
