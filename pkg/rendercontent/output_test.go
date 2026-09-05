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

func TestOutputPathCopiesExactChanges(t *testing.T) {
	output, err := FromSorted([]Change{
		{Key: "a", Text: "a\n"},
		{Key: "b", Text: "b\n"},
		{Key: "c", Text: "c\n"},
	})
	require.NoError(t, err)
	unchanged, err := output.WithText("b", "b\n")
	require.NoError(t, err)
	assert.Same(t, output.state, unchanged.state)

	changed, err := output.WithText("b", "B\n")
	require.NoError(t, err)
	assert.NotSame(t, output.state, changed.state)
	same, err := output.SameRoot(changed)
	require.NoError(t, err)
	assert.False(t, same)
	assertOutputString(t, output, "a\nb\nc\n")
	assertOutputString(t, changed, "a\nB\nc\n")

	removed, err := changed.Delete("b")
	require.NoError(t, err)
	assertOutputString(t, removed, "a\nc\n")
	absent, err := removed.Delete("b")
	require.NoError(t, err)
	assert.Same(t, removed.state, absent.state)
}

func TestOutputSharesUnchangedSubtrees(t *testing.T) {
	output, err := FromSorted([]Change{
		{Key: "a", Text: "a"}, {Key: "b", Text: "b"}, {Key: "c", Text: "c"},
		{Key: "d", Text: "d"}, {Key: "e", Text: "e"}, {Key: "f", Text: "f"},
		{Key: "g", Text: "g"},
	})
	require.NoError(t, err)
	changed, err := output.WithText("a", "A")
	require.NoError(t, err)
	assert.Same(t, output.state.root.right, changed.state.root.right)
	assert.Same(t, output.state.root.left.right, changed.state.root.left.right)
	assert.NotSame(t, output.state.root.left.left, changed.state.root.left.left)
	assertOutputInvariants(t, output)
	assertOutputInvariants(t, changed)

	removed, err := changed.Delete("a")
	require.NoError(t, err)
	assert.Same(t, changed.state.root.right, removed.state.root.right)
	assertOutputInvariants(t, removed)
}

func TestOutputApplyIsAtomicAndCanonical(t *testing.T) {
	output, err := FromSorted([]Change{{Key: "a", Text: "a"}, {Key: "c", Text: "c"}})
	require.NoError(t, err)
	changed, err := output.Apply([]Change{
		{Key: "d", Text: "d"},
		{Key: "a", Text: ""},
		{Key: "b", Text: "b"},
	})
	require.NoError(t, err)
	assertOutputString(t, output, "ac")
	assertOutputString(t, changed, "bcd")

	_, err = output.Apply([]Change{{Key: "same", Text: "a"}, {Key: "same", Text: "b"}})
	require.ErrorIs(t, err, errDuplicatePartChange)
	assertOutputString(t, output, "ac")

	_, err = output.Apply([]Change{{Key: "b", Text: "b"}, {Key: "", Text: "invalid"}})
	require.ErrorIs(t, err, errEmptyPartKey)
	assertOutputString(t, output, "ac")

	unchanged, err := output.Apply([]Change{{Key: "c", Text: "c"}, {Key: "a", Text: "a"}})
	require.NoError(t, err)
	assert.Same(t, output.state, unchanged.state)
}

func TestOutputRandomUpdatesMatchOrderedMap(t *testing.T) {
	output := Empty()
	want := map[string]string{}
	for operation := range 10_000 {
		key := fmt.Sprintf("key-%04d", rand.IntN(500))
		text := ""
		if rand.IntN(4) != 0 {
			text = fmt.Sprintf("value-%d\n", operation)
			want[key] = text
		} else {
			delete(want, key)
		}
		var err error
		output, err = output.WithText(key, text)
		require.NoError(t, err)
		require.NoError(t, output.ValidateAuthentication())
		assertOutputInvariants(t, output)
		if operation%97 == 0 {
			assertOutputMatchesMap(t, output, want)
		}
	}
	assertOutputMatchesMap(t, output, want)
	assertOutputInvariants(t, output)
}

