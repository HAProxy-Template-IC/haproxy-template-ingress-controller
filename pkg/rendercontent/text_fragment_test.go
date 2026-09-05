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

package rendercontent

import (
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var _ io.WriterTo = TextFragment{}

func TestTextFragmentDistinguishesPresentEmptyFromAbsent(t *testing.T) {
	empty := EmptyTextFragment()
	present, err := empty.WithPart("part", "")
	require.NoError(t, err)

	text, found, err := present.Get("part")
	require.NoError(t, err)
	assert.True(t, found)
	assert.Empty(t, text)
	assertTextFragmentString(t, present, "")
	parts, err := present.Parts()
	require.NoError(t, err)
	assert.Equal(t, 1, parts)
	same, err := empty.SameRoot(present)
	require.NoError(t, err)
	assert.False(t, same)

	withDelimiter, err := present.WithDelimiter("ignored")
	require.NoError(t, err)
	assert.Same(t, present.state, withDelimiter.state)

	twoPresent, err := withDelimiter.WithPart("second", "")
	require.NoError(t, err)
	assertTextFragmentString(t, twoPresent, "")
	joined, err := twoPresent.WithDelimiter("|")
	require.NoError(t, err)
	assertTextFragmentString(t, joined, "|")

	canonical, err := joined.Delete("second")
	require.NoError(t, err)
	assertTextFragmentString(t, canonical, "")
	assert.Nil(t, canonical.state.delimiter)
	readded, err := canonical.WithPart("second", "")
	require.NoError(t, err)
	assertTextFragmentString(t, readded, "")

	deleted, err := canonical.Delete("part")
	require.NoError(t, err)
	assert.Same(t, emptyTextFragment.state, deleted.state)
}

func TestTextFragmentDelimiterSemantics(t *testing.T) {
	tests := []struct {
		name      string
		texts     []string
		delimiter string
		want      string
	}{
		{name: "absent", delimiter: "|", want: ""},
		{name: "one empty", texts: []string{""}, delimiter: "|", want: ""},
		{name: "one text", texts: []string{"one"}, delimiter: "|", want: "one"},
		{name: "two empty", texts: []string{"", ""}, delimiter: "|", want: "|"},
		{name: "empty edges", texts: []string{"", "x", ""}, delimiter: "|", want: "|x|"},
		{name: "empty middle", texts: []string{"a", "", "b"}, delimiter: "|", want: "a||b"},
		{name: "empty delimiter", texts: []string{"a", "", "b"}, want: "ab"},
		{name: "nul delimiter", texts: []string{"", "x", ""}, delimiter: "\x00::\x00", want: "\x00::\x00x\x00::\x00"},
		{name: "multibyte text", texts: []string{"\u00e4", "\u03b2"}, delimiter: "\u2192", want: "\u00e4\u2192\u03b2"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			parts := make([]TextPart, len(test.texts))
			for index, text := range test.texts {
				parts[index] = TextPart{Key: fmt.Sprintf("part-%03d", index), Text: text}
			}
			fragment, err := TextFragmentFromSorted(parts)
			require.NoError(t, err)
			root := fragment.state.root
			fragment, err = fragment.WithDelimiter(test.delimiter)
			require.NoError(t, err)
			assert.Same(t, root, fragment.state.root)
			assertTextFragmentString(t, fragment, test.want)
			assertTextFragmentBoundary(t, fragment, test.want)
			assertTextFragmentInvariants(t, fragment)

			var streamed strings.Builder
			written, err := fragment.WriteTo(&streamed)
			require.NoError(t, err)
			assert.EqualValues(t, len(test.want), written)
			assert.Equal(t, test.want, streamed.String())
		})
	}
}

