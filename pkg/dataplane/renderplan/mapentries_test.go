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

package renderplan_test

import (
	"fmt"
	"math/rand/v2"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/renderplan"
)

func TestReparseMatchesParseMapEntriesAcrossRandomEdits(t *testing.T) {
	random := rand.New(rand.NewPCG(7, 11))
	lineKinds := []func(int) string{
		func(i int) string { return fmt.Sprintf("host-%d.example.com backend-%d", i, i%7) },
		func(i int) string { return fmt.Sprintf("  /path/%d\tbe%d  ", i, i) },
		func(i int) string { return "# comment " + fmt.Sprint(i) },
		func(int) string { return "" },
		func(i int) string { return fmt.Sprintf("bare-%d", i) },
		func(i int) string { return fmt.Sprintf("crlf-%d value\r", i) },
	}
	randomLine := func(i int) string { return lineKinds[random.IntN(len(lineKinds))](i) }
	lines := make([]string, 0, 64)
	for i := range 40 {
		lines = append(lines, randomLine(i))
	}
	join := func() string {
		content := strings.Join(lines, "\n")
		if random.IntN(2) == 0 {
			content += "\n"
		}
		return content
	}
	parsed := renderplan.ParseMapEntriesIndexed(join())
	require.Equal(t, renderplan.ParseMapEntries(parsed.Content), parsed.Entries)

	for step := range 400 {
		switch random.IntN(5) {
		case 0:
			if len(lines) > 0 {
				lines[random.IntN(len(lines))] = randomLine(1000 + step)
			}
		case 1:
			at := random.IntN(len(lines) + 1)
			lines = append(lines[:at], append([]string{randomLine(2000 + step)}, lines[at:]...)...)
		case 2:
			if len(lines) > 0 {
				at := random.IntN(len(lines))
				lines = append(lines[:at], lines[at+1:]...)
			}
		case 3:
			lines = lines[:0]
		default:
			for range random.IntN(30) {
				lines = append(lines, randomLine(3000+step))
			}
		}
		content := join()
		parsed = parsed.Reparse(content)
		require.Equal(t, content, parsed.Content)
		require.Equal(t, renderplan.ParseMapEntries(content), parsed.Entries, "step %d", step)
		fresh := renderplan.ParseMapEntriesIndexed(content)
		require.Equal(t, fresh, parsed, "step %d", step)
		require.True(t, renderplan.MapEntriesMatch(content, parsed.Entries), "step %d", step)
		if len(parsed.Entries) > 0 {
			changed := append([]renderplan.Entry(nil), parsed.Entries...)
			changed[random.IntN(len(changed))].Value += "x"
			require.False(t, renderplan.MapEntriesMatch(content, changed), "step %d", step)
			require.False(t, renderplan.MapEntriesMatch(content, parsed.Entries[:len(parsed.Entries)-1]), "step %d", step)
			require.False(t, renderplan.MapEntriesMatch(content, append(changed, renderplan.Entry{Key: "k"})), "step %d", step)
		}
	}
}

func TestMapEntriesMatchEmptyContent(t *testing.T) {
	require.True(t, renderplan.MapEntriesMatch("", nil))
	require.False(t, renderplan.MapEntriesMatch("", []renderplan.Entry{}))
	require.True(t, renderplan.MapEntriesMatch("# only a comment\n", nil))
	require.False(t, renderplan.MapEntriesMatch("# only a comment\n", []renderplan.Entry{}))
}

func TestReparseUnchangedContentReturnsTheSameEntries(t *testing.T) {
	parsed := renderplan.ParseMapEntriesIndexed("a 1\nb 2\n")
	again := parsed.Reparse("a 1\nb 2\n")
	require.Equal(t, parsed, again)
	require.Same(t, &parsed.Entries[0], &again.Entries[0])
}