func TestOutputRejectsSealAndRootPoison(t *testing.T) {
	output, err := FromSorted([]Change{{Key: "a", Text: "safe"}})
	require.NoError(t, err)
	poisonedRoot := cloneOutputHandleState(output)
	poisonedRoot.state.root = mustOutputLeaf(t, "evil")
	require.ErrorIs(t, poisonedRoot.ValidateAuthentication(), errInvalidOutput)

	poisonedSize := cloneOutputHandleState(output)
	poisonedSize.state.bytes++
	require.ErrorIs(t, poisonedSize.ValidateAuthentication(), errInvalidOutput)

	poisonedSeal := cloneOutputHandleState(output)
	poisonedSeal.state.auth.root = mustOutputLeaf(t, "safe")
	require.ErrorIs(t, poisonedSeal.ValidateAuthentication(), errInvalidOutput)

	poisonedMemo := cloneOutputHandleState(output)
	poisonedMemo.state.memo = &outputMemo{}
	require.ErrorIs(t, poisonedMemo.ValidateAuthentication(), errInvalidOutput)

	copied := output
	require.NoError(t, copied.ValidateAuthentication())
	assert.Same(t, output.state, copied.state)

	equivalent, err := FromSorted([]Change{{Key: "a", Text: "safe"}})
	require.NoError(t, err)
	same, err := output.SameRoot(equivalent)
	require.NoError(t, err)
	assert.False(t, same)

	var zero Output
	require.ErrorIs(t, zero.ValidateAuthentication(), errInvalidOutput)
	require.ErrorIs(t, new(Output).ValidateAuthentication(), errInvalidOutput)
}

func TestOutputRejectsAggregateOverflow(t *testing.T) {
	left := &outputNode{bytes: int(^uint(0) >> 1), parts: int(^uint(0) >> 1)}
	_, err := makeNode("key", "x", left, nil)
	require.ErrorIs(t, err, errOutputTooLarge)

	left = &outputNode{bytes: 1, parts: int(^uint(0) >> 1)}
	_, err = makeNode("key", "", left, nil)
	require.ErrorIs(t, err, errOutputTooLarge)
}

func TestOutputRejectsInvalidConstruction(t *testing.T) {
	_, err := FromSorted([]Change{{Key: "", Text: "value"}})
	require.ErrorIs(t, err, errEmptyPartKey)
	_, err = FromSorted([]Change{{Key: "b", Text: "b"}, {Key: "a", Text: "a"}})
	require.ErrorIs(t, err, errUnsortedParts)
	_, err = FromSorted([]Change{{Key: "a", Text: "a"}, {Key: "a", Text: "b"}})
	require.ErrorIs(t, err, errDuplicatePartChange)
}

func TestOutputGetAndWriterContracts(t *testing.T) {
	output, err := FromSorted([]Change{{Key: "a", Text: "alpha"}, {Key: "b", Text: "beta"}})
	require.NoError(t, err)
	text, found, err := output.Get("b")
	require.NoError(t, err)
	assert.True(t, found)
	assert.Equal(t, "beta", text)
	_, found, err = output.Get("c")
	require.NoError(t, err)
	assert.False(t, found)
	_, _, err = output.Get("")
	require.ErrorIs(t, err, errEmptyPartKey)

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
		{name: "negative", count: -1, wantErr: errInvalidWriteCount},
		{name: "oversize", count: 6, wantErr: errInvalidWriteCount},
		{name: "negative with error", count: -1, writeErr: sentinel, wantErr: errInvalidWriteCount},
		{name: "oversize with error", count: 6, writeErr: sentinel, wantErr: errInvalidWriteCount},
		{name: "partial with error", count: 4, writeErr: sentinel, wantWritten: 4, wantErr: sentinel},
		{name: "full with error", count: 5, writeErr: sentinel, wantWritten: 5, wantErr: sentinel},
	}
	for _, test := range tests {
		t.Run(test.name+" Write", func(t *testing.T) {
			written, writeErr := output.WriteTo(fixedWriter{count: test.count, err: test.writeErr})
			require.ErrorIs(t, writeErr, test.wantErr)
			assert.Equal(t, test.wantWritten, written)
		})
		t.Run(test.name+" WriteString", func(t *testing.T) {
			written, writeErr := output.WriteTo(fixedStringWriter{count: test.count, err: test.writeErr})
			require.ErrorIs(t, writeErr, test.wantErr)
			assert.Equal(t, test.wantWritten, written)
		})
	}
	var complete strings.Builder
	written, err := output.WriteTo(&complete)
	require.NoError(t, err)
	assert.EqualValues(t, 9, written)
	assert.Equal(t, "alphabeta", complete.String())
}

