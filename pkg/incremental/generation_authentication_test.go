package incremental

import (
	"testing"
)

func TestCommittedGenerationAuthenticationFailsClosed(t *testing.T) {
	tests := []struct {
		name   string
		poison func(*Graph, *graphGeneration, InputKey, QueryKey)
	}{
		{
			name: "generation number",
			poison: func(graph *Graph, _ *graphGeneration, _ InputKey, _ QueryKey) {
				graph.current.number++
			},
		},
		{
			name: "copied generation",
			poison: func(graph *Graph, _ *graphGeneration, _ InputKey, _ QueryKey) {
				copied := *graph.current
				graph.current = &copied
			},
		},
		{
			name: "copied generation authentication",
			poison: func(graph *Graph, _ *graphGeneration, _ InputKey, _ QueryKey) {
				copied := *graph.current.authentication
				graph.current.authentication = &copied
			},
		},
		{
			name: "copied current authentication",
			poison: func(graph *Graph, _ *graphGeneration, _ InputKey, _ QueryKey) {
				copied := *graph.currentAuthentication
				graph.currentAuthentication = &copied
			},
		},
		{
			name: "older generation substitution",
			poison: func(graph *Graph, base *graphGeneration, _ InputKey, _ QueryKey) {
				graph.current = base
			},
		},
		{
			name: "input root substitution",
			poison: func(graph *Graph, _ *graphGeneration, inputKey InputKey, _ QueryKey) {
				entry, _ := graph.current.inputs.Root().Get([]byte(inputKey.value))
				inputs, _, _ := graph.current.inputs.Insert([]byte(inputKey.value), entry)
				graph.current.inputs = inputs
			},
		},
		{
			name: "query root substitution",
			poison: func(graph *Graph, _ *graphGeneration, _ InputKey, queryKey QueryKey) {
				entry, _ := graph.current.nodes.Root().Get([]byte(queryKey.value))
				nodes, _, _ := graph.current.nodes.Insert([]byte(queryKey.value), entry)
				graph.current.nodes = nodes
			},
		},
		{
			name: "reverse root substitution",
			poison: func(graph *Graph, _ *graphGeneration, inputKey InputKey, _ QueryKey) {
				key := dependencyTreeKey(inputDep(inputKey))
				entry, _ := graph.current.reverse.Root().Get([]byte(key))
				reverse, _, _ := graph.current.reverse.Insert([]byte(key), entry)
				graph.current.reverse = reverse
			},
		},
		{
			name: "dirty root substitution",
			poison: func(graph *Graph, _ *graphGeneration, _ InputKey, queryKey QueryKey) {
				dirty, _, _ := graph.current.dirty.Insert([]byte(queryKey.value), struct{}{})
				graph.current.dirty = dirty
			},
		},
		{
			name: "counter root substitution",
			poison: func(graph *Graph, _ *graphGeneration, _ InputKey, queryKey QueryKey) {
				entry, _ := graph.current.counters.Root().Get([]byte(queryKey.value))
				counters, _, _ := graph.current.counters.Insert([]byte(queryKey.value), entry)
				graph.current.counters = counters
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			graph, base, inputKey, queryKey := committedGenerationAuthenticationFixture(t)
			graph.mu.Lock()
			test.poison(graph, base, inputKey, queryKey)
			graph.mu.Unlock()

			if graph.Generation() != 0 {
				t.Fatalf("Generation() = %d, want fail-closed zero", graph.Generation())
			}
			if _, exists := graph.Value(queryKey); exists {
				t.Fatal("Value() exposed a poisoned generation")
			}
			if _, err := graph.Begin(); err == nil {
				t.Fatal("Begin() accepted a poisoned generation")
			}
		})
	}
}

func TestCommittedLeavesDoNotExposeMutableAliases(t *testing.T) {
	graph, _, inputKey, queryKey := committedGenerationAuthenticationFixture(t)
	graph.mu.RLock()
	input, inputExists := graph.current.inputs.Root().Get([]byte(inputKey.value))
	node, nodeExists := graph.current.nodes.Root().Get([]byte(queryKey.value))
	graph.mu.RUnlock()
	if !inputExists || !nodeExists {
		t.Fatal("committed fixture is incomplete")
	}
	input.value = "poison"
	dependencies, err := node.deps.Values(graph.dependencyAuthority)
	if err != nil {
		t.Fatalf("dependencies Values() error = %v", err)
	}
	observations, err := node.inputs.Values(graph.observationAuthority)
	if err != nil {
		t.Fatalf("observations Values() error = %v", err)
	}
	dependencies[0].revision = NewRevision("poison")
	observations[0].Revision = NewRevision("poison")

	if got := stringValue(t, graph, queryKey); got != "value" {
		t.Fatalf("committed value = %q after detached-leaf mutation", got)
	}
	if graph.Generation() != 1 || !graph.HasInputDependents(inputKey) {
		t.Fatal("detached-leaf mutation changed committed graph state")
	}
	graph.mu.RLock()
	committedInput, _ := graph.current.inputs.Root().Get([]byte(inputKey.value))
	graph.mu.RUnlock()
	if committedInput.value == input.value {
		t.Fatal("committed input aliases the detached copy")
	}
}

func committedGenerationAuthenticationFixture(
	t *testing.T,
) (*Graph, *graphGeneration, InputKey, QueryKey) {
	t.Helper()
	inputKey := NewInputKey("input")
	queryKey := NewQueryKey("query")
	graph := mustGraph(t, Definition{Key: queryKey, Run: readInputQuery(inputKey)})
	base := graph.current
	session := mustBegin(t, graph)
	mustApply(t, session, exactInput(inputKey, "revision", "value"))
	mustEvaluate(t, session, queryKey)
	mustCommit(t, session)
	return graph, base, inputKey, queryKey
}
