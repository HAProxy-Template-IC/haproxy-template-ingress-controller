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

package templating

import (
	"context"
	"fmt"
	"maps"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestIncrementalComponentVectorCarrierLargeDispatch(t *testing.T) {
	for _, count := range []int{1, 2, 127, 128, 129, 252, 255, 256, 257, 513} {
		t.Run(fmt.Sprintf("lanes=%d", count), func(t *testing.T) {
			templates := make(map[string]string, count)
			for index := range count {
				name := fmt.Sprintf("lane-%04d", index)
				templates[name] = name + ":{{ source }}"
			}
			engine := newIncrementalVectorCarrierTestEngine(t, templates)
			eligibility, available := engine.IncrementalComponentVectorCarrierEligibility()
			require.Truef(t, available, "carrier rejection: %v", engine.IncrementalComponentVectorCarrierDiagnostic())
			require.Len(t, eligibility.TemplateNames, count)
			lanes := make([]IncrementalComponentVectorCarrierLane, 0, count)
			wave := IncrementalComponentVectorCarrierWave{}
			var want []string
			for index := count - 1; index >= 0; index-- {
				name := fmt.Sprintf("lane-%04d", index)
				lane := newIncrementalVectorCarrierTestLane(t, engine, name, index%2+1, nil)
				lanes = append(lanes, lane)
				wave.Lanes = append(wave.Lanes, IncrementalComponentVectorCarrierWaveLane{
					TemplateName: name, Count: lane.Count,
				})
				for _, ctx := range lane.Contexts {
					values := maps.Clone(ctx.Value(RenderContextContextKey).(map[string]any))
					delete(values, "renderMode")
					output, err := engine.RenderIncrementalComponent(ctx, name, values)
					require.NoError(t, err)
					want = append(want, output)
				}
			}
			lifecycle := &incrementalVectorCarrierWavesTestLifecycle{
				incrementalVectorTestLifecycle: newIncrementalVectorTestLifecycle(len(want)),
				waves:                          [][]IncrementalComponentVectorCarrierLane{lanes},
			}
			require.NoError(t, engine.RenderIncrementalComponentVectorCarrierWaves(t.Context(),
				IncrementalComponentVectorCarrierWavesInput{
					Waves: []IncrementalComponentVectorCarrierWave{wave}, Lifecycle: lifecycle,
				}))
			require.Equal(t, want, lifecycle.outputs)
			require.Len(t, lifecycle.begins, len(want))
			require.Len(t, lifecycle.ends, len(want))
		})
	}
}

func TestIncrementalComponentSourceTransactionsLargeDispatch(t *testing.T) {
	for _, count := range []int{129, 252, 257, 513} {
		t.Run(fmt.Sprintf("lanes=%d", count), func(t *testing.T) {
			templates := make(map[string]string, count)
			for index := range count {
				name := fmt.Sprintf("lane-%04d", index)
				templates[name] = name + ":{{ source }}"
			}
			engine := newIncrementalVectorCarrierTestEngine(t, templates)
			require.Truef(t, engine.IncrementalComponentSourceTransactionsEligibility(),
				"carrier rejection: %v", engine.IncrementalComponentVectorCarrierDiagnostic())
			lifecycle := &incrementalSourceTransactionTestLifecycle{
				incrementalVectorTestLifecycle: newIncrementalVectorTestLifecycle(count),
			}
			transaction := IncrementalComponentSourceTransaction{}
			want := make([]string, 0, count)
			for index := count - 1; index >= 0; index-- {
				name := fmt.Sprintf("lane-%04d", index)
				lane := newIncrementalVectorCarrierTestLane(t, engine, name, 1, nil)
				if lifecycle.batch.Bindings == nil {
					lifecycle.batch.Bindings = lane.Bindings
					lifecycle.batch.Contexts = []context.Context{lane.Contexts[0]}
				}
				transaction.Children = append(transaction.Children, IncrementalComponentSourceTransactionChild{
					TemplateName: name, Index: len(transaction.Children),
				})
				lifecycle.batch.ChildContexts = append(lifecycle.batch.ChildContexts, lane.Contexts[0])
				values := maps.Clone(lane.Contexts[0].Value(RenderContextContextKey).(map[string]any))
				delete(values, "renderMode")
				output, err := engine.RenderIncrementalComponent(lane.Contexts[0], name, values)
				require.NoError(t, err)
				want = append(want, output)
			}
			require.NoError(t, engine.RenderIncrementalComponentSourceTransactions(t.Context(),
				IncrementalComponentSourceTransactionsInput{
					Waves: []IncrementalComponentSourceTransactionWave{{
						Transactions: []IncrementalComponentSourceTransaction{transaction},
					}},
					Lifecycle: lifecycle,
				}))
			require.Equal(t, want, lifecycle.outputs)
			require.Len(t, lifecycle.begins, count)
			require.Len(t, lifecycle.ends, count)
			require.Equal(t, []int{0}, lifecycle.loads)
			require.Equal(t, []int{0}, lifecycle.seals)
			require.Zero(t, lifecycle.abortN)
		})
	}
}
