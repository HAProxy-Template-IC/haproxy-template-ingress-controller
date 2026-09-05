package renderer

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/rendercontext"
	"gitlab.com/haproxy-haptic/haptic/pkg/incremental"
)

func testExactRoot(
	tb testing.TB,
	key incremental.QueryKey,
	encoded []byte,
) incremental.ExactValueRoot {
	tb.Helper()
	_, roots := testExactRoots(tb, map[incremental.QueryKey]string{key: string(encoded)})
	return roots[key]
}

func testExactRootVariants(
	tb testing.TB,
	key incremental.QueryKey,
	values ...string,
) (*incremental.Graph, []incremental.ExactValueRoot) {
	tb.Helper()
	graph, err := incremental.New(incremental.Definition{
		Key: key,
		Run: func(context.Context, incremental.Reader) ([]byte, error) {
			return nil, nil
		},
	})
	require.NoError(tb, err)
	session, err := graph.Begin()
	require.NoError(tb, err)
	var roots []incremental.ExactValueRoot
	_, err = session.EvaluateAllExactBatch(tb.Context(), func(
		_ context.Context,
		queries []incremental.BatchQuery,
	) ([]incremental.ExactBatchValue, error) {
		for _, value := range values {
			root, rootErr := queries[0].NewExactValue(value)
			if rootErr != nil {
				return nil, rootErr
			}
			roots = append(roots, root)
		}
		return []incremental.ExactBatchValue{{Value: roots[0]}}, nil
	}, key)
	require.NoError(tb, err)
	require.NoError(tb, session.Commit(tb.Context(), func(
		context.Context,
		[]incremental.InputRevision,
	) (bool, error) {
		return true, nil
	}))
	return graph, roots
}

func testExactRoots(
	tb testing.TB,
	values map[incremental.QueryKey]string,
) (graph *incremental.Graph, roots map[incremental.QueryKey]incremental.ExactValueRoot) {
	tb.Helper()
	definitions := make([]incremental.Definition, 0, len(values))
	keys := make([]incremental.QueryKey, 0, len(values))
	for key := range values {
		keys = append(keys, key)
		definitions = append(definitions, incremental.Definition{
			Key: key,
			Run: func(context.Context, incremental.Reader) ([]byte, error) {
				return nil, nil
			},
		})
	}
	var err error
	graph, err = incremental.New(definitions...)
	require.NoError(tb, err)
	session, err := graph.Begin()
	require.NoError(tb, err)
	results, err := session.EvaluateAllExactBatch(tb.Context(), func(
		_ context.Context,
		queries []incremental.BatchQuery,
	) ([]incremental.ExactBatchValue, error) {
		batch := make([]incremental.ExactBatchValue, len(queries))
		for index := range queries {
			batch[index].Value, batch[index].Err = queries[index].NewExactValue(values[queries[index].Key])
		}
		return batch, nil
	}, keys...)
	require.NoError(tb, err)
	require.NoError(tb, session.Commit(tb.Context(), func(
		context.Context,
		[]incremental.InputRevision,
	) (bool, error) {
		return true, nil
	}))
	roots = make(map[incremental.QueryKey]incremental.ExactValueRoot, len(results))
	for index := range results {
		roots[results[index].Key] = results[index].Value
	}
	return graph, roots
}

func testFreshExactResult(
	tb testing.TB,
	key incremental.QueryKey,
	result *incrementalComponentResult,
) (incremental.ExactValueRoot, *authenticatedFreshComponentResult) {
	tb.Helper()
	encoded, fresh, err := newAuthenticatedFreshComponentResult(key, result)
	require.NoError(tb, err)
	root := testExactRoot(tb, key, []byte(encoded))
	require.NoError(tb, bindAuthenticatedFreshComponentResult(fresh, key, root))
	return root, fresh
}

