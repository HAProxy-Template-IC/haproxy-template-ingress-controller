// Copyright 2025 Philipp Hossner
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

package rendercontext

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/renderplan"
)

// assertPartitions is the invariant the whole diff pipeline rests on:
// concatenating the sections reproduces the config byte for byte.
func assertPartitions(t *testing.T, config string, sections []renderplan.Section) {
	t.Helper()
	offset := 0
	for _, section := range sections {
		require.LessOrEqual(t, offset+section.Length, len(config))
		text := config[offset : offset+section.Length]
		assert.Equal(t, section.TextDigest, renderplan.DigestString(text), "digest of section %q", section.Name)
		offset += section.Length
	}
	assert.Equal(t, len(config), offset, "sections must cover the whole config")
}

func TestAssembleWithoutTokens(t *testing.T) {
	tests := []struct {
		name     string
		rendered string
		want     []renderplan.Section
	}{
		{name: "empty render", rendered: "", want: nil},
		{
			name:     "config untouched",
			rendered: "global\n    daemon\n\ndefaults\n    mode http\n",
			want: []renderplan.Section{{
				Kind:       renderplan.SectionKindCore,
				Name:       "core#0",
				TextDigest: renderplan.DigestString("global\n    daemon\n\ndefaults\n    mode http\n"),
				Length:     len("global\n    daemon\n\ndefaults\n    mode http\n"),
				Text:       "global\n    daemon\n\ndefaults\n    mode http\n",
				TextKnown:  true,
			}},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			registry := NewPlanRegistry(nil)

			config, sections, err := registry.Assemble(context.Background(), tc.rendered, failingPost(t))

			require.NoError(t, err)
			assert.Equal(t, tc.rendered, config, "a render without tokens must pass through unchanged")
			assert.Equal(t, tc.want, sections)
			assertPartitions(t, config, sections)
		})
	}
}

func TestAssembleSplicesSections(t *testing.T) {
	registry := NewPlanRegistry(nil)
	beA, err := registry.Section("backend", "be_a", "backend be_a\n    server s1 10.0.0.1:80\n")
	require.NoError(t, err)
	beB, err := registry.Section("backend", "be_b", "backend be_b\n")
	require.NoError(t, err)

	rendered := "global\n    daemon\n\n" + beA + "\n# between\n" + beB + "frontend fe\n"
	config, sections, err := registry.Assemble(context.Background(), rendered, nil)

	require.NoError(t, err)
	assert.Equal(t,
		"global\n    daemon\n\n"+
			"backend be_a\n    server s1 10.0.0.1:80\n"+
			"\n# between\n"+
			"backend be_b\n"+
			"frontend fe\n", config)
	assert.Equal(t,
		[]string{"core#0", "be_a", "core#1", "be_b", "core#2"},
		sectionNames(sections))
	assert.Equal(t,
		[]string{"core", "backend", "core", "backend", "core"},
		sectionKinds(sections))
	assertPartitions(t, config, sections)
}

func TestAssembleProfileGroup(t *testing.T) {
	registry := NewPlanRegistry(nil)
	_, err := registry.Section("profile", "zeta", "defaults zeta\n")
	require.NoError(t, err)
	_, err = registry.Section("profile", "alpha", "defaults alpha\n")
	require.NoError(t, err)
	backend, err := registry.Section("backend", "be_a", "backend be_a\n")
	require.NoError(t, err)

	rendered := "global\n" + registry.ProfileGroup() + backend
	config, sections, err := registry.Assemble(context.Background(), rendered, nil)

	require.NoError(t, err)
	assert.Equal(t, "global\ndefaults alpha\ndefaults zeta\nbackend be_a\n", config,
		"profiles are spliced sorted by name")
	assert.Equal(t, []string{"core#0", "alpha", "zeta", "be_a"}, sectionNames(sections))
	assertPartitions(t, config, sections)
}

