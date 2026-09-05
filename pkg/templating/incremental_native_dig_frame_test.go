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

package templating

import (
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gitlab.com/haproxy-haptic/scriggo"
	"gitlab.com/haproxy-haptic/scriggo/native"
)

type incrementalDigFrameNamedString string

type incrementalDigFrameNested struct {
	Name    incrementalDigFrameNamedString `json:"name"`
	Missing string                         `json:"missing,omitempty"`
}

type incrementalDigFrameItem struct {
	Nested  incrementalDigFrameNested `json:"nested"`
	Payload struct{ Count int }       `json:"payload"`
	Nil     any                       `json:"nil"`
	Pointer *int                      `json:"pointer"`
}

func buildIncrementalDigFrameTemplate(tb testing.TB, source string, declarations native.Declarations) *scriggo.Template {
	tb.Helper()
	globals := native.Declarations{
		"dig":        native.Synchronous(incrementalDig),
		"dig_string": native.Synchronous(incrementalDigString),
		"item":       (*any)(nil),
	}
	for name, declaration := range declarations {
		globals[name] = declaration
	}
	template, err := scriggo.BuildTemplate(
		scriggo.Files{"index.txt": []byte(source)},
		"index.txt",
		&scriggo.BuildOptions{Globals: globals},
	)
	require.NoError(tb, err)
	return template
}

func runIncrementalDigFrameTemplate(
	tb testing.TB,
	template *scriggo.Template,
	item any,
	direct bool,
) (string, error) {
	tb.Helper()
	options := &scriggo.RunOptions{Context: tb.Context()}
	if direct {
		options.NativeFunctionTrampolines = []*native.FunctionTrampoline{
			makeIncrementalNativeFunctionSignatureFrameTrampoline(incrementalDig),
			makeIncrementalNativeFunctionSignatureFrameTrampoline(incrementalDigString),
		}
	}
	var output strings.Builder
	err := template.Run(&output, map[string]any{"item": item}, options)
	return output.String(), err
}

func TestIncrementalDigFrameMatchesReflectiveDispatch(t *testing.T) {
	tests := []struct {
		name   string
		source string
		item   any
	}{
		{
			name:   "untyped map",
			source: `{{ dig_string(item, "fallback", "metadata", "name") }}:{{ dig(item, "metadata", "port") }}`,
			item: map[string]any{
				"metadata": map[string]any{"name": "route", "port": 8443},
			},
		},
		{
			name:   "typed fields",
			source: `{{ dig_string(item, "fallback", "nested", "name") }}`,
			item: &incrementalDigFrameItem{
				Nested: incrementalDigFrameNested{Name: "route"},
			},
		},
		{
			name:   "named map key",
			source: `{{ dig_string(item, "fallback", "name") }}`,
			item:   map[incrementalDigFrameNamedString]string{"name": "route"},
		},
		{
			name:   "omitempty",
			source: `{{ dig_string(item, "fallback", "nested", "missing") }}`,
			item:   &incrementalDigFrameItem{},
		},
		{
			name:   "nil interface",
			source: `{{ dig_string(item, "fallback", "nil") }}`,
			item:   &incrementalDigFrameItem{},
		},
		{
			name:   "typed nil pointer",
			source: `{{ dig_string(item, "fallback", "pointer") }}`,
			item:   &incrementalDigFrameItem{},
		},
		{
			name:   "zero keys",
			source: `{{ dig_string(item, "fallback") }}`,
			item:   incrementalDigFrameNamedString("route"),
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			template := buildIncrementalDigFrameTemplate(t, test.source, nil)
			reflective, reflectiveErr := runIncrementalDigFrameTemplate(t, template, test.item, false)
			direct, directErr := runIncrementalDigFrameTemplate(t, template, test.item, true)
			require.Equal(t, reflectiveErr, directErr)
			assert.Equal(t, reflective, direct)
		})
	}
}