func TestTextFragmentPathCopiesExactChanges(t *testing.T) {
	fragment, err := TextFragmentFromSorted([]TextPart{
		{Key: "a", Text: "a"}, {Key: "b", Text: ""}, {Key: "c", Text: "c"},
		{Key: "d", Text: "d"}, {Key: "e", Text: ""}, {Key: "f", Text: "f"},
		{Key: "g", Text: "g"},
	})
	require.NoError(t, err)
	fragment, err = fragment.WithDelimiter("|")
	require.NoError(t, err)

	unchanged, err := fragment.WithPart("b", "")
	require.NoError(t, err)
	assert.Same(t, fragment.state, unchanged.state)

	changed, err := fragment.WithPart("a", "A")
	require.NoError(t, err)
	assert.NotSame(t, fragment.state, changed.state)
	assert.Same(t, fragment.state.root.right, changed.state.root.right)
	assert.Same(t, fragment.state.root.left.right, changed.state.root.left.right)
	assert.NotSame(t, fragment.state.root.left.left, changed.state.root.left.left)
	assert.Same(t, fragment.state.delimiter, changed.state.delimiter)
	assertTextFragmentString(t, fragment, "a||c|d||f|g")
	assertTextFragmentString(t, changed, "A||c|d||f|g")

	removed, err := changed.Delete("a")
	require.NoError(t, err)
	assert.Same(t, changed.state.root.right, removed.state.root.right)
	absent, err := removed.Delete("a")
	require.NoError(t, err)
	assert.Same(t, removed.state, absent.state)
	assertTextFragmentInvariants(t, fragment)
	assertTextFragmentInvariants(t, changed)
	assertTextFragmentInvariants(t, removed)
}

func TestTextFragmentApplyIsAtomicAndUsesBulkColdBuild(t *testing.T) {
	fragment, err := TextFragmentFromSorted([]TextPart{{Key: "a", Text: ""}, {Key: "c", Text: "c"}})
	require.NoError(t, err)
	fragment, err = fragment.WithDelimiter("|")
	require.NoError(t, err)
	changed, err := fragment.Apply([]TextFragmentChange{
		{Key: "d", Text: "d", Present: true},
		{Key: "a", Present: false},
		{Key: "b", Text: "", Present: true},
	})
	require.NoError(t, err)
	assertTextFragmentString(t, fragment, "|c")
	assertTextFragmentString(t, changed, "|c|d")

	unchanged, err := fragment.Apply([]TextFragmentChange{
		{Key: "missing", Text: "ignored", Present: false},
		{Key: "c", Text: "c", Present: true},
		{Key: "a", Text: "", Present: true},
	})
	require.NoError(t, err)
	assert.Same(t, fragment.state, unchanged.state)

	_, err = fragment.Apply([]TextFragmentChange{
		{Key: "same", Text: "a", Present: true},
		{Key: "same", Present: false},
	})
	require.ErrorIs(t, err, errDuplicateTextFragmentChange)
	_, err = fragment.Apply([]TextFragmentChange{
		{Key: "b", Text: "b", Present: true},
		{Key: "", Text: "invalid", Present: true},
	})
	require.ErrorIs(t, err, errEmptyTextFragmentPartKey)
	assertTextFragmentString(t, fragment, "|c")

	cold, err := EmptyTextFragment().Apply([]TextFragmentChange{
		{Key: "d", Text: "d", Present: true},
		{Key: "a", Text: "ignored", Present: false},
		{Key: "c", Text: "", Present: true},
		{Key: "b", Text: "b", Present: true},
	})
	require.NoError(t, err)
	assertTextFragmentString(t, cold, "bd")
	assert.Equal(t, 2, cold.state.root.height)
	assertTextFragmentInvariants(t, cold)
}

func TestTextFragmentDelimiterIdentityIsExactAndCanonical(t *testing.T) {
	base, err := TextFragmentFromSorted([]TextPart{{Key: "a", Text: "a"}, {Key: "b", Text: "b"}})
	require.NoError(t, err)
	first, err := base.WithDelimiter("|")
	require.NoError(t, err)
	second, err := base.WithDelimiter(string([]byte{'|'}))
	require.NoError(t, err)
	assert.NotSame(t, first.state.delimiter, second.state.delimiter)
	same, err := first.SameRoot(second)
	require.NoError(t, err)
	assert.True(t, same)

	different, err := base.WithDelimiter("::")
	require.NoError(t, err)
	same, err = first.SameRoot(different)
	require.NoError(t, err)
	assert.False(t, same)

	one, err := TextFragmentFromSorted([]TextPart{{Key: "a", Text: "a"}})
	require.NoError(t, err)
	canonical, err := one.WithDelimiter("|")
	require.NoError(t, err)
	assert.Same(t, one.state, canonical.state)
	empty, err := EmptyTextFragment().WithDelimiter("|")
	require.NoError(t, err)
	assert.Same(t, emptyTextFragment.state, empty.state)

	independent, err := TextFragmentFromSorted([]TextPart{{Key: "a", Text: "a"}, {Key: "b", Text: "b"}})
	require.NoError(t, err)
	independent, err = independent.WithDelimiter("|")
	require.NoError(t, err)
	same, err = first.SameRoot(independent)
	require.NoError(t, err)
	assert.False(t, same)
}