func TestAssemblePostProcessesEachSection(t *testing.T) {
	registry := NewPlanRegistry(nil)
	token, err := registry.Section("backend", "be_a", "backend be_a\n\tserver s1 10.0.0.1:80")
	require.NoError(t, err)

	post := func(_ context.Context, text string) (string, error) {
		return strings.ReplaceAll(text, "\t", "    "), nil
	}
	config, sections, err := registry.Assemble(context.Background(), "global\n"+token, post)

	require.NoError(t, err)
	assert.Equal(t, "global\nbackend be_a\n    server s1 10.0.0.1:80\n", config)
	assert.Equal(t, renderplan.DigestString("backend be_a\n    server s1 10.0.0.1:80\n"), sections[1].TextDigest,
		"the section digest covers the post-processed bytes")
	assertPartitions(t, config, sections)
}

func TestAssembleBatchPostProcessesInEmissionOrder(t *testing.T) {
	registry := NewPlanRegistry(nil)
	_, err := registry.Section("profile", "zeta", "defaults zeta\n")
	require.NoError(t, err)
	_, err = registry.Section("profile", "alpha", "defaults alpha\n")
	require.NoError(t, err)
	first, err := registry.Section("backend", "first", "backend first\n\tserver first 127.0.0.1:80")
	require.NoError(t, err)
	last, err := registry.Section("backend", "last", "backend last\n\tserver last 127.0.0.1:80")
	require.NoError(t, err)

	var inputs []string
	batch := func(_ context.Context, texts []string) ([]string, error) {
		inputs = append(inputs, texts...)
		outputs := make([]string, len(texts))
		for index, text := range texts {
			outputs[index] = strings.ReplaceAll(text, "\t", "  ")
		}
		return outputs, nil
	}
	config, sections, err := registry.AssembleWithBatch(
		context.Background(), first+registry.ProfileGroup()+last, failingPost(t), batch,
	)

	require.NoError(t, err)
	assert.Equal(t, []string{
		"backend first\n\tserver first 127.0.0.1:80",
		"defaults alpha\n",
		"defaults zeta\n",
		"backend last\n\tserver last 127.0.0.1:80",
	}, inputs)
	assert.Equal(t,
		"backend first\n  server first 127.0.0.1:80\n"+
			"defaults alpha\ndefaults zeta\n"+
			"backend last\n  server last 127.0.0.1:80\n",
		config,
	)
	assertPartitions(t, config, sections)
}

func TestAssembleBatchSkipsEmptySectionsLikeSequential(t *testing.T) {
	registry := NewPlanRegistry(nil)
	emptyBackend, err := registry.Section("backend", "empty", "")
	require.NoError(t, err)
	_, err = registry.Section("profile", "empty", "")
	require.NoError(t, err)
	fullBackend, err := registry.Section("backend", "full", "backend full\n")
	require.NoError(t, err)

	var inputs []string
	batch := func(_ context.Context, texts []string) ([]string, error) {
		inputs = append(inputs, texts...)
		outputs := make([]string, len(texts))
		for index, text := range texts {
			outputs[index] = "prefix:" + text
		}
		return outputs, nil
	}
	config, sections, err := registry.AssembleWithBatch(
		context.Background(), emptyBackend+registry.ProfileGroup()+fullBackend, failingPost(t), batch,
	)

	require.NoError(t, err)
	assert.Equal(t, []string{"backend full\n"}, inputs)
	assert.Equal(t, "\n\nprefix:backend full\n", config)
	assertPartitions(t, config, sections)
}

func TestAssembleBatchDoesNotCallProcessorForOnlyEmptySections(t *testing.T) {
	registry := NewPlanRegistry(nil)
	empty, err := registry.Section("backend", "empty", "")
	require.NoError(t, err)

	config, sections, err := registry.AssembleWithBatch(
		context.Background(), empty, failingPost(t), func(context.Context, []string) ([]string, error) {
			t.Fatal("batch post-processing must not run for empty sections")
			return nil, nil
		},
	)

	require.NoError(t, err)
	assert.Equal(t, "\n", config)
	assertPartitions(t, config, sections)
}

