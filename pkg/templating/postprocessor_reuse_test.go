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
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type postProcessReuseOverrideEngine struct {
	*ScriggoEngine
}

func (*postProcessReuseOverrideEngine) PostProcess(context.Context, string, string) (string, error) {
	return "override", nil
}

func TestPostProcessReuseProofIdentifiesExactConfiguredChain(t *testing.T) {
	withoutProcessors, err := New(
		map[string]string{"main": "value"},
		&Options{EntryPoints: []string{"main"}},
	)
	require.NoError(t, err)
	emptyProof, err := withoutProcessors.PostProcessReuseProof("main")
	require.NoError(t, err)
	require.NotNil(t, emptyProof)
	require.NoError(t, emptyProof.ValidateAuthentication())
	repeated, err := withoutProcessors.PostProcessReuseProof("main")
	require.NoError(t, err)
	assert.Same(t, emptyProof, repeated)

	first := newPostProcessReuseTestEngine(t, "y")
	second := newPostProcessReuseTestEngine(t, "y")
	changed := newPostProcessReuseTestEngine(t, "z")
	firstProof, err := first.PostProcessReuseProof("main")
	require.NoError(t, err)
	secondProof, err := second.PostProcessReuseProof("main")
	require.NoError(t, err)
	changedProof, err := changed.PostProcessReuseProof("main")
	require.NoError(t, err)
	require.NotNil(t, firstProof)
	require.NotNil(t, secondProof)
	require.NotNil(t, changedProof)
	assert.NotSame(t, firstProof, secondProof)
	assert.NotSame(t, firstProof, changedProof)
	assert.NotSame(t, emptyProof, firstProof)
}

func TestPostProcessReuseProofCertifiesOnlyExactEngineIdentity(t *testing.T) {
	identity, err := New(
		map[string]string{"main": "value", "other": "other"},
		&Options{EntryPoints: []string{"main", "other"}},
	)
	require.NoError(t, err)
	proof, err := identity.PostProcessReuseProof("main")
	require.NoError(t, err)
	require.NotNil(t, proof)
	certified, err := proof.CertifiesIdentity(identity, "main")
	require.NoError(t, err)
	assert.True(t, certified)

	nonIdentity := newPostProcessReuseTestEngine(t, "y")
	nonIdentityProof, err := nonIdentity.PostProcessReuseProof("main")
	require.NoError(t, err)
	certified, err = nonIdentityProof.CertifiesIdentity(nonIdentity, "main")
	require.NoError(t, err)
	assert.False(t, certified)

	foreign, err := New(
		map[string]string{"main": "value"},
		&Options{EntryPoints: []string{"main"}},
	)
	require.NoError(t, err)
	_, err = proof.CertifiesIdentity(foreign, "main")
	require.ErrorContains(t, err, "another engine")
	_, err = proof.CertifiesIdentity(&postProcessReuseOverrideEngine{ScriggoEngine: identity}, "main")
	require.ErrorContains(t, err, "another engine")
	_, err = proof.CertifiesIdentity(identity, "other")
	require.ErrorContains(t, err, "another template")
}

func TestPostProcessReuseProofFailsClosedForAmbientNativeCalls(t *testing.T) {
	tests := []struct {
		name      string
		source    string
		functions map[string]GlobalFunc
	}{
		{name: "time", source: `{{ now() }}`},
		{
			name:   "custom native",
			source: `{{ input }}:{{ next() }}`,
			functions: map[string]GlobalFunc{
				"next": func(...any) (any, error) { return 1, nil },
			},
		},
		{name: "uncertified builtin", source: `{{ replace(input, "x", "y") }}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			engine, err := New(
				map[string]string{"main": "value"},
				&Options{
					EntryPoints: []string{"main"},
					Functions:   test.functions,
					PostProcessors: map[string][]PostProcessorConfig{"main": {{
						Type:   PostProcessorTypeTemplate,
						Params: map[string]string{"source": test.source},
					}}},
				},
			)
			require.NoError(t, err)
			proof, err := engine.PostProcessReuseProof("main")
			require.NoError(t, err)
			assert.Nil(t, proof)
		})
	}
}

func TestPostProcessReuseProofRejectsCopiesSubstitutionsAndChainMutation(t *testing.T) {
	t.Run("copy", func(t *testing.T) {
		engine := newPostProcessReuseTestEngine(t, "y")
		proof, err := engine.PostProcessReuseProof("main")
		require.NoError(t, err)
		copied := *proof
		require.ErrorContains(t, copied.ValidateAuthentication(), "invalid")
	})

	t.Run("template substitution", func(t *testing.T) {
		engine, err := New(
			map[string]string{"main": "main", "other": "other"},
			&Options{EntryPoints: []string{"main", "other"}},
		)
		require.NoError(t, err)
		other, err := engine.PostProcessReuseProof("other")
		require.NoError(t, err)
		engine.postProcessReuseProofs["main"] = other
		_, err = engine.PostProcessReuseProof("main")
		require.ErrorContains(t, err, "another template")
	})

	t.Run("proof substitution", func(t *testing.T) {
		engine := newPostProcessReuseTestEngine(t, "y")
		proof, err := engine.PostProcessReuseProof("main")
		require.NoError(t, err)
		copied := *proof
		engine.postProcessReuseProofs["main"] = &copied
		_, err = engine.PostProcessReuseProof("main")
		require.ErrorContains(t, err, "invalid")
	})

	t.Run("configured chain mutation", func(t *testing.T) {
		engine := newPostProcessReuseTestEngine(t, "y")
		uncacheable, err := NewTemplatePostProcessor(`{{ now() }}`, engine.globals)
		require.NoError(t, err)
		engine.postProcessors["main"] = []PostProcessor{uncacheable}
		_, err = engine.PostProcessReuseProof("main")
		require.ErrorContains(t, err, "configured chain")
	})
}

func newPostProcessReuseTestEngine(t *testing.T, replace string) *ScriggoEngine {
	t.Helper()
	engine, err := New(
		map[string]string{"main": "value"},
		&Options{
			EntryPoints: []string{"main"},
			PostProcessors: map[string][]PostProcessorConfig{"main": {{
				Type: PostProcessorTypeRegexReplace,
				Params: map[string]string{
					"pattern": "x",
					"replace": replace,
				},
			}}},
		},
	)
	require.NoError(t, err)
	return engine
}
