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
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPostProcessBatchMatchesSequentialPureChain(t *testing.T) {
	engine, err := New(map[string]string{"main": ""}, &Options{
		EntryPoints: []string{"main"},
		PostProcessors: map[string][]PostProcessorConfig{"main": {
			{Type: PostProcessorTypeRegexReplace, Params: map[string]string{
				"pattern": "^[ ]+", "replace": "  ",
			}},
			{Type: PostProcessorTypeTemplate, Params: map[string]string{
				"source": `{{ regexp("\\n([ \\t]*\\n)+").ReplaceAll(input, "\n\n") }}`,
			}},
		}},
	})
	require.NoError(t, err)
	inputs := []string{"    one\n\n \n\ntwo\n", "\tthree\n", ""}
	want := make([]string, len(inputs))
	for index, input := range inputs {
		want[index], err = engine.PostProcess(t.Context(), "main", input)
		require.NoError(t, err)
	}

	got, err := engine.PostProcessBatch(t.Context(), "main", inputs)

	require.NoError(t, err)
	assert.Equal(t, want, got)
	assert.Equal(t, []string{"    one\n\n \n\ntwo\n", "\tthree\n", ""}, inputs)
}

func TestPostProcessBatchPreservesAmbientProcessorOrder(t *testing.T) {
	nextValue := 0
	next := func() int {
		nextValue++
		return nextValue
	}
	engine, err := New(map[string]string{"main": ""}, &Options{
		EntryPoints:  []string{"main"},
		Declarations: map[string]any{"next": next},
		PostProcessors: map[string][]PostProcessorConfig{"main": {
			{Type: PostProcessorTypeTemplate, Params: map[string]string{"source": `{{ input }}:{{ next() }}`}},
			{Type: PostProcessorTypeTemplate, Params: map[string]string{"source": `{{ input }}:{{ next() }}`}},
		}},
	})
	require.NoError(t, err)

	got, err := engine.PostProcessBatch(t.Context(), "main", []string{"first", "second"})

	require.NoError(t, err)
	assert.Equal(t, []string{"first:1:2", "second:3:4"}, got)
}

func TestPostProcessBatchReportsInputIndex(t *testing.T) {
	engine, err := New(map[string]string{"main": ""}, &Options{
		EntryPoints: []string{"main"},
		PostProcessors: map[string][]PostProcessorConfig{"main": {{
			Type: PostProcessorTypeTemplate,
			Params: map[string]string{
				"source": `{% if input == "bad" %}{{ fail("boom") }}{% end %}{{ input }}`,
			},
		}}},
	})
	require.NoError(t, err)

	_, err = engine.PostProcessBatch(t.Context(), "main", []string{"first", "bad", "last"})

	require.Error(t, err)
	var batchErr *PostProcessBatchError
	require.True(t, errors.As(err, &batchErr))
	assert.Equal(t, 1, batchErr.Index)
	assert.ErrorContains(t, err, "boom")
}

func TestPostProcessBatchPreservesFirstFailureAcrossProcessors(t *testing.T) {
	engine, err := New(map[string]string{"main": ""}, &Options{
		EntryPoints: []string{"main"},
		PostProcessors: map[string][]PostProcessorConfig{"main": {
			{Type: PostProcessorTypeTemplate, Params: map[string]string{
				"source": `{% if input == "second" %}{% panic("first processor second input") %}{% end %}{{ input }}`,
			}},
			{Type: PostProcessorTypeTemplate, Params: map[string]string{
				"source": `{% if input == "first" %}{% panic("second processor first input") %}{% end %}{{ input }}`,
			}},
		}},
	})
	require.NoError(t, err)

	_, err = engine.PostProcessBatch(t.Context(), "main", []string{"first", "second"})

	require.Error(t, err)
	var batchErr *PostProcessBatchError
	require.True(t, errors.As(err, &batchErr))
	assert.Equal(t, 0, batchErr.Index)
	assert.ErrorContains(t, err, "second processor first input")
	assert.NotContains(t, err.Error(), "first processor second input")
}

func BenchmarkPostProcessBatch(b *testing.B) {
	engine, err := New(map[string]string{"main": ""}, &Options{
		EntryPoints: []string{"main"},
		PostProcessors: map[string][]PostProcessorConfig{"main": {{
			Type: PostProcessorTypeTemplate,
			Params: map[string]string{
				"source": `{{ regexp("\\n([ \\t]*\\n)+").ReplaceAll(input, "\n\n") }}`,
			},
		}}},
	})
	if err != nil {
		b.Fatal(err)
	}
	inputs := make([]string, 3000)
	for index := range inputs {
		inputs[index] = fmt.Sprintf("backend be-%d\n  server s-%d 127.0.0.1:80\n\n\n", index, index)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		outputs, batchErr := engine.PostProcessBatch(context.Background(), "main", inputs)
		if batchErr != nil {
			b.Fatal(batchErr)
		}
		if len(outputs) != len(inputs) {
			b.Fatalf("got %d outputs, want %d", len(outputs), len(inputs))
		}
	}
}
