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

package rendercontext

import (
	"context"
	"errors"
	"io"
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/names"
	"gitlab.com/haproxy-haptic/haptic/pkg/rendercontent"
	"gitlab.com/haproxy-haptic/haptic/pkg/templating"
)

func TestRenderDocumentWriterRetainsAuthenticatedFragments(t *testing.T) {
	output, err := rendercontent.FromSorted([]rendercontent.Change{
		{Key: "a", Text: "alpha"},
		{Key: "b", Text: "beta"},
	})
	require.NoError(t, err)

	first := &renderDocumentWriter{}
	_, err = first.Write([]byte("before:"))
	require.NoError(t, err)
	require.NoError(t, first.WriteTextFragment(output))
	_, err = first.Write([]byte(":after"))
	require.NoError(t, err)
	document, err := first.builder.Build(nil)
	require.NoError(t, err)
	text, err := document.String()
	require.NoError(t, err)
	assert.Equal(t, "before:alphabeta:after", text)
	assert.Equal(t, int64(len(text)), first.bytes)

	second := &renderDocumentWriter{}
	_, err = second.Write([]byte("before:"))
	require.NoError(t, err)
	require.NoError(t, second.WriteTextFragment(output))
	_, err = second.Write([]byte(":after"))
	require.NoError(t, err)
	reused, err := second.builder.Build(&document)
	require.NoError(t, err)
	same, err := document.SameRoot(reused)
	require.NoError(t, err)
	assert.True(t, same)
}

func TestRenderDocumentWriterRetainsAuthenticatedTextFragments(t *testing.T) {
	fragment, err := rendercontent.TextFragmentFromSorted([]rendercontent.TextPart{
		{Key: "a", Text: "alpha"},
		{Key: "b", Text: "beta"},
	})
	require.NoError(t, err)

	first := &renderDocumentWriter{}
	_, err = first.Write([]byte("before:"))
	require.NoError(t, err)
	require.NoError(t, first.WriteTextFragment(fragment))
	_, err = first.Write([]byte(":after"))
	require.NoError(t, err)
	document, err := first.builder.Build(nil)
	require.NoError(t, err)
	leaves, err := document.Leaves()
	require.NoError(t, err)
	assert.Equal(t, 3, leaves)
	text, err := document.String()
	require.NoError(t, err)
	assert.Equal(t, "before:alphabeta:after", text)
	assert.Equal(t, int64(len(text)), first.bytes)

	second := &renderDocumentWriter{}
	_, err = second.Write([]byte("before:"))
	require.NoError(t, err)
	require.NoError(t, second.WriteTextFragment(&fragment))
	_, err = second.Write([]byte(":after"))
	require.NoError(t, err)
	reused, err := second.builder.Build(&document)
	require.NoError(t, err)
	same, err := document.SameRoot(reused)
	require.NoError(t, err)
	assert.True(t, same)

	changed, err := fragment.WithPart("b", "changed")
	require.NoError(t, err)
	third := &renderDocumentWriter{}
	_, err = third.Write([]byte("before:"))
	require.NoError(t, err)
	require.NoError(t, third.WriteTextFragment(changed))
	_, err = third.Write([]byte(":after"))
	require.NoError(t, err)
	changedDocument, err := third.builder.Build(&document)
	require.NoError(t, err)
	same, err = document.SameRoot(changedDocument)
	require.NoError(t, err)
	assert.False(t, same)
	changedText, err := changedDocument.String()
	require.NoError(t, err)
	assert.Equal(t, "before:alphachanged:after", changedText)
}

func TestRenderDocumentWriterRetainsRankedTemplateFragment(t *testing.T) {
	engine, err := templating.New(map[string]string{
		"main": `before:{{ incremental_ranked_text_fragment("group", "lines") }}:after`,
	}, nil)
	require.NoError(t, err)
	fragment, err := rendercontent.TextFragmentFromSorted([]rendercontent.TextPart{
		{Key: "a", Text: "alpha"},
		{Key: "b", Text: "beta"},
	})
	require.NoError(t, err)
	renderer := &renderDocumentTestRenderer{fragment: fragment}
	ctx := templating.WithIncrementalRenderer(t.Context(), renderer)
	writer := &renderDocumentWriter{}

	_, err = engine.RenderRawTo(ctx, "main", map[string]any{}, writer)
	require.NoError(t, err)
	document, err := writer.builder.Build(nil)
	require.NoError(t, err)
	leaves, err := document.Leaves()
	require.NoError(t, err)
	assert.Equal(t, 3, leaves)
	text, err := document.String()
	require.NoError(t, err)
	assert.Equal(t, "before:alphabeta:after", text)
}

func TestRenderDocumentWriterRejectsPoisonedFragment(t *testing.T) {
	var poisoned rendercontent.Output

	writer := &renderDocumentWriter{}
	err := writer.WriteTextFragment(poisoned)
	require.Error(t, err)
	document, buildErr := writer.builder.Build(nil)
	require.NoError(t, buildErr)
	text, stringErr := document.String()
	require.NoError(t, stringErr)
	assert.Empty(t, text)
}

func TestRenderDocumentWriterRejectsPoisonedTextFragment(t *testing.T) {
	var poisoned rendercontent.TextFragment
	tests := []struct {
		name     string
		fragment templating.TextFragment
	}{
		{name: "value", fragment: poisoned},
		{name: "pointer", fragment: &poisoned},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			writer := &renderDocumentWriter{}
			err := writer.WriteTextFragment(test.fragment)
			require.Error(t, err)
			assert.Zero(t, writer.bytes)
			document, buildErr := writer.builder.Build(nil)
			require.NoError(t, buildErr)
			text, stringErr := document.String()
			require.NoError(t, stringErr)
			assert.Empty(t, text)
		})
	}
}

