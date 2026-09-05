// Copyright 2026 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package renderer

import (
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestIncrementalVectorDirectCallDrainsBeforeEnd(t *testing.T) {
	execution := testIncrementalVectorExecution(t, 1)
	require.NoError(t, execution.Begin(0))
	item, err := execution.enterDirect(0, "direct capability")
	require.NoError(t, err)
	require.Same(t, &execution.items[0], item)

	ended := make(chan error, 1)
	go func() { ended <- execution.End(0, "") }()
	writerWaiting := false
	for range 100_000 {
		if !execution.callGate.TryRLock() {
			writerWaiting = true
			break
		}
		execution.callGate.RUnlock()
		runtime.Gosched()
	}
	require.True(t, writerWaiting, "End never reached the revocation gate")
	select {
	case <-ended:
		t.Fatal("End returned before the direct call drained")
	default:
	}
	execution.leaveDirect()
	require.NoError(t, <-ended)
	require.NoError(t, execution.finish())
}

func TestIncrementalVectorDirectCallRejectsAnotherItem(t *testing.T) {
	execution := testIncrementalVectorExecution(t, 2)
	require.NoError(t, execution.Begin(0))
	_, err := execution.enterDirect(1, "direct capability")
	require.ErrorContains(t, err, "inactive incremental component vector item 1")
	require.Error(t, execution.End(0, ""))
	require.Error(t, execution.finish())
}

func BenchmarkIncrementalVectorCallGuard(b *testing.B) {
	for _, benchmark := range []struct {
		name string
		call func(*incrementalVectorExecution) error
	}{
		{
			name: "closure",
			call: func(execution *incrementalVectorExecution) error {
				release, err := execution.current(0)
				if err == nil {
					release()
				}
				return err
			},
		},
		{
			name: "direct",
			call: func(execution *incrementalVectorExecution) error {
				_, err := execution.enterDirect(0, "benchmark")
				if err == nil {
					execution.leaveDirect()
				}
				return err
			},
		},
	} {
		b.Run(benchmark.name, func(b *testing.B) {
			execution := testIncrementalVectorExecutionContextForBenchmark(b)
			require.NoError(b, execution.Begin(0))
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				if err := benchmark.call(execution); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func testIncrementalVectorExecutionContextForBenchmark(b *testing.B) *incrementalVectorExecution {
	b.Helper()
	execution := testIncrementalVectorExecutionFixture(b.Context(), 1)
	b.Cleanup(func() { execution.Abort(0, nil) })
	return execution
}