type fixedWriter struct {
	count int
	err   error
}

func (w fixedWriter) Write([]byte) (int, error) {
	return w.count, w.err
}

type fixedStringWriter struct {
	count int
	err   error
}

func (w fixedStringWriter) Write([]byte) (int, error) {
	return 0, errors.New("WriteString was not used")
}

func (w fixedStringWriter) WriteString(string) (int, error) {
	return w.count, w.err
}

func TestOutputStringMemoizationIsConcurrent(t *testing.T) {
	output, err := FromSorted([]Change{{Key: "a", Text: strings.Repeat("a", 4096)}})
	require.NoError(t, err)
	const workers = 64
	var wait sync.WaitGroup
	wait.Add(workers)
	for range workers {
		go func() {
			defer wait.Done()
			text, stringErr := output.String()
			assert.NoError(t, stringErr)
			assert.Len(t, text, 4096)
		}()
	}
	wait.Wait()
}

func mustOutputLeaf(t *testing.T, text string) *outputNode {
	t.Helper()
	node, err := makeNode("part", text, nil, nil)
	require.NoError(t, err)
	return node
}

func assertOutputMatchesMap(t *testing.T, output Output, values map[string]string) {
	t.Helper()
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	var want strings.Builder
	for _, key := range keys {
		want.WriteString(values[key])
	}
	assertOutputString(t, output, want.String())
	parts, err := output.Parts()
	require.NoError(t, err)
	assert.Equal(t, len(values), parts)
}

func assertOutputString(t *testing.T, output Output, want string) {
	t.Helper()
	got, err := output.String()
	require.NoError(t, err)
	assert.Equal(t, want, got)
	bytes, err := output.Bytes()
	require.NoError(t, err)
	assert.Equal(t, len(want), bytes)
}

func assertOutputInvariants(t *testing.T, output Output) {
	t.Helper()
	require.NoError(t, output.ValidateAuthentication())
	got, err := validateOutputNodeInvariants(output.state.root)
	require.NoError(t, err)
	assert.Equal(t, nodeHeight(output.state.root), got.height)
	assert.Equal(t, output.state.bytes, got.bytes)
	assert.Equal(t, output.state.parts, got.parts)
}

func cloneOutputHandleState(output Output) Output {
	state := *output.state
	state.seal = &state
	return Output{state: &state}
}

type outputNodeInvariants struct {
	height  int
	bytes   int
	parts   int
	minimum string
	maximum string
}

func validateOutputNodeInvariants(node *outputNode) (outputNodeInvariants, error) {
	if node == nil {
		return outputNodeInvariants{}, nil
	}
	left, err := validateOutputNodeInvariants(node.left)
	if err != nil {
		return outputNodeInvariants{}, err
	}
	right, err := validateOutputNodeInvariants(node.right)
	if err != nil {
		return outputNodeInvariants{}, err
	}
	if node.key == "" || node.text == "" {
		return outputNodeInvariants{}, errors.New("node has an empty key or text")
	}
	if node.left != nil && left.maximum >= node.key {
		return outputNodeInvariants{}, fmt.Errorf("left key %q is not before %q", left.maximum, node.key)
	}
	if node.right != nil && right.minimum <= node.key {
		return outputNodeInvariants{}, fmt.Errorf("right key %q is not after %q", right.minimum, node.key)
	}
	if left.height-right.height < -1 || left.height-right.height > 1 {
		return outputNodeInvariants{}, fmt.Errorf("node %q has balance %d", node.key, left.height-right.height)
	}
	height := max(left.height, right.height) + 1
	bytes := left.bytes + len(node.text) + right.bytes
	parts := left.parts + 1 + right.parts
	if node.height != height || node.bytes != bytes || node.parts != parts {
		return outputNodeInvariants{}, fmt.Errorf("node %q has inconsistent metadata", node.key)
	}
	minimum, maximum := node.key, node.key
	if node.left != nil {
		minimum = left.minimum
	}
	if node.right != nil {
		maximum = right.maximum
	}
	return outputNodeInvariants{
		height: height, bytes: bytes, parts: parts, minimum: minimum, maximum: maximum,
	}, nil
}