func TestTextFragmentRandomUpdatesMatchOrderedMap(t *testing.T) {
	fragment := EmptyTextFragment()
	want := map[string]string{}
	for operation := range 10_000 {
		key := fmt.Sprintf("key-%04d", rand.IntN(500))
		if rand.IntN(4) == 0 {
			delete(want, key)
			var err error
			fragment, err = fragment.Delete(key)
			require.NoError(t, err)
		} else {
			text := ""
			if rand.IntN(3) != 0 {
				text = fmt.Sprintf("value-%d", operation)
			}
			want[key] = text
			var err error
			fragment, err = fragment.WithPart(key, text)
			require.NoError(t, err)
		}
		if operation%97 == 0 {
			assertTextFragmentMatchesMap(t, fragment, want, "")
			assertTextFragmentInvariants(t, fragment)
		}
	}
	fragment, err := fragment.WithDelimiter("\x00|\x00")
	require.NoError(t, err)
	assertTextFragmentMatchesMap(t, fragment, want, "\x00|\x00")
	assertTextFragmentInvariants(t, fragment)
}

func TestTextFragmentRejectsInvalidConstruction(t *testing.T) {
	_, err := TextFragmentFromSorted([]TextPart{{Key: "", Text: "value"}})
	require.ErrorIs(t, err, errEmptyTextFragmentPartKey)
	_, err = TextFragmentFromSorted([]TextPart{{Key: "b", Text: "b"}, {Key: "a", Text: "a"}})
	require.ErrorIs(t, err, errUnsortedTextFragmentParts)
	_, err = TextFragmentFromSorted([]TextPart{{Key: "a", Text: "a"}, {Key: "a", Text: ""}})
	require.ErrorIs(t, err, errDuplicateTextFragmentChange)
	_, err = buildBalancedTextFragment(0, func(int) (string, string) { return "", "" })
	require.ErrorIs(t, err, errInvalidTextFragment)
	_, err = buildBalancedTextFragment(1, nil)
	require.ErrorIs(t, err, errInvalidTextFragment)
}

func TestTextFragmentGetWalkAndInputIsolation(t *testing.T) {
	parts := []TextPart{{Key: "a", Text: "alpha"}, {Key: "b", Text: ""}, {Key: "c", Text: "charlie"}}
	fragment, err := TextFragmentFromSorted(parts)
	require.NoError(t, err)
	parts[0] = TextPart{Key: "z", Text: "poison"}

	text, found, err := fragment.Get("b")
	require.NoError(t, err)
	assert.True(t, found)
	assert.Empty(t, text)
	_, found, err = fragment.Get("missing")
	require.NoError(t, err)
	assert.False(t, found)
	_, _, err = fragment.Get("")
	require.ErrorIs(t, err, errEmptyTextFragmentPartKey)

	var visited []TextPart
	err = fragment.Walk(func(key, text string) error {
		visited = append(visited, TextPart{Key: key, Text: text})
		return nil
	})
	require.NoError(t, err)
	assert.Equal(t, []TextPart{{Key: "a", Text: "alpha"}, {Key: "b", Text: ""}, {Key: "c", Text: "charlie"}}, visited)
	require.Error(t, fragment.Walk(nil))
	sentinel := errors.New("stop")
	count := 0
	err = fragment.Walk(func(string, string) error {
		count++
		return sentinel
	})
	require.ErrorIs(t, err, sentinel)
	assert.Equal(t, 1, count)
}

