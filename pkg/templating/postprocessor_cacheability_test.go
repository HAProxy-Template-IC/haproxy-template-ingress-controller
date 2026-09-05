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
	"reflect"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gitlab.com/haproxy-haptic/scriggo"
	"gitlab.com/haproxy-haptic/scriggo/builtin"
	"gitlab.com/haproxy-haptic/scriggo/native"
)

// overriddenRegexpBuiltin behaves like builtin.RegExp but is a distinct
// function value, so the cacheability prover must reject it as untrusted.
func overriddenRegexpBuiltin(expression string) builtin.Regexp {
	return builtin.RegExp(expression)
}

func TestTemplatePostProcessorCacheabilityIsCompilerProven(t *testing.T) {
	tests := []struct {
		name      string
		source    string
		configure func(native.Declarations)
		want      bool
	}{
		{name: "input only", source: `{{ input }}`, want: true},
		{
			name:   "canonical regexp",
			source: `{{ regexp("x").ReplaceAll(input, "y") }}`,
			want:   true,
		},
		{
			name:   "uncertified regexp method",
			source: `{{ regexp("x").Match(input) }}`,
		},
		{
			name:   "escaped canonical regexp",
			source: `{% re := regexp %}{{ re("x").ReplaceAll(input, "y") }}`,
			want:   true,
		},
		{
			name:   "escaped canonical regexp method",
			source: `{% replace := regexp("x").ReplaceAll %}{{ replace(input, "y") }}`,
			want:   true,
		},
		{name: "unapproved pure helper", source: `{{ replace(input, "x", "y") }}`},
		{name: "ambient time", source: `{{ now() }}`},
		{
			name:   "ambient time in unused macro",
			source: `{% macro ambient %}{{ now() }}{% end %}{{ input }}`,
		},
		{
			name:   "ambient time in dead branch",
			source: `{% if false %}{{ now() }}{% end %}{{ input }}`,
		},
		{name: "parallel render", source: `{% macro M %}{{ input }}{% end %}{{ go M() }}`},
		{name: "language print", source: `{% print(input) %}`},
		{
			name:   "shadowed universe builtin",
			source: `{{ len(input) }}`,
			configure: func(globals native.Declarations) {
				globals["len"] = func(string) int { return 1 }
			},
		},
		{
			name:   "overridden regexp",
			source: `{{ regexp("x").ReplaceAll(input, "y") }}`,
			configure: func(globals native.Declarations) {
				globals["regexp"] = overriddenRegexpBuiltin
			},
		},
		{
			name:   "self-certified overridden regexp",
			source: `{{ regexp("x").ReplaceAll(input, "y") }}`,
			configure: func(globals native.Declarations) {
				globals["regexp"] = native.Synchronous(overriddenRegexpBuiltin)
			},
		},
		{
			name:   "unused custom declaration",
			source: `{{ input }}`,
			configure: func(globals native.Declarations) {
				globals["custom"] = func() string { return "unused" }
			},
			want: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			globals := buildScriggoGlobals(nil, nil, nil)
			if test.configure != nil {
				test.configure(globals)
			}
			processor, err := NewTemplatePostProcessor(test.source, globals)
			require.NoError(t, err)
			assert.Equal(t, test.want, processor.postProcessCacheable())
		})
	}
}

func TestTemplatePostProcessorCertifiesOnlyReplaceAll(t *testing.T) {
	processor, err := NewTemplatePostProcessor(
		`{{ regexp("x").ReplaceAll(input, "y") }}`,
		buildScriggoGlobals(nil, nil, nil),
	)
	require.NoError(t, err)
	require.Len(t, processor.compiled.UsedNativeCallables(), 1)
	callable := processor.compiled.UsedNativeCallables()[0]
	assert.Equal(t, scriggo.NativeCallableMethod, callable.Kind)
	assert.Equal(t, reflect.TypeOf(builtin.Regexp{}), callable.Receiver)
	assert.Equal(t, "regexp", callable.DeclarationName)
	assert.Equal(t, "ReplaceAll", callable.MemberPath)
	assert.True(t, callable.Synchronous)
}

func TestPostProcessorChainCacheabilityFailsClosed(t *testing.T) {
	regex, err := NewRegexReplaceProcessor("x", "y")
	require.NoError(t, err)
	globals := buildScriggoGlobals(nil, nil, nil)
	cacheableTemplate, err := NewTemplatePostProcessor(`{{ regexp("x").ReplaceAll(input, "y") }}`, globals)
	require.NoError(t, err)
	uncacheableTemplate, err := NewTemplatePostProcessor(`{{ now() }}`, globals)
	require.NoError(t, err)

	assert.False(t, postProcessorChainCacheable(nil))
	assert.True(t, postProcessorChainCacheable([]PostProcessor{regex, cacheableTemplate}))
	assert.False(t, postProcessorChainCacheable([]PostProcessor{regex, uncacheableTemplate}))
	assert.False(t, postProcessorChainCacheable([]PostProcessor{postProcessorWithoutCertificate{}}))
	assert.True(t, postProcessorChainBatchable([]PostProcessor{regex, cacheableTemplate}))
	assert.False(t, postProcessorChainBatchable([]PostProcessor{cacheableTemplate, cacheableTemplate}))
	assert.False(t, postProcessorChainBatchable([]PostProcessor{regex, uncacheableTemplate}))
}

type postProcessorWithoutCertificate struct{}

func (postProcessorWithoutCertificate) Process(input string) (string, error) {
	return input, nil
}