func TestRenderServiceInvalidatedEqualComponentReusesCanonicalExactRoot(t *testing.T) {
	fixture := newIncrementalHTTPTestFixture(t)
	const output = "a=first\nb=stable\n"
	require.Equal(t, output, fixture.render(t))
	require.Equal(t, output, fixture.render(t))

	before := requireGraphExactValue(t, fixture.service.incremental.graph, fixture.queryA)
	beforeIndex := fixture.service.incremental.snapshot.groupIndexes["routes"]
	require.NoError(t, fixture.provider.GetStore("routes").Update(
		incrementalTestResource("default", "a", map[string]any{
			"url":       fixture.urlA,
			"unrelated": "changed",
		}),
		[]string{"default", "a"},
	))

	result, err := fixture.service.Render(t.Context(), fixture.provider, rendercontext.RenderModeReconcile)
	require.NoError(t, err)
	transaction, ok := result.InputTransaction.(*combinedRenderInputTransaction)
	require.True(t, ok)
	require.False(t, transaction.incremental.groupChanged["routes"])
	require.Same(t, beforeIndex, transaction.incremental.groupIndexes["routes"])
	require.NoError(t, result.InputTransaction.Commit(t.Context()))

	after := requireGraphExactValue(t, fixture.service.incremental.graph, fixture.queryA)
	requireExactRootsSame(t, before, after)
	require.Equal(t, uint64(2), fixture.service.incremental.graph.Counters(fixture.queryA).Executions)
	require.Equal(t, uint64(1), fixture.service.incremental.graph.Counters(fixture.queryA).Backdates)
	require.Equal(t, uint64(1), fixture.service.incremental.graph.Counters(fixture.queryB).Executions)
	require.Same(t, beforeIndex, fixture.service.incremental.snapshot.groupIndexes["routes"])
}

func TestRenderServiceExactComponentRootPreservesABAHistory(t *testing.T) {
	fixture := newIncrementalHTTPTestFixture(t)
	require.Equal(t, "a=first\nb=stable\n", fixture.render(t))
	require.Equal(t, "a=first\nb=stable\n", fixture.render(t))
	firstA := requireGraphExactValue(t, fixture.service.incremental.graph, fixture.queryA)

	require.NoError(t, fixture.provider.GetStore("routes").Update(
		incrementalTestResource("default", "a", map[string]any{"url": fixture.urlB}),
		[]string{"default", "a"},
	))
	require.Equal(t, "a=stable\nb=stable\n", fixture.render(t))
	rootB := requireGraphExactValue(t, fixture.service.incremental.graph, fixture.queryA)
	requireExactRootsDistinct(t, firstA, rootB)

	require.NoError(t, fixture.provider.GetStore("routes").Update(
		incrementalTestResource("default", "a", map[string]any{"url": fixture.urlA}),
		[]string{"default", "a"},
	))
	require.Equal(t, "a=first\nb=stable\n", fixture.render(t))
	secondA := requireGraphExactValue(t, fixture.service.incremental.graph, fixture.queryA)
	equal, err := firstA.ExactEqual(secondA)
	require.NoError(t, err)
	require.True(t, equal)
	requireExactRootsDistinct(t, firstA, secondA)
	requireExactRootsDistinct(t, rootB, secondA)
	counters := fixture.service.incremental.graph.Counters(fixture.queryA)
	require.Equal(t, uint64(3), counters.Executions)
	require.Equal(t, uint64(3), counters.Changes)
	require.Zero(t, counters.Backdates)
}

