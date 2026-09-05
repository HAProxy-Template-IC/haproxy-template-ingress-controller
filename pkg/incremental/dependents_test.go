package incremental

import (
	"context"
	"testing"
)

func TestGraphHasDependentsTracksCommittedDirectEdges(t *testing.T) {
	selectorKey := NewInputKey("selector")
	leftKey := NewQueryKey("left")
	rightKey := NewQueryKey("right")
	consumerKey := NewQueryKey("consumer")
	rootKey := NewQueryKey("root")
	graph := mustGraph(t,
		Definition{Key: leftKey, Run: constantQuery("left")},
		Definition{Key: rightKey, Run: constantQuery("right")},
		Definition{Key: consumerKey, Run: func(ctx context.Context, reader Reader) ([]byte, error) {
			selector, _, err := reader.Input(selectorKey)
			if err != nil {
				return nil, err
			}
			if string(selector) == "left" {
				return reader.Query(ctx, leftKey)
			}
			return reader.Query(ctx, rightKey)
		}},
		Definition{Key: rootKey, Run: func(ctx context.Context, reader Reader) ([]byte, error) {
			return reader.Query(ctx, consumerKey)
		}},
	)

	if graph.HasDependents(leftKey) || graph.HasDependents(NewQueryKey("unknown")) {
		t.Fatal("empty graph reported committed dependents")
	}
	session := mustBegin(t, graph)
	mustApply(t, session, exactInput(selectorKey, "s1", "left"))
	mustEvaluate(t, session, rootKey)
	if graph.HasDependents(leftKey) {
		t.Fatal("speculative dependency escaped before commit")
	}
	mustCommit(t, session)

	if !graph.HasDependents(leftKey) {
		t.Fatal("direct query dependent is missing")
	}
	if !graph.HasDependents(consumerKey) {
		t.Fatal("direct root dependent is missing")
	}
	if graph.HasDependents(rightKey) || graph.HasDependents(rootKey) {
		t.Fatal("graph reported a transitive or absent dependent")
	}
}

func TestGraphHasDependentsDropsChangedAndRemovedEdgesAfterCommit(t *testing.T) {
	selectorKey := NewInputKey("selector")
	leftKey := NewQueryKey("left")
	rightKey := NewQueryKey("right")
	consumerKey := NewQueryKey("consumer")
	graph := mustGraph(t,
		Definition{Key: leftKey, Run: constantQuery("left")},
		Definition{Key: rightKey, Run: constantQuery("right")},
		Definition{Key: consumerKey, Run: func(ctx context.Context, reader Reader) ([]byte, error) {
			selector, _, err := reader.Input(selectorKey)
			if err != nil {
				return nil, err
			}
			if string(selector) == "left" {
				return reader.Query(ctx, leftKey)
			}
			return reader.Query(ctx, rightKey)
		}},
	)

	session := mustBegin(t, graph)
	mustApply(t, session, exactInput(selectorKey, "s1", "left"))
	mustEvaluate(t, session, consumerKey)
	mustCommit(t, session)

	session = mustBegin(t, graph)
	mustApply(t, session, exactInput(selectorKey, "s2", "right"))
	mustEvaluate(t, session, consumerKey)
	if !graph.HasDependents(leftKey) || graph.HasDependents(rightKey) {
		t.Fatal("speculative dependency change mutated committed edges")
	}
	mustCommit(t, session)
	if graph.HasDependents(leftKey) || !graph.HasDependents(rightKey) {
		t.Fatal("committed dependency change did not replace its direct edge")
	}

	session = mustBegin(t, graph)
	if err := session.RemoveQueries(consumerKey); err != nil {
		t.Fatalf("RemoveQueries() error = %v", err)
	}
	if !graph.HasDependents(rightKey) {
		t.Fatal("speculative removal mutated committed edges")
	}
	mustCommit(t, session)
	if graph.HasDependents(rightKey) {
		t.Fatal("removed query retained its dependency edge")
	}
}

func TestGraphHasInputDependentsTracksOnlyCommittedEdges(t *testing.T) {
	leftInput := NewInputKey("left")
	rightInput := NewInputKey("right")
	selectorInput := NewInputKey("selector")
	consumerKey := NewQueryKey("consumer")
	graph := mustGraph(t, Definition{Key: consumerKey, Run: func(_ context.Context, reader Reader) ([]byte, error) {
		selector, _, err := reader.Input(selectorInput)
		if err != nil {
			return nil, err
		}
		if string(selector) == "left" {
			value, _, inputErr := reader.Input(leftInput)
			return value, inputErr
		}
		value, _, inputErr := reader.Input(rightInput)
		return value, inputErr
	}})

	session := mustBegin(t, graph)
	mustApply(t, session,
		exactInput(selectorInput, "selector-1", "left"),
		exactInput(leftInput, "left-1", "left"),
		exactInput(rightInput, "right-1", "right"),
	)
	mustEvaluate(t, session, consumerKey)
	if graph.HasInputDependents(leftInput) || graph.HasInputDependents(rightInput) {
		t.Fatal("speculative input dependency escaped before commit")
	}
	mustCommit(t, session)
	if !graph.HasInputDependents(leftInput) || graph.HasInputDependents(rightInput) {
		t.Fatal("committed input dependency was not published exactly")
	}

	session = mustBegin(t, graph)
	mustApply(t, session, exactInput(selectorInput, "selector-2", "right"))
	mustEvaluate(t, session, consumerKey)
	if !graph.HasInputDependents(leftInput) || graph.HasInputDependents(rightInput) {
		t.Fatal("speculative input dependency replacement mutated committed edges")
	}
	mustCommit(t, session)
	if graph.HasInputDependents(leftInput) || !graph.HasInputDependents(rightInput) {
		t.Fatal("committed input dependency replacement was not published exactly")
	}

	session = mustBegin(t, graph)
	if err := session.RemoveQueries(consumerKey); err != nil {
		t.Fatalf("RemoveQueries() error = %v", err)
	}
	if !graph.HasInputDependents(rightInput) {
		t.Fatal("speculative removal mutated committed input edges")
	}
	mustCommit(t, session)
	if graph.HasInputDependents(rightInput) {
		t.Fatal("removed query retained its input dependency edge")
	}
}

func constantQuery(value string) QueryFunc {
	return func(context.Context, Reader) ([]byte, error) {
		return []byte(value), nil
	}
}