func TestIncrementalDigFrameMatchesReflectiveFailures(t *testing.T) {
	tests := []struct {
		name string
		item any
	}{
		{name: "non-string map key", item: map[int]string{1: "value"}},
		{name: "non-scalar result", item: map[string]any{"value": struct{ Count int }{Count: 1}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			template := buildIncrementalDigFrameTemplate(
				t,
				`{{ dig_string(item, "fallback", "value") }}`,
				nil,
			)
			_, reflectiveErr := runIncrementalDigFrameTemplate(t, template, test.item, false)
			_, directErr := runIncrementalDigFrameTemplate(t, template, test.item, true)
			require.Error(t, reflectiveErr)
			assert.EqualError(t, directErr, reflectiveErr.Error())
		})
	}
}

func TestIncrementalDigFrameDetachesEscapedStructValue(t *testing.T) {
	var mu sync.Mutex
	var captured any
	capture := func(value any) string {
		mu.Lock()
		captured = value
		mu.Unlock()
		return "captured"
	}
	item := &incrementalDigFrameItem{}
	item.Payload.Count = 7
	template := buildIncrementalDigFrameTemplate(
		t,
		`{{ capture(dig(item, "payload")) }}`,
		native.Declarations{"capture": native.Synchronous(capture)},
	)
	output, err := runIncrementalDigFrameTemplate(t, template, item, true)
	require.NoError(t, err)
	assert.Equal(t, "captured", output)
	item.Payload.Count = 9
	mu.Lock()
	defer mu.Unlock()
	assert.Equal(t, struct{ Count int }{Count: 7}, captured)
}

func TestIncrementalDigFrameConcurrentRuns(t *testing.T) {
	template := buildIncrementalDigFrameTemplate(
		t,
		`{{ dig_string(item, "fallback", "nested", "name") }}`,
		nil,
	)
	const workers = 32
	const iterations = 50
	var wait sync.WaitGroup
	errors := make(chan error, workers)
	for worker := range workers {
		wait.Add(1)
		go func() {
			defer wait.Done()
			want := fmt.Sprintf("route-%d", worker)
			item := &incrementalDigFrameItem{
				Nested: incrementalDigFrameNested{Name: incrementalDigFrameNamedString(want)},
			}
			for range iterations {
				output, err := runIncrementalDigFrameTemplate(t, template, item, true)
				if err != nil {
					errors <- err
					return
				}
				if output != want {
					errors <- fmt.Errorf("output %q, want %q", output, want)
					return
				}
			}
		}()
	}
	wait.Wait()
	close(errors)
	for err := range errors {
		t.Error(err)
	}
}

func BenchmarkIncrementalDigFrame(b *testing.B) {
	template := buildIncrementalDigFrameTemplate(
		b,
		`{% for _ := range iterations %}{{ dig_string(item, "fallback", "nested", "name") }}{% end %}`,
		native.Declarations{"iterations": (*[]int)(nil)},
	)
	item := &incrementalDigFrameItem{
		Nested: incrementalDigFrameNested{Name: "route"},
	}
	iterations := make([]int, 1000)
	genericFrame := makeIncrementalFunctionFrameTrampoline(
		incrementalDigString,
		func(frame native.FunctionCallFrame) {
			frame.SetResultString(0, incrementalDigString(
				frame.ArgEnv(0),
				incrementalFrameInterface(frame.ArgValue(1)),
				frame.ArgString(2),
				incrementalFrameVariadicStrings(frame, 3)...,
			))
		},
	)
	dispatches := []struct {
		name       string
		trampoline *native.FunctionTrampoline
	}{
		{name: "reflective"},
		{name: "generic-frame", trampoline: genericFrame},
		{
			name:       "direct-register",
			trampoline: makeIncrementalNativeFunctionSignatureFrameTrampoline(incrementalDigString),
		},
	}
	for _, dispatch := range dispatches {
		b.Run(dispatch.name, func(b *testing.B) {
			options := &scriggo.RunOptions{Context: b.Context()}
			if dispatch.trampoline != nil {
				options.NativeFunctionTrampolines = []*native.FunctionTrampoline{dispatch.trampoline}
			}
			variables := map[string]any{"item": item, "iterations": iterations}
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				if err := template.Run(io.Discard, variables, options); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
