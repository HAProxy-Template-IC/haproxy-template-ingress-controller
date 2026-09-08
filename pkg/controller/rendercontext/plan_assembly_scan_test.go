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

package rendercontext

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/rendercontent"
)

// The token visitor must see exactly the lines a line-by-line walk would hand
// to the classifier, whatever leaf boundaries the document has: a marker or
// its line may straddle two leaves, and the last line may lack a newline.
func TestVisitDocumentTokensMatchesALineWalk(t *testing.T) {
	const marker = "@haptic:nonce"
	lines := []string{
		"global\n",
		"  daemon\n",
		"# " + marker + ":section:backend:be_a@\n",
		"frontend http\n",
		"  bind :80 # " + marker + ":fragment:rules@\n",
		"  default_backend be_a\n",
		"# " + marker + ":section:backend:be_b@\n",
		"# trailing without newline " + marker,
	}
	text := strings.Join(lines, "")
	for _, cut := range []int{1, 5, 17, 40, len(text) / 2, len(text) - 3} {
		var builder rendercontent.DocumentBuilder
		var first, second rendercontent.DocumentBuilder
		_, _ = first.WriteString(text[:cut])
		_, _ = second.WriteString(text[cut:])
		left, err := first.Build(nil)
		require.NoError(t, err)
		right, err := second.Build(nil)
		require.NoError(t, err)
		require.NoError(t, builder.AppendDocument(left))
		require.NoError(t, builder.AppendDocument(right))
		document, err := builder.Build(nil)
		require.NoError(t, err)

		var events []string
		var tokens []string
		err = visitDocumentTokens(document, marker,
			func(run string) error {
				assert.NotContains(t, run, marker, "cut at %d", cut)
				events = append(events, run)
				return nil
			},
			func(line string) error {
				tokens = append(tokens, line)
				events = append(events, line)
				return nil
			})
		require.NoError(t, err, "cut at %d", cut)
		assert.Equal(t, []string{lines[2], lines[4], lines[6], lines[7]}, tokens, "cut at %d", cut)
		assert.Equal(t, text, strings.Join(events, ""), "cut at %d: every byte is either a run or a token line, in order", cut)
	}
}

func TestValidSectionNameMatchesThePattern(t *testing.T) {
	for _, name := range []string{"be_a", "gtw_mesh-x_app.1:80", "A", "0-9"} {
		assert.True(t, validSectionName(name), name)
		assert.True(t, sectionNamePattern.MatchString(name), name)
	}
	for _, name := range []string{"", "be a", "be/a", "bé", "a@b", "a\n"} {
		assert.False(t, validSectionName(name), name)
		assert.False(t, sectionNamePattern.MatchString(name), name)
	}
}
