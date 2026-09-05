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
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type countingCacheablePostProcessor struct {
	calls atomic.Int32
}

func (p *countingCacheablePostProcessor) Process(input string) (string, error) {
	p.calls.Add(1)
	return strings.ToUpper(input), nil
}

func (*countingCacheablePostProcessor) postProcessCacheable() bool {
	return true
}

func (*countingCacheablePostProcessor) postProcessTotal() bool {
	return true
}

func TestPostProcessTransactionPublishesOnlyCommittedExactInputs(t *testing.T) {
	engine := postProcessTransactionTestEngine(t)
	processor := &countingCacheablePostProcessor{}
	engine.postProcessors["main"] = []PostProcessor{processor}

	ctx, transaction := engine.BeginPostProcessTransaction(context.Background())
	require.NotNil(t, transaction)
	outputs, err := engine.PostProcessBatch(ctx, "main", []string{"one", "two", "one"})
	require.NoError(t, err)
	assert.Equal(t, []string{"ONE", "TWO", "ONE"}, outputs)
	assert.EqualValues(t, 2, processor.calls.Load())
	publication, err := transaction.Stage(ctx)
	require.NoError(t, err)
	publication.Abort()

	ctx, transaction = engine.BeginPostProcessTransaction(context.Background())
	outputs, err = engine.PostProcessBatch(ctx, "main", []string{"one", "two", "one"})
	require.NoError(t, err)
	assert.Equal(t, []string{"ONE", "TWO", "ONE"}, outputs)
	assert.EqualValues(t, 4, processor.calls.Load())
	publication, err = transaction.Stage(ctx)
	require.NoError(t, err)
	publication.Publish()

	ctx, transaction = engine.BeginPostProcessTransaction(context.Background())
	outputs, err = engine.PostProcessBatch(ctx, "main", []string{"two", "one", "three"})
	require.NoError(t, err)
	assert.Equal(t, []string{"TWO", "ONE", "THREE"}, outputs)
	assert.EqualValues(t, 5, processor.calls.Load())
	transaction.Abort()
}

func TestPostProcessTransactionIsBoundToItsEngine(t *testing.T) {
	first := postProcessTransactionTestEngine(t)
	second := postProcessTransactionTestEngine(t)
	firstProcessor := &countingCacheablePostProcessor{}
	secondProcessor := &countingCacheablePostProcessor{}
	first.postProcessors["main"] = []PostProcessor{firstProcessor}
	second.postProcessors["main"] = []PostProcessor{secondProcessor}

	ctx, transaction := first.BeginPostProcessTransaction(context.Background())
	output, err := second.PostProcess(ctx, "main", "value")
	require.NoError(t, err)
	assert.Equal(t, "VALUE", output)
	assert.Zero(t, firstProcessor.calls.Load())
	assert.EqualValues(t, 1, secondProcessor.calls.Load())
	transaction.Abort()
}

func postProcessTransactionTestEngine(t *testing.T) *ScriggoEngine {
	t.Helper()
	engine, err := New(
		map[string]string{"main": ""},
		&Options{
			EntryPoints: []string{"main"},
			PostProcessors: map[string][]PostProcessorConfig{"main": {{
				Type: PostProcessorTypeRegexReplace,
				Params: map[string]string{
					"pattern": "x",
					"replace": "y",
				},
			}}},
		},
	)
	require.NoError(t, err)
	return engine
}

func BenchmarkPostProcessTransactionBatchHit(b *testing.B) {
	engine, err := New(
		map[string]string{"main": ""},
		&Options{
			EntryPoints: []string{"main"},
			PostProcessors: map[string][]PostProcessorConfig{"main": {{
				Type: PostProcessorTypeTemplate,
				Params: map[string]string{
					"source": `{{- regexp("\\n([ \\t]*\\n)+").ReplaceAll(input, "\n\n") -}}`,
				},
			}}},
		},
	)
	if err != nil {
		b.Fatal(err)
	}
	inputs := make([]string, 3000)
	for index := range inputs {
		inputs[index] = fmt.Sprintf("backend backend-%06d\n  server server-%06d 10.0.0.1:8080\n\n\n", index, index)
	}
	ctx, transaction := engine.BeginPostProcessTransaction(context.Background())
	if _, err = engine.PostProcessBatch(ctx, "main", inputs); err != nil {
		b.Fatal(err)
	}
	publication, err := transaction.Stage(ctx)
	if err != nil {
		b.Fatal(err)
	}
	publication.Publish()

	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		ctx, transaction = engine.BeginPostProcessTransaction(context.Background())
		outputs, processErr := engine.PostProcessBatch(ctx, "main", inputs)
		if processErr != nil {
			b.Fatal(processErr)
		}
		publication, processErr = transaction.Stage(ctx)
		if processErr != nil {
			b.Fatal(processErr)
		}
		publication.Publish()
		if len(outputs) != len(inputs) {
			b.Fatalf("outputs = %d, want %d", len(outputs), len(inputs))
		}
	}
}