func TestTextFragmentRejectsSealRootAndMetadataPoison(t *testing.T) {
	fragment, err := TextFragmentFromSorted([]TextPart{{Key: "a", Text: ""}, {Key: "b", Text: "safe"}})
	require.NoError(t, err)
	fragment, err = fragment.WithDelimiter("|")
	require.NoError(t, err)

	poisonedRoot := cloneTextFragmentHandleState(fragment)
	poisonedRoot.state.root = mustOutputLeaf(t, "evil")
	require.ErrorIs(t, poisonedRoot.ValidateAuthentication(), errInvalidTextFragment)
	poisonedSize := cloneTextFragmentHandleState(fragment)
	poisonedSize.state.bytes++
	require.ErrorIs(t, poisonedSize.ValidateAuthentication(), errInvalidTextFragment)
	poisonedParts := cloneTextFragmentHandleState(fragment)
	poisonedParts.state.parts++
	require.ErrorIs(t, poisonedParts.ValidateAuthentication(), errInvalidTextFragment)
	poisonedDelimiter := cloneTextFragmentHandleState(fragment)
	poisonedDelimiter.state.delimiter = &textFragmentDelimiter{text: ";"}
	require.ErrorIs(t, poisonedDelimiter.ValidateAuthentication(), errInvalidTextFragment)
	poisonedEmptyDelimiter := cloneTextFragmentHandleState(fragment)
	poisonedEmptyDelimiter.state.delimiter = &textFragmentDelimiter{}
	poisonedEmptyDelimiter.state.auth.delimiter = poisonedEmptyDelimiter.state.delimiter
	require.ErrorIs(t, poisonedEmptyDelimiter.ValidateAuthentication(), errInvalidTextFragment)
	poisonedMemo := cloneTextFragmentHandleState(fragment)
	poisonedMemo.state.memo = &textFragmentMemo{}
	require.ErrorIs(t, poisonedMemo.ValidateAuthentication(), errInvalidTextFragment)
	poisonedAuth := cloneTextFragmentHandleState(fragment)
	poisonedAuth.state.auth.root = mustOutputLeaf(t, "")
	require.ErrorIs(t, poisonedAuth.ValidateAuthentication(), errInvalidTextFragment)

	one, err := TextFragmentFromSorted([]TextPart{{Key: "a", Text: "a"}})
	require.NoError(t, err)
	poisonedCanonical := cloneTextFragmentHandleState(one)
	poisonedCanonical.state.delimiter = &textFragmentDelimiter{text: "|"}
	poisonedCanonical.state.auth.delimiter = poisonedCanonical.state.delimiter
	require.ErrorIs(t, poisonedCanonical.ValidateAuthentication(), errInvalidTextFragment)

	copied := fragment
	require.NoError(t, copied.ValidateAuthentication())
	assert.Same(t, fragment.state, copied.state)
	var zero TextFragment
	require.ErrorIs(t, zero.ValidateAuthentication(), errInvalidTextFragment)
	require.ErrorIs(t, new(TextFragment).ValidateAuthentication(), errInvalidTextFragment)
	_, err = fragment.SameRoot(zero)
	require.ErrorIs(t, err, errInvalidTextFragment)
}

func TestTextFragmentRejectsAggregateAndDelimiterOverflow(t *testing.T) {
	maximum := int(^uint(0) >> 1)
	root := &outputNode{bytes: maximum, parts: 2}
	_, ok := textFragmentByteSize(root, &textFragmentDelimiter{text: "|"})
	assert.False(t, ok)
	_, err := sealTextFragment(root, &textFragmentDelimiter{text: "|"})
	require.ErrorIs(t, err, errTextFragmentTooLarge)

	root = &outputNode{parts: maximum}
	_, ok = textFragmentByteSize(root, &textFragmentDelimiter{text: "||"})
	assert.False(t, ok)
	root = &outputNode{bytes: -1, parts: 1}
	_, ok = textFragmentByteSize(root, nil)
	assert.False(t, ok)
	root = &outputNode{parts: -1}
	_, ok = textFragmentByteSize(root, nil)
	assert.False(t, ok)
	root = &outputNode{}
	_, ok = textFragmentByteSize(root, nil)
	assert.False(t, ok)

	left := &outputNode{bytes: maximum, parts: 1}
	_, err = makeNode("key", "x", left, nil)
	require.ErrorIs(t, textFragmentNodeError(err), errTextFragmentTooLarge)
}

