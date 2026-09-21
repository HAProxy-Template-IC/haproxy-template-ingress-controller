package incremental

import (
	"testing"

	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/incremental/internal/orderedset"
)

func TestReverseSetEditsRejectDuplicateTransitionsAndCancelExactly(t *testing.T) {
	input := NewInputKey("input")
	query := NewQueryKey("query")
	graph := mustGraph(t, Definition{Key: query, Run: readInputQuery(input)})
	session := mustBegin(t, graph)
	mustApply(t, session, exactInput(input, "revision/1", "value"))
	mustEvaluate(t, session, query)
	mustCommit(t, session)
	dependency := inputDep(input)
	original, err := graph.reverseRootLocked(dependency)
	require.NoError(t, err)
	editor := reverseSetEditor{graph: graph, roots: map[dependencyKey]orderedset.Root{}}

	require.ErrorContains(t, editor.add(dependency, query), "already contains")
	require.NoError(t, editor.delete(dependency, query))
	require.ErrorContains(t, editor.delete(dependency, query), "does not contain")
	require.NoError(t, editor.add(dependency, query))
	require.ErrorContains(t, editor.add(dependency, query), "already contains")

	added := NewQueryKey("new")
	require.ErrorContains(t, editor.delete(dependency, added), "does not contain")
	require.NoError(t, editor.add(dependency, added))
	require.ErrorContains(t, editor.add(dependency, added), "already contains")
	require.NoError(t, editor.delete(dependency, added))
	require.ErrorContains(t, editor.delete(dependency, added), "does not contain")

	changes, err := editor.changes()
	require.NoError(t, err)
	same, err := original.SameRoot(graph.reverseAuthority, reverseScope(dependency), changes[dependency].root)
	require.NoError(t, err)
	require.True(t, same)
	committed, err := graph.reverseRootLocked(dependency)
	require.NoError(t, err)
	same, err = original.SameRoot(graph.reverseAuthority, reverseScope(dependency), committed)
	require.NoError(t, err)
	require.True(t, same)

	editor.roots[dependency] = orderedset.NewAuthority().Empty()
	require.Error(t, editor.add(dependency, added))
	_, err = editor.changes()
	require.Error(t, err)
}
