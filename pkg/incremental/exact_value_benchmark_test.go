package incremental

import (
	"context"
	"fmt"
	"runtime"
	"strings"
	"testing"
)

type exactValueBenchmarkFixture struct {
	keys          []QueryKey
	definitions   []Definition
	stringsByKey  map[QueryKey]string
	bytesByKey    map[QueryKey][]byte
	payloadLength int64
}

func BenchmarkExactValueBatchResults(b *testing.B) {
	fixture := newExactValueBenchmarkFixture(b, 256, 4096)
	benchmarkColdLegacyValueBatch(b, fixture)
	benchmarkColdExactValueBatch(b, fixture)
	benchmarkWarmValueBatch(b, "warm-legacy-bytes", fixture, false)
	benchmarkWarmValueBatch(b, "warm-exact-roots", fixture, true)
}

func BenchmarkExactValueExecutionAuthentication(b *testing.B) {
	key := NewQueryKey("query")
	authority := newExactValueAuthority()
	execution := newExactValueExecution(authority, key)
	root := newExactValueRootForExecution(authority, key, "value", execution)
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if err := root.validateExecution(execution); err != nil {
			b.Fatal(err)
		}
	}
}

func newExactValueBenchmarkFixture(
	b *testing.B,
	queryCount, valueSize int,
) *exactValueBenchmarkFixture {
	b.Helper()
	fixture := &exactValueBenchmarkFixture{
		keys:          make([]QueryKey, queryCount),
		definitions:   make([]Definition, queryCount),
		stringsByKey:  make(map[QueryKey]string, queryCount),
		bytesByKey:    make(map[QueryKey][]byte, queryCount),
		payloadLength: int64(queryCount * valueSize),
	}
	for index := range fixture.keys {
		key := NewQueryKey(fmt.Sprintf("query-%03d", index))
		value := fmt.Sprintf("%06d:%s", index, strings.Repeat("v", valueSize-7))
		fixture.keys[index] = key
		fixture.stringsByKey[key] = value
		fixture.bytesByKey[key] = []byte(value)
		fixture.definitions[index] = Definition{Key: key, Run: func(context.Context, Reader) ([]byte, error) {
			return nil, nil
		}}
	}
	return fixture
}

func (f *exactValueBenchmarkFixture) legacyBatch(
	_ context.Context,
	queries []BatchQuery,
) ([]BatchValue, error) {
	values := make([]BatchValue, len(queries))
	for index := range queries {
		values[index].Value = f.bytesByKey[queries[index].Key]
	}
	return values, nil
}

func (f *exactValueBenchmarkFixture) exactBatch(
	_ context.Context,
	queries []BatchQuery,
) ([]ExactBatchValue, error) {
	values := make([]ExactBatchValue, len(queries))
	for index := range queries {
		values[index].Value, values[index].Err = queries[index].NewExactValue(f.stringsByKey[queries[index].Key])
	}
	return values, nil
}

func benchmarkColdLegacyValueBatch(b *testing.B, fixture *exactValueBenchmarkFixture) {
	b.Helper()
	b.Run("cold-legacy-bytes", func(b *testing.B) {
		graph := benchmarkExactValueGraph(b, fixture.definitions)
		b.ReportAllocs()
		b.SetBytes(fixture.payloadLength)
		b.ResetTimer()
		for range b.N {
			session := benchmarkColdSession(b, graph)
			results, err := session.EvaluateAllBatch(b.Context(), fixture.legacyBatch, fixture.keys...)
			if err != nil {
				b.Fatalf("EvaluateAllBatch() error = %v", err)
			}
			benchmarkCommit(b, session)
			runtime.KeepAlive(results)
		}
	})
}

func benchmarkColdExactValueBatch(b *testing.B, fixture *exactValueBenchmarkFixture) {
	b.Helper()
	b.Run("cold-exact-roots", func(b *testing.B) {
		graph := benchmarkExactValueGraph(b, fixture.definitions)
		b.ReportAllocs()
		b.SetBytes(fixture.payloadLength)
		b.ResetTimer()
		for range b.N {
			session := benchmarkColdSession(b, graph)
			results, err := session.EvaluateAllExactBatch(b.Context(), fixture.exactBatch, fixture.keys...)
			if err != nil {
				b.Fatalf("EvaluateAllExactBatch() error = %v", err)
			}
			benchmarkCommit(b, session)
			runtime.KeepAlive(results)
		}
	})
}

func benchmarkWarmValueBatch(
	b *testing.B,
	name string,
	fixture *exactValueBenchmarkFixture,
	exact bool,
) {
	b.Helper()
	b.Run(name, func(b *testing.B) {
		graph := benchmarkExactValueGraph(b, fixture.definitions)
		session, err := graph.Begin()
		if err != nil {
			b.Fatalf("Begin() error = %v", err)
		}
		if _, err := session.EvaluateAllExactBatch(b.Context(), fixture.exactBatch, fixture.keys...); err != nil {
			b.Fatalf("seed EvaluateAllExactBatch() error = %v", err)
		}
		benchmarkCommit(b, session)
		b.ReportAllocs()
		b.ResetTimer()
		for range b.N {
			session, err := graph.Begin()
			if err != nil {
				b.Fatalf("Begin() error = %v", err)
			}
			benchmarkWarmValueEvaluation(b, session, fixture, exact)
			session.Abort()
		}
	})
}

func benchmarkWarmValueEvaluation(
	b *testing.B,
	session *Session,
	fixture *exactValueBenchmarkFixture,
	exact bool,
) {
	b.Helper()
	if exact {
		results, err := session.EvaluateAllExactBatch(b.Context(), fixture.exactBatch, fixture.keys...)
		if err != nil {
			b.Fatalf("EvaluateAllExactBatch() error = %v", err)
		}
		runtime.KeepAlive(results)
		return
	}
	results, err := session.EvaluateAllBatch(b.Context(), fixture.legacyBatch, fixture.keys...)
	if err != nil {
		b.Fatalf("EvaluateAllBatch() error = %v", err)
	}
	runtime.KeepAlive(results)
}

func benchmarkExactValueGraph(b *testing.B, definitions []Definition) *Graph {
	b.Helper()
	graph, err := New(definitions...)
	if err != nil {
		b.Fatalf("New() error = %v", err)
	}
	return graph
}

func benchmarkColdSession(b *testing.B, graph *Graph) *Session {
	b.Helper()
	session, err := graph.BeginColdReset()
	if err != nil {
		b.Fatalf("BeginColdReset() error = %v", err)
	}
	return session
}

func benchmarkCommit(b *testing.B, session *Session) {
	b.Helper()
	if err := session.Commit(b.Context(), acceptRevisions); err != nil {
		b.Fatalf("Commit() error = %v", err)
	}
}