func TestAssembleBatchRejectsInvalidResults(t *testing.T) {
	registry := NewPlanRegistry(nil)
	first, err := registry.Section("backend", "first", "backend first\n")
	require.NoError(t, err)
	last, err := registry.Section("backend", "last", "backend last\n")
	require.NoError(t, err)
	rendered := first + last

	_, _, err = registry.AssembleWithBatch(context.Background(), rendered, nil,
		func(context.Context, []string) ([]string, error) {
			return []string{"only one"}, nil
		},
	)
	require.ErrorContains(t, err, "returned 1 of 2 sections")

	_, _, err = registry.AssembleWithBatch(context.Background(), rendered, nil,
		func(context.Context, []string) ([]string, error) {
			return nil, indexedBatchTestError{index: 1}
		},
	)
	require.ErrorContains(t, err, `post-processing backend "last"`)
	require.ErrorIs(t, err, errBatchTest)
}

func TestAssembleBatchValidatesTokensBeforeProcessing(t *testing.T) {
	registry := NewPlanRegistry(nil)
	token, err := registry.Section("backend", "first", "backend first\n")
	require.NoError(t, err)

	called := false
	_, _, err = registry.AssembleWithBatch(context.Background(), token+token, nil,
		func(context.Context, []string) ([]string, error) {
			called = true
			return nil, nil
		},
	)
	require.ErrorContains(t, err, `backend "first" spliced more than once`)
	assert.False(t, called)
}

var errBatchTest = errors.New("batch failed")

type indexedBatchTestError struct {
	index int
}

func (e indexedBatchTestError) Error() string {
	return errBatchTest.Error()
}

func (e indexedBatchTestError) Unwrap() error {
	return errBatchTest
}

func (e indexedBatchTestError) BatchIndex() int {
	return e.index
}

func TestAssembleIndentedToken(t *testing.T) {
	registry := NewPlanRegistry(nil)
	token, err := registry.Section("backend", "be_a", "backend be_a\n")
	require.NoError(t, err)

	config, sections, err := registry.Assemble(context.Background(), "global\n  "+token, nil)

	require.NoError(t, err)
	assert.Equal(t, "global\nbackend be_a\n", config, "post-processing may re-indent a token line")
	assertPartitions(t, config, sections)
}