func TestRenderDocumentWriterValidatesUnknownFragment(t *testing.T) {
	sentinel := errors.New("fragment failed")
	tests := []struct {
		name     string
		fragment testTextFragment
		wantErr  error
	}{
		{name: "negative", fragment: testTextFragment{text: "value", reported: -1}},
		{name: "mismatch", fragment: testTextFragment{text: "value", reported: 4}},
		{name: "error", fragment: testTextFragment{text: "value", reported: 5, err: sentinel}, wantErr: sentinel},
		{name: "valid", fragment: testTextFragment{text: "value", reported: 5}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			writer := &renderDocumentWriter{}
			err := writer.WriteTextFragment(test.fragment)
			if test.name == "valid" {
				require.NoError(t, err)
				document, buildErr := writer.builder.Build(nil)
				require.NoError(t, buildErr)
				text, stringErr := document.String()
				require.NoError(t, stringErr)
				assert.Equal(t, "value", text)
				return
			}
			if test.wantErr != nil {
				require.ErrorIs(t, err, test.wantErr)
			} else {
				require.Error(t, err)
			}
		})
	}

	writer := &renderDocumentWriter{}
	require.Error(t, writer.WriteTextFragment(nil))
	var typedNil *testPointerTextFragment
	require.Error(t, writer.WriteTextFragment(typedNil))
}

func TestRenderDocumentWriterRejectsOverflowAndSuppressedWriterError(t *testing.T) {
	writer := &renderDocumentWriter{bytes: math.MaxInt64}
	written, err := writer.Write([]byte("x"))
	require.Error(t, err)
	assert.Zero(t, written)

	err = writer.WriteTextFragment(testSuppressingTextFragment{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "platform limit")
}

func TestRenderMainReusesOnlyExactAuthenticatedDocument(t *testing.T) {
	engine, err := templating.New(map[string]string{
		names.MainTemplateName: `{{ incremental_render("lines") }}`,
	}, nil)
	require.NoError(t, err)
	output, err := rendercontent.FromSorted([]rendercontent.Change{{Key: "a", Text: "alpha"}})
	require.NoError(t, err)
	renderer := &renderDocumentTestRenderer{fragment: output}
	ctx := templating.WithIncrementalRenderer(t.Context(), renderer)
	state := newRenderCacheTestState(t, engine)

	first, err := state.renderMain(t, ctx, map[string]any{}, NewPlanRegistry(nil))
	require.NoError(t, err)
	assert.Equal(t, "alpha\n", first.Config)
	firstRoot := state.publication.candidate.document.document
	require.NoError(t, firstRoot.ValidateAuthentication())

	second, err := state.renderMain(t, ctx, map[string]any{}, NewPlanRegistry(nil))
	require.NoError(t, err)
	assert.Equal(t, first.Config, second.Config)
	same, err := firstRoot.SameRoot(state.publication.candidate.document.document)
	require.NoError(t, err)
	assert.True(t, same)

	changed, err := output.WithText("a", "beta")
	require.NoError(t, err)
	renderer.fragment = changed
	third, err := state.renderMain(t, ctx, map[string]any{}, NewPlanRegistry(nil))
	require.NoError(t, err)
	assert.Equal(t, "beta\n", third.Config)
	same, err = firstRoot.SameRoot(state.publication.candidate.document.document)
	require.NoError(t, err)
	assert.False(t, same)

	validPublication := state.publication
	validGeneration := validPublication.candidate.document
	validRoot := validGeneration.document
	var poisonedOutput rendercontent.Output
	renderer.fragment = poisonedOutput
	_, err = state.renderMain(t, ctx, map[string]any{}, NewPlanRegistry(nil))
	require.Error(t, err)
	same, sameErr := validRoot.SameRoot(state.publication.candidate.document.document)
	require.NoError(t, sameErr)
	assert.True(t, same)
	assert.Same(t, validPublication, state.publication)

	poisonedGeneration := *validGeneration
	poisonedGeneration.document = rendercontent.Document{}
	poisonedGeneration.seal = &poisonedGeneration
	poisonedCandidate := *validPublication.candidate
	poisonedCandidate.document = &poisonedGeneration
	poisonedCandidate.seal = &poisonedCandidate
	poisonedPublication := *validPublication
	poisonedPublication.candidate = &poisonedCandidate
	poisonedPublication.seal = &poisonedPublication
	renderer.fragment = changed
	_, err = state.cache.Begin(engine, state.occurrence+1, &poisonedPublication)
	require.ErrorContains(t, err, "invalid root")
}

type renderDocumentTestRenderer struct {
	fragment templating.TextFragment
}

func (r *renderDocumentTestRenderer) RenderIncremental(context.Context, string) (string, error) {
	return "legacy", nil
}

func (r *renderDocumentTestRenderer) RenderIncrementalTextFragment(
	context.Context,
	string,
) (templating.TextFragment, error) {
	return r.fragment, nil
}

func (r *renderDocumentTestRenderer) IncrementalRankedTextFragment(
	context.Context,
	string,
	string,
) (templating.TextFragment, error) {
	return r.fragment, nil
}

type testTextFragment struct {
	text     string
	reported int64
	err      error
}

func (f testTextFragment) WriteTo(writer io.Writer) (int64, error) {
	_, writeErr := io.WriteString(writer, f.text)
	if writeErr != nil {
		return f.reported, writeErr
	}
	return f.reported, f.err
}

type testPointerTextFragment struct{}

func (*testPointerTextFragment) WriteTo(io.Writer) (int64, error) {
	panic("typed nil fragment was invoked")
}

type testSuppressingTextFragment struct{}

func (testSuppressingTextFragment) WriteTo(writer io.Writer) (int64, error) {
	_, _ = writer.Write([]byte("x"))
	return 0, nil
}