func TestRenderServiceRejectsHistoricalSameValueComponentRoot(t *testing.T) {
	fixture := newIncrementalHTTPTestFixture(t)
	require.Equal(t, "a=first\nb=stable\n", fixture.render(t))
	require.Equal(t, "a=first\nb=stable\n", fixture.render(t))
	historical := requireGraphExactValue(t, fixture.service.incremental.graph, fixture.queryA)

	require.NoError(t, fixture.provider.GetStore("routes").Update(
		incrementalTestResource("default", "a", map[string]any{"url": fixture.urlB}),
		[]string{"default", "a"},
	))
	require.Equal(t, "a=stable\nb=stable\n", fixture.render(t))
	require.NoError(t, fixture.provider.GetStore("routes").Update(
		incrementalTestResource("default", "a", map[string]any{"url": fixture.urlA}),
		[]string{"default", "a"},
	))
	require.Equal(t, "a=first\nb=stable\n", fixture.render(t))
	current := requireGraphExactValue(t, fixture.service.incremental.graph, fixture.queryA)
	requireExactRootsDistinct(t, historical, current)
	equal, err := historical.ExactEqual(current)
	require.NoError(t, err)
	require.True(t, equal)
	require.NoError(t, fixture.service.incremental.graph.ValidateExactValue(fixture.queryA, historical))
	require.ErrorContains(
		t,
		fixture.service.incremental.graph.ValidateCommittedExactValue(fixture.queryA, historical),
		"not the committed query root",
	)

	component := fixture.service.incremental.components["routes"]
	key := resultKey(&component, "routes", "default", "a")
	base := fixture.service.incremental.snapshot
	graphSession, err := fixture.service.incremental.graph.BeginWithResolver(func(
		context.Context,
		incremental.InputKey,
	) (incremental.Input, error) {
		return incremental.Input{}, nil
	})
	require.NoError(t, err)
	t.Cleanup(graphSession.Abort)
	poisoned := &incrementalRenderSession{
		state:        fixture.service.incremental,
		base:         base,
		graphSession: graphSession,
		results:      base.results.Txn(),
		httpEffects:  base.httpEffects.Txn(),
		groupIndexes: cloneGroupIndexes(base.groupIndexes),
	}

	err = poisoned.verifyGroupIndexResult(
		&component, "routes", "default", "a", historical, true, key,
	)
	require.ErrorContains(t, err, "not the transaction-current query root")
}

func TestRenderServiceEqualProjectionDoesNotInvalidateDownstreamComponent(t *testing.T) {
	fixture := newGovernanceEffectsFixture(t)
	first := fixture.renderAndCommitCacheReady(t)
	require.Equal(t, "route=alpha:v1\n", first.HAProxyConfig)
	projectionBefore := requireGraphExactValue(t, fixture.service.incremental.graph, fixture.projectionQuery)
	consumerBefore := requireGraphExactValue(t, fixture.service.incremental.graph, fixture.consumerQuery)

	fixture.eventMessage = "second"
	second := fixture.renderAndCommitCacheReady(t)
	require.Equal(t, first.HAProxyConfig, second.HAProxyConfig)
	projectionAfter := requireGraphExactValue(t, fixture.service.incremental.graph, fixture.projectionQuery)
	consumerAfter := requireGraphExactValue(t, fixture.service.incremental.graph, fixture.consumerQuery)
	requireExactRootsSame(t, projectionBefore, projectionAfter)
	requireExactRootsSame(t, consumerBefore, consumerAfter)
	require.Equal(t, uint64(2), fixture.counters(fixture.projectionQuery).Executions)
	require.Equal(t, uint64(1), fixture.counters(fixture.projectionQuery).Backdates)
	require.Equal(t, uint64(1), fixture.counters(fixture.consumerQuery).Executions)
}

func requireGraphExactValue(
	tb testing.TB,
	graph *incremental.Graph,
	key incremental.QueryKey,
) incremental.ExactValueRoot {
	tb.Helper()
	root, found, err := graph.ExactValue(key)
	require.NoError(tb, err)
	require.True(tb, found)
	require.NoError(tb, graph.ValidateExactValue(key, root))
	return root
}

func requireExactRootsSame(tb testing.TB, left, right incremental.ExactValueRoot) {
	tb.Helper()
	same, err := left.SameRoot(right)
	require.NoError(tb, err)
	require.True(tb, same)
}

func requireExactRootsDistinct(tb testing.TB, left, right incremental.ExactValueRoot) {
	tb.Helper()
	same, err := left.SameRoot(right)
	require.NoError(tb, err)
	require.False(tb, same)
}