func TestTextFragmentWriterContracts(t *testing.T) {
	fragment, err := TextFragmentFromSorted([]TextPart{{Key: "part", Text: "alpha"}})
	require.NoError(t, err)
	_, err = fragment.WriteTo(nil)
	require.Error(t, err)

	sentinel := errors.New("write failed")
	tests := []struct {
		name        string
		count       int
		writeErr    error
		wantWritten int64
		wantErr     error
	}{
		{name: "zero", count: 0, wantErr: io.ErrShortWrite},
		{name: "short", count: 4, wantWritten: 4, wantErr: io.ErrShortWrite},
		{name: "negative", count: -1, wantErr: errInvalidTextFragmentWrite},
		{name: "oversize", count: 6, wantErr: errInvalidTextFragmentWrite},
		{name: "negative with error", count: -1, writeErr: sentinel, wantErr: errInvalidTextFragmentWrite},
		{name: "oversize with error", count: 6, writeErr: sentinel, wantErr: errInvalidTextFragmentWrite},
		{name: "partial with error", count: 4, writeErr: sentinel, wantWritten: 4, wantErr: sentinel},
		{name: "full with error", count: 5, writeErr: sentinel, wantWritten: 5, wantErr: sentinel},
	}
	for _, test := range tests {
		t.Run(test.name+" Write", func(t *testing.T) {
			written, writeErr := fragment.WriteTo(fixedWriter{count: test.count, err: test.writeErr})
			require.ErrorIs(t, writeErr, test.wantErr)
			assert.Equal(t, test.wantWritten, written)
		})
		t.Run(test.name+" WriteString", func(t *testing.T) {
			written, writeErr := fragment.WriteTo(fixedStringWriter{count: test.count, err: test.writeErr})
			require.ErrorIs(t, writeErr, test.wantErr)
			assert.Equal(t, test.wantWritten, written)
		})
	}

	joined, err := TextFragmentFromSorted([]TextPart{{Key: "a", Text: "a"}, {Key: "b", Text: "b"}})
	require.NoError(t, err)
	joined, err = joined.WithDelimiter("--")
	require.NoError(t, err)
	writer := &scriptedStringWriter{counts: []int{1, 1}}
	written, err := joined.WriteTo(writer)
	require.ErrorIs(t, err, io.ErrShortWrite)
	assert.EqualValues(t, 2, written)
	assert.Equal(t, []string{"a", "--"}, writer.values)
}

func TestTextFragmentStringMemoizationIsConcurrent(t *testing.T) {
	parts := make([]TextPart, 1024)
	for index := range parts {
		parts[index] = TextPart{Key: fmt.Sprintf("part-%04d", index), Text: strings.Repeat("x", 4)}
	}
	fragment, err := TextFragmentFromSorted(parts)
	require.NoError(t, err)
	fragment, err = fragment.WithDelimiter("|")
	require.NoError(t, err)
	const workers = 64
	var wait sync.WaitGroup
	wait.Add(workers)
	for range workers {
		go func() {
			defer wait.Done()
			text, stringErr := fragment.String()
			assert.NoError(t, stringErr)
			assert.Len(t, text, 5*len(parts)-1)
		}()
	}
	wait.Wait()
}

func assertTextFragmentMatchesMap(t *testing.T, fragment TextFragment, values map[string]string, delimiter string) {
	t.Helper()
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	texts := make([]string, len(keys))
	for index, key := range keys {
		texts[index] = values[key]
	}
	want := strings.Join(texts, delimiter)
	assertTextFragmentString(t, fragment, want)
	parts, err := fragment.Parts()
	require.NoError(t, err)
	assert.Equal(t, len(values), parts)
}