func TestAssembleErrors(t *testing.T) {
	tests := []struct {
		name     string
		setup    func(*PlanRegistry) string
		wantErr  string
		wantPost bool
	}{
		{
			name: "registered section never emitted",
			setup: func(r *PlanRegistry) string {
				_, err := r.Section("backend", "be_a", "backend be_a\n")
				require.NoError(t, err)
				return "global\n"
			},
			wantErr: "registered sections but the render emitted no token",
		},
		{
			name: "one of two sections never emitted",
			setup: func(r *PlanRegistry) string {
				token, err := r.Section("backend", "be_a", "backend be_a\n")
				require.NoError(t, err)
				_, err = r.Section("backend", "be_b", "backend be_b\n")
				require.NoError(t, err)
				return token
			},
			wantErr: "1 of 2 registered sections have no token in the config: backend be_b",
		},
		{
			name: "token for an unregistered section",
			setup: func(r *PlanRegistry) string {
				return r.sectionToken("backend", "be_ghost")
			},
			wantErr: `token for unregistered backend "be_ghost"`,
		},
		{
			name: "same section spliced twice",
			setup: func(r *PlanRegistry) string {
				token, err := r.Section("backend", "be_a", "backend be_a\n")
				require.NoError(t, err)
				return token + token
			},
			wantErr: `backend "be_a" spliced more than once`,
		},
		{
			name: "profile spliced by group and by its own token",
			setup: func(r *PlanRegistry) string {
				token, err := r.Section("profile", "shared", "defaults shared\n")
				require.NoError(t, err)
				return r.ProfileGroup() + token
			},
			wantErr: `profile "shared" spliced more than once`,
		},
		{
			name:    "malformed token",
			setup:   func(r *PlanRegistry) string { return "# @haptic:" + r.nonce + ":section:backend\n" },
			wantErr: "malformed token",
		},
		{
			name:    "unknown token kind",
			setup:   func(r *PlanRegistry) string { return "# @haptic:" + r.nonce + ":section:frontend:fe@\n" },
			wantErr: `unknown kind "frontend"`,
		},
		{
			name:    "unknown token verb",
			setup:   func(r *PlanRegistry) string { return "# @haptic:" + r.nonce + ":verb:x@\n" },
			wantErr: "unknown token",
		},
		{
			name:    "token embedded in a line",
			setup:   func(r *PlanRegistry) string { return "    acl x hdr(host) -i @haptic:" + r.nonce + "\n" },
			wantErr: "malformed token",
		},
		{
			name: "token inside a section body",
			setup: func(r *PlanRegistry) string {
				token, err := r.Section("backend", "be_a", "backend be_a\n# @haptic:"+r.nonce+":group:profiles@\n")
				require.NoError(t, err)
				return token
			},
			wantErr: "a token survived assembly",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			registry := NewPlanRegistry(nil)
			rendered := tc.setup(registry)

			config, sections, err := registry.Assemble(context.Background(), rendered, nil)

			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.wantErr)
			assert.Empty(t, config)
			assert.Nil(t, sections)
		})
	}
}

func TestAssembleForeignNonceIsNotSpliced(t *testing.T) {
	registry := NewPlanRegistry(nil)
	foreign := NewPlanRegistry(nil)
	require.NotEqual(t, registry.nonce, foreign.nonce)

	rendered := "global\n" + foreign.sectionToken("backend", "be_a")
	config, sections, err := registry.Assemble(context.Background(), rendered, nil)

	require.NoError(t, err, "another render's token is text, not ours to splice")
	assert.Equal(t, rendered, config)
	assertPartitions(t, config, sections)
}

func TestAssembleSharedAuthorityRevalidatesCurrentSections(t *testing.T) {
	authority := NewPlanTokenAuthority()
	first, err := NewPlanRegistryWithAuthority(nil, authority)
	require.NoError(t, err)
	second, err := NewPlanRegistryWithAuthority(nil, authority)
	require.NoError(t, err)
	firstToken, err := first.Section("backend", "be_a", "backend be_a\n  old\n")
	require.NoError(t, err)
	secondToken, err := second.Section("backend", "be_a", "backend be_a\n  current\n")
	require.NoError(t, err)
	assert.Equal(t, firstToken, secondToken)

	config, sections, err := second.Assemble(t.Context(), "global\n"+firstToken, nil)
	require.NoError(t, err)
	assert.Equal(t, "global\nbackend be_a\n  current\n", config)
	assertPartitions(t, config, sections)

	unregistered, err := NewPlanRegistryWithAuthority(nil, authority)
	require.NoError(t, err)
	_, _, err = unregistered.Assemble(t.Context(), firstToken, nil)
	require.ErrorContains(t, err, `token for unregistered backend "be_a"`)
}

func failingPost(t *testing.T) PostProcessFunc {
	t.Helper()
	return func(context.Context, string) (string, error) {
		t.Fatal("post-processing must not run when nothing is spliced")
		return "", nil
	}
}

func sectionNames(sections []renderplan.Section) []string {
	names := make([]string, 0, len(sections))
	for _, section := range sections {
		names = append(names, section.Name)
	}
	return names
}

func sectionKinds(sections []renderplan.Section) []string {
	kinds := make([]string, 0, len(sections))
	for _, section := range sections {
		kinds = append(kinds, section.Kind)
	}
	return kinds
}
