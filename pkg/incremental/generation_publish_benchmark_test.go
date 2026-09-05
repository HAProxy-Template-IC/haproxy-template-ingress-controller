package incremental

import (
	"context"
	"fmt"
	"runtime"
	"testing"
)

const coldGenerationBenchmarkQueryCount = 39_012
const coldGenerationBenchmarkInputCount = 3_000

func BenchmarkColdGenerationPreparation39012(b *testing.B) {
	definitions, queryKeys, inputs := coldGenerationBenchmarkFixture()
	b.Run("serial", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			b.StopTimer()
			session := prepareColdGenerationBenchmarkSession(b, definitions, queryKeys, inputs)
			b.StartTimer()
			if err := session.validateNodeChanges(); err != nil {
				b.Fatal(err)
			}
			observations, err := session.commitObservations()
			if err != nil {
				b.Fatal(err)
			}
			plan, err := session.prepareReplacementGraphCommit()
			if err != nil {
				b.Fatal(err)
			}
			runtime.KeepAlive(observations)
			runtime.KeepAlive(plan)
		}
	})
	b.Run("prebuilt-atomic", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			b.StopTimer()
			session := prepareColdGenerationBenchmarkSession(b, definitions, queryKeys, inputs)
			b.StartTimer()
			plan, observations, err := session.prepareReplacementCommit()
			if err != nil {
				b.Fatal(err)
			}
			runtime.KeepAlive(observations)
			runtime.KeepAlive(plan)
		}
	})
}

func runWarmGenerationPrepareIteration(b *testing.B, graph *Graph, inputs []Input, iteration int) {
	b.Helper()
	b.StopTimer()
	session, err := graph.Begin()
	if err != nil {
		b.Fatal(err)
	}
	inputIndex := iteration % len(inputs)
	input := exactInput(
		inputs[inputIndex].Key,
		fmt.Sprintf("warm-revision/%d", iteration),
		fmt.Sprintf("warm-value/%d", iteration),
	)
	if err := session.ApplyInputs(input); err != nil {
		b.Fatal(err)
	}
	dirty, err := session.DirtyQueries()
	if err != nil {
		b.Fatal(err)
	}
	if _, err := session.EvaluateAll(b.Context(), dirty...); err != nil {
		b.Fatal(err)
	}
	if err := session.validateNodeChanges(); err != nil {
		b.Fatal(err)
	}
	observations, err := session.commitObservations()
	if err != nil {
		b.Fatal(err)
	}
	graph.mu.Lock()
	b.StartTimer()
	plan, err := session.prepareIncrementalGraphCommitLocked()
	b.StopTimer()
	graph.mu.Unlock()
	if err != nil {
		b.Fatal(err)
	}
	if !plan.replacementGeneration.valid(graph) {
		b.Fatal("prepared warm generation has invalid authentication")
	}
	session.Abort()
	runtime.KeepAlive(observations)
	runtime.KeepAlive(plan)
	b.StartTimer()
}

func BenchmarkWarmGenerationRoots39012(b *testing.B) {
	definitions, queryKeys, inputs := coldGenerationBenchmarkFixture()
	graph := prepareWarmGenerationBenchmarkGraph(b, definitions, queryKeys, inputs)

	b.Run("prepare-affected-nodes", func(b *testing.B) {
		b.ReportAllocs()
		for iteration := 0; b.Loop(); iteration++ {
			runWarmGenerationPrepareIteration(b, graph, inputs, iteration)
		}
	})

	b.Run("authenticate-generation", func(b *testing.B) {
		generation := graph.current
		b.ReportAllocs()
		b.ResetTimer()
		for b.Loop() {
			if !generation.valid(graph) {
				b.Fatal("generation authentication failed")
			}
		}
		runtime.KeepAlive(generation)
	})

	b.Run("authenticate-current", func(b *testing.B) {
		b.ReportAllocs()
		b.ResetTimer()
		for b.Loop() {
			graph.mu.RLock()
			valid := graph.currentValidLocked()
			graph.mu.RUnlock()
			if !valid {
				b.Fatal("current generation authentication failed")
			}
		}
	})
}

func prepareWarmGenerationBenchmarkGraph(
	b *testing.B,
	definitions []Definition,
	queryKeys []QueryKey,
	inputs []Input,
) *Graph {
	b.Helper()
	session := prepareColdGenerationBenchmarkSession(b, definitions, queryKeys, inputs)
	graph := session.graph
	if err := session.Commit(b.Context(), acceptRevisions); err != nil {
		b.Fatal(err)
	}
	return graph
}

func coldGenerationBenchmarkFixture() ([]Definition, []QueryKey, []Input) {
	inputs := make([]Input, coldGenerationBenchmarkInputCount)
	for index := range coldGenerationBenchmarkInputCount {
		inputs[index] = exactInput(
			NewInputKey(fmt.Sprintf("input/%06d", index)),
			fmt.Sprintf("revision/%06d", index),
			fmt.Sprintf("value/%06d", index),
		)
	}
	definitions := make([]Definition, coldGenerationBenchmarkQueryCount)
	queryKeys := make([]QueryKey, coldGenerationBenchmarkQueryCount)
	for index := range coldGenerationBenchmarkQueryCount {
		queryKeys[index] = NewQueryKey(fmt.Sprintf("query/%06d", index))
		definitions[index] = Definition{
			Key: queryKeys[index],
			Run: readInputQuery(inputs[index%coldGenerationBenchmarkInputCount].Key),
		}
	}
	return definitions, queryKeys, inputs
}

func prepareColdGenerationBenchmarkSession(
	b *testing.B,
	definitions []Definition,
	queryKeys []QueryKey,
	inputs []Input,
) *Session {
	b.Helper()
	graph, err := New(definitions...)
	if err != nil {
		b.Fatal(err)
	}
	session, err := graph.BeginColdReset(inputs...)
	if err != nil {
		b.Fatal(err)
	}
	_, err = session.EvaluateAllColdExactBatch(b.Context(), func(
		_ context.Context,
		batch ColdExactBatch,
	) error {
		for index := range batch.Len() {
			input := inputs[index%coldGenerationBenchmarkInputCount]
			if _, err := batch.Query(index).ExactInput(input.Key); err != nil {
				return err
			}
			if _, err := batch.Query(index).Complete("value"); err != nil {
				return err
			}
		}
		return nil
	}, queryKeys...)
	if err != nil {
		b.Fatal(err)
	}
	return session
}