func assertTextFragmentString(t *testing.T, fragment TextFragment, want string) {
	t.Helper()
	got, err := fragment.String()
	require.NoError(t, err)
	assert.Equal(t, want, got)
	bytes, err := fragment.Bytes()
	require.NoError(t, err)
	assert.Equal(t, len(want), bytes)
}

func assertTextFragmentBoundary(t *testing.T, fragment TextFragment, want string) {
	t.Helper()
	first, firstPresent, err := fragment.FirstByte()
	require.NoError(t, err)
	last, lastPresent, err := fragment.LastByte()
	require.NoError(t, err)
	if want == "" {
		assert.False(t, firstPresent)
		assert.False(t, lastPresent)
		return
	}
	assert.True(t, firstPresent)
	assert.True(t, lastPresent)
	assert.Equal(t, want[0], first)
	assert.Equal(t, want[len(want)-1], last)
}

func assertTextFragmentInvariants(t *testing.T, fragment TextFragment) {
	t.Helper()
	require.NoError(t, fragment.ValidateAuthentication())
	got, err := validateTextFragmentNodeInvariants(fragment.state.root)
	require.NoError(t, err)
	assert.Equal(t, nodeHeight(fragment.state.root), got.height)
	assert.Equal(t, nodeBytes(fragment.state.root), got.bytes)
	assert.Equal(t, fragment.state.parts, got.parts)
	wantBytes := got.bytes
	if got.parts > 1 {
		wantBytes += (got.parts - 1) * len(textFragmentDelimiterText(fragment.state.delimiter))
	}
	assert.Equal(t, fragment.state.bytes, wantBytes)
	if got.parts < 2 {
		assert.Nil(t, fragment.state.delimiter)
	}
}

func cloneTextFragmentHandleState(fragment TextFragment) TextFragment {
	state := *fragment.state
	state.seal = &state
	return TextFragment{state: &state}
}

type textFragmentNodeInvariants struct {
	height  int
	bytes   int
	parts   int
	minimum string
	maximum string
}

func validateTextFragmentNodeInvariants(node *outputNode) (textFragmentNodeInvariants, error) {
	if node == nil {
		return textFragmentNodeInvariants{}, nil
	}
	left, err := validateTextFragmentNodeInvariants(node.left)
	if err != nil {
		return textFragmentNodeInvariants{}, err
	}
	right, err := validateTextFragmentNodeInvariants(node.right)
	if err != nil {
		return textFragmentNodeInvariants{}, err
	}
	if node.key == "" {
		return textFragmentNodeInvariants{}, errors.New("node has an empty key")
	}
	if node.left != nil && left.maximum >= node.key {
		return textFragmentNodeInvariants{}, fmt.Errorf("left key %q is not before %q", left.maximum, node.key)
	}
	if node.right != nil && right.minimum <= node.key {
		return textFragmentNodeInvariants{}, fmt.Errorf("right key %q is not after %q", right.minimum, node.key)
	}
	if left.height-right.height < -1 || left.height-right.height > 1 {
		return textFragmentNodeInvariants{}, fmt.Errorf("node %q has balance %d", node.key, left.height-right.height)
	}
	height := max(left.height, right.height) + 1
	bytes := left.bytes + len(node.text) + right.bytes
	parts := left.parts + 1 + right.parts
	if node.height != height || node.bytes != bytes || node.parts != parts {
		return textFragmentNodeInvariants{}, fmt.Errorf("node %q has inconsistent metadata", node.key)
	}
	minimum, maximum := node.key, node.key
	if node.left != nil {
		minimum = left.minimum
	}
	if node.right != nil {
		maximum = right.maximum
	}
	return textFragmentNodeInvariants{
		height: height, bytes: bytes, parts: parts, minimum: minimum, maximum: maximum,
	}, nil
}

type scriptedStringWriter struct {
	counts []int
	values []string
	index  int
}

func (w *scriptedStringWriter) Write([]byte) (int, error) {
	return 0, errors.New("WriteString was not used")
}

func (w *scriptedStringWriter) WriteString(value string) (int, error) {
	w.values = append(w.values, value)
	count := len(value)
	if w.index < len(w.counts) {
		count = w.counts[w.index]
	}
	w.index++
	return count, nil
}
