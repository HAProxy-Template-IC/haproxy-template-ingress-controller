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
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/rendercontent"
)

func fragmentOf(t *testing.T, parts ...string) rendercontent.TextFragment {
	t.Helper()
	fragment := rendercontent.EmptyTextFragment()
	for index, text := range parts {
		next, err := fragment.WithPart(string(rune('a'+index)), text)
		require.NoError(t, err)
		fragment = next
	}
	return fragment
}

// TestAssembleDocumentSplicesFragmentByteIdenticallyToInlineText is the whole
// contract: a fragment must be indistinguishable from the template having
// emitted the text itself — same bytes, same section partition. The token
// follows text on its own line because the fragment supplies the newline that
// ends it, which is what an inline expression does.
func TestAssembleDocumentSplicesFragmentByteIdenticallyToInlineText(t *testing.T) {
	const spliced = "\n# rule a\n# rule b\n"
	const prefix = "global\n  # header"
	const suffix = "frontend fe\n"

	inlineRegistry := NewPlanRegistry(nil)
	inlineSource, err := renderDocumentFromString(prefix + spliced + suffix)
	require.NoError(t, err)
	inlineDocument, inlineSections, err := inlineRegistry.AssembleDocument(t.Context(), inlineSource, nil)
	require.NoError(t, err)

	fragmentRegistry := NewPlanRegistry(nil)
	token, err := fragmentRegistry.Fragment("rules", fragmentOf(t, "\n# rule a\n", "# rule b\n"))
	require.NoError(t, err)
	fragmentSource, err := renderDocumentFromString(prefix + token + suffix)
	require.NoError(t, err)
	fragmentDocument, fragmentSections, err := fragmentRegistry.AssembleDocument(t.Context(), fragmentSource, nil)
	require.NoError(t, err)

	assert.Equal(t, mustDocumentString(t, inlineDocument), mustDocumentString(t, fragmentDocument))
	assert.Equal(t, inlineSections, fragmentSections)
}

func TestAssembleDocumentRejectsUnregisteredFragment(t *testing.T) {
	registry := NewPlanRegistry(nil)
	token, err := registry.Fragment("rules", fragmentOf(t, "\n# rule\n"))
	require.NoError(t, err)

	other, err := NewPlanRegistryWithAuthority(nil, registry.authority)
	require.NoError(t, err)
	source, err := renderDocumentFromString("global\n" + token)
	require.NoError(t, err)

	_, _, err = other.AssembleDocument(t.Context(), source, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unregistered fragment")
}

// TestAssembleDocumentRejectsATokenInsideAFragment keeps the splice from
// becoming a forgery surface: fragment text is spliced verbatim, so a token in
// it would otherwise be re-read as a placeholder.
func TestAssembleDocumentRejectsATokenInsideAFragment(t *testing.T) {
	registry := NewPlanRegistry(nil)
	sectionToken, err := registry.Section("backend", "be_a", "backend be_a\n")
	require.NoError(t, err)

	token, err := registry.Fragment("rules", fragmentOf(t, "\n"+sectionToken))
	require.NoError(t, err)
	source, err := renderDocumentFromString("global\n" + token)
	require.NoError(t, err)

	_, _, err = registry.AssembleDocument(t.Context(), source, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "a token survived fragment")
}

func TestFragmentRejectsConflictingTextUnderOneName(t *testing.T) {
	registry := NewPlanRegistry(nil)
	_, err := registry.Fragment("rules", fragmentOf(t, "\n# first\n"))
	require.NoError(t, err)

	_, err = registry.Fragment("rules", fragmentOf(t, "\n# second\n"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "registered twice with different text")
}

// A fragment whose text does not end with a newline must still end its line.
//
// The token occupies a line of its own, so the splice owes the terminator that
// line carried. Without it the fragment's last line fuses with whatever the
// template writes next, and in a rendered HAProxy config that means two
// directives share a line: the second is swallowed and the rule it encoded is
// silently gone. That reached a cluster — three fused `http-request` lines per
// pod, and the Gateway route-winner lookup they carried never ran.
func TestAssembleDocumentEndsTheLineAFragmentDidNotTerminate(t *testing.T) {
	registry := NewPlanRegistry(nil)
	token, err := registry.Fragment("rules", fragmentOf(t, "\n  http-request set-var(txn.a) str(x)"))
	require.NoError(t, err)

	source, err := renderDocumentFromString(
		"frontend fe\n  # header" + token + "  http-request set-var(txn.b) str(y)\n")
	require.NoError(t, err)
	document, _, err := registry.AssembleDocument(t.Context(), source, nil)
	require.NoError(t, err)

	rendered := mustDocumentString(t, document)
	assert.NotContains(t, rendered, ")  http-request",
		"the fragment's last directive fused with the next one")
	assert.Contains(t, rendered, "str(x)\n  http-request set-var(txn.b)",
		"the next directive must start its own line")
}

// A fragment that contributes no text owes the token line's terminator too.
//
// The Gateway route libraries put the token directly after a section-marker
// comment. With nothing ranked into the fragment the comment swallowed the
// `set-var(txn.gw_rule_id)` directive behind it, so `route-winner.map` was never
// consulted and every Gateway route answered 404.
func TestAssembleDocumentEndsTheLineAnEmptyFragmentDidNotTerminate(t *testing.T) {
	registry := NewPlanRegistry(nil)
	token, err := registry.Fragment("rules", rendercontent.EmptyTextFragment())
	require.NoError(t, err)

	source, err := renderDocumentFromString(
		"frontend fe\n  # marker" + token + "  http-request set-var(txn.b) str(y)\n")
	require.NoError(t, err)
	document, _, err := registry.AssembleDocument(t.Context(), source, nil)
	require.NoError(t, err)

	rendered := mustDocumentString(t, document)
	assert.NotContains(t, rendered, "# marker  http-request",
		"the marker comment swallowed the directive behind it")
	assert.Contains(t, rendered, "# marker\n  http-request set-var(txn.b)",
		"the directive after an empty fragment must start its own line")
}

// A rendered line that merely looks like a fragment token is ordinary text.
//
// The marker carries a per-registry nonce, so template output cannot forge one:
// an annotation reflected verbatim into the config, a path, a header value —
// none of them know the nonce. Config that mentions the literal prefix has to
// survive as config.
func TestAssembleDocumentKeepsAMarkerLookalikeAsText(t *testing.T) {
	registry := NewPlanRegistry(nil)
	lookalike := "  http-request set-header X-Note \"# @haptic:not-the-nonce:fragment:rules@\""

	source, err := renderDocumentFromString("frontend fe\n" + lookalike + "\n")
	require.NoError(t, err)
	document, _, err := registry.AssembleDocument(t.Context(), source, nil)
	require.NoError(t, err)

	assert.Contains(t, mustDocumentString(t, document), lookalike,
		"a line that only resembles a token must render verbatim")
}
