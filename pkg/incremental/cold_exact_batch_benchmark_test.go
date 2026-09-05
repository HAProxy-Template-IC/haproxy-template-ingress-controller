package incremental

import (
	"context"
	"fmt"
	"runtime"
	"testing"
)

const coldExactBatchQueryCount = 42_012

func BenchmarkColdExactBatch42012(b *testing.B) {
	keys := make([]QueryKey, coldExactBatchQueryCount)
	definitions := make([]Definition, coldExactBatchQueryCount)
	for index := range keys {
		key := NewQueryKey(fmt.Sprintf("query/%06d", index))
		keys[index] = key
		definitions[index] = Definition{Key: key, Run: func(context.Context, Reader) ([]byte, error) {
			return nil, nil
		}}
	}
	b.Run("reader-slice", func(b *testing.B) {
		graph := coldExactBenchmarkGraph(b, definitions)
		batch := func(_ context.Context, queries []BatchQuery) ([]ExactBatchValue, error) {
			values := make([]ExactBatchValue, len(queries))
			for index := range queries {
				values[index].Value, values[index].Err = queries[index].NewExactValue("x")
			}
			return values, nil
		}
		benchmarkColdExactBatches(b, graph, func(session *Session) ([]ExactResult, error) {
			return session.EvaluateAllExactBatch(b.Context(), batch, keys...)
		})
	})
	b.Run("columnar", func(b *testing.B) {
		graph := coldExactBenchmarkGraph(b, definitions)
		benchmarkColdExactBatches(b, graph, func(session *Session) ([]ExactResult, error) {
			return session.EvaluateAllColdExactBatch(b.Context(), func(_ context.Context, batch ColdExactBatch) error {
				for index := range batch.Len() {
					if _, err := batch.Query(index).Complete("x"); err != nil {
						return err
					}
				}
				return nil
			}, keys...)
		})
	})
}

func coldExactBenchmarkGraph(b *testing.B, definitions []Definition) *Graph {
	b.Helper()
	graph, err := New(definitions...)
	if err != nil {
		b.Fatalf("New() error = %v", err)
	}
	return graph
}

func benchmarkColdExactBatches(
	b *testing.B,
	graph *Graph,
	evaluate func(*Session) ([]ExactResult, error),
) {
	b.Helper()
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		session, err := graph.BeginColdReset()
		if err != nil {
			b.Fatalf("BeginColdReset() error = %v", err)
		}
		results, err := evaluate(session)
		if err != nil {
			b.Fatalf("cold exact batch error = %v", err)
		}
		if err := session.Commit(b.Context(), acceptRevisions); err != nil {
			b.Fatalf("Commit() error = %v", err)
		}
		runtime.KeepAlive(results)
	}
}
