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
	"io"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDocumentRetainsOrderedFragments(t *testing.T) {
	output, err := FromSorted([]Change{{Key: "a", Text: "alpha"}, {Key: "b", Text: "beta"}})
	require.NoError(t, err)
	fragment, err := TextFragmentFromSorted([]TextPart{{Key: "a", Text: ""}, {Key: "b", Text: "ranked"}})
	require.NoError(t, err)
	fragment, err = fragment.WithDelimiter("|")
	require.NoError(t, err)
	child := buildDocument(t, nil, func(builder *DocumentBuilder) {
		_, writeErr := builder.WriteString("child")
		require.NoError(t, writeErr)
	})

	var builder DocumentBuilder
	_, err = builder.WriteString("prefix-")
	require.NoError(t, err)
	require.NoError(t, builder.AppendOutput(output))
	_, err = builder.WriteString("-middle-")
	require.NoError(t, err)
	require.NoError(t, builder.AppendTextFragment(fragment))
	_, err = builder.WriteString("-")
	require.NoError(t, err)
	require.NoError(t, builder.AppendDocument(child))
	_, err = builder.WriteString("-suffix")
	require.NoError(t, err)
	document, err := builder.Build(nil)
	require.NoError(t, err)

	assertDocumentString(t, document, "prefix-alphabeta-middle-|ranked-child-suffix")
	leaves, err := document.Leaves()
	require.NoError(t, err)
	assert.Equal(t, 7, leaves)
	first, found, err := document.FirstByte()
	require.NoError(t, err)
	assert.True(t, found)
	assert.Equal(t, byte('p'), first)
	last, found, err := document.LastByte()
	require.NoError(t, err)
	assert.True(t, found)
	assert.Equal(t, byte('x'), last)
	assertDocumentInvariants(t, document)
}

func TestDocumentBuilderCopiesWritesAndSkipsEmptyChildren(t *testing.T) {
	value := []byte("safe")
	var builder DocumentBuilder
	written, err := builder.Write(value)
	require.NoError(t, err)
	assert.Equal(t, len(value), written)
	value[0] = 'X'
	require.NoError(t, builder.AppendOutput(Empty()))
	require.NoError(t, builder.AppendTextFragment(EmptyTextFragment()))
	presentEmpty, err := EmptyTextFragment().WithPart("part", "")
	require.NoError(t, err)
	require.NoError(t, builder.AppendTextFragment(presentEmpty))
	require.NoError(t, builder.AppendDocument(EmptyDocument()))
	_, err = builder.WriteString("-text")
	require.NoError(t, err)
	document, err := builder.Build(nil)
	require.NoError(t, err)
	assertDocumentString(t, document, "safe-text")
	leaves, err := document.Leaves()
	require.NoError(t, err)
	assert.Equal(t, 1, leaves)

	empty := EmptyDocument()
	assertDocumentString(t, empty, "")
	_, found, err := empty.FirstByte()
	require.NoError(t, err)
	assert.False(t, found)
	_, found, err = empty.LastByte()
	require.NoError(t, err)
	assert.False(t, found)
}

func TestDocumentReusesExactTextFragmentLeaves(t *testing.T) {
	base, err := TextFragmentFromSorted([]TextPart{{Key: "a", Text: "a"}, {Key: "b", Text: "b"}})
	require.NoError(t, err)
	first, err := base.WithDelimiter("|")
	require.NoError(t, err)
	second, err := base.WithDelimiter(string([]byte{'|'}))
	require.NoError(t, err)
	previous := buildDocument(t, nil, func(builder *DocumentBuilder) {
		require.NoError(t, builder.AppendTextFragment(first))
	})
	unchanged := buildDocument(t, &previous, func(builder *DocumentBuilder) {
		require.NoError(t, builder.AppendTextFragment(second))
	})
	assert.Same(t, previous.state, unchanged.state)

	changedFragment, err := base.WithPart("b", "B")
	require.NoError(t, err)
	changedFragment, err = changedFragment.WithDelimiter("|")
	require.NoError(t, err)
	changed := buildDocument(t, &previous, func(builder *DocumentBuilder) {
		require.NoError(t, builder.AppendTextFragment(changedFragment))
	})
	assert.NotSame(t, previous.state, changed.state)
	assertDocumentString(t, changed, "a|B")
}

func TestDocumentReusesOnlyExactLeaves(t *testing.T) {
	output, err := FromSorted([]Change{{Key: "part", Text: "output"}})
	require.NoError(t, err)
	child := buildDocument(t, nil, func(builder *DocumentBuilder) {
		_, writeErr := builder.WriteString("child")
		require.NoError(t, writeErr)
	})
	previous := buildMixedDocument(t, nil, "prefix", &output, &child)
	unchanged := buildMixedDocument(t, &previous, "prefix", &output, &child)
	assert.Same(t, previous.state, unchanged.state)
	same, err := previous.SameRoot(unchanged)
	require.NoError(t, err)
	assert.True(t, same)

	equivalentOutput, err := FromSorted([]Change{{Key: "part", Text: "output"}})
	require.NoError(t, err)
	equivalent := buildMixedDocument(t, &previous, "prefix", &equivalentOutput, &child)
	assert.NotSame(t, previous.state, equivalent.state)
	same, err = previous.SameRoot(equivalent)
	require.NoError(t, err)
	assert.False(t, same)
	assertDocumentString(t, equivalent, "prefixoutputchild")

	changed := buildMixedDocument(t, &previous, "changed", &output, &child)
	assert.NotSame(t, previous.state, changed.state)
	assertDocumentString(t, changed, "changedoutputchild")
}

func TestDocumentRejectsPoisonedValues(t *testing.T) {
	output, err := FromSorted([]Change{{Key: "part", Text: "safe"}})
	require.NoError(t, err)
	document := buildMixedDocument(t, nil, "prefix", &output, nil)

	copied := document
	require.NoError(t, copied.ValidateAuthentication())
	assert.Same(t, document.state, copied.state)
	poisonedRoot := cloneDocumentHandleState(document)
	poisonedRoot.state.root = newDocumentLeafNode(documentLeaf{
		kind: documentTextLeaf, text: "evil", bytes: 4, first: 'e', last: 'l',
	})
	require.ErrorIs(t, poisonedRoot.ValidateAuthentication(), errInvalidDocument)
	poisonedSize := cloneDocumentHandleState(document)
	poisonedSize.state.bytes++
	require.ErrorIs(t, poisonedSize.ValidateAuthentication(), errInvalidDocument)
	poisonedMemo := cloneDocumentHandleState(document)
	poisonedMemo.state.memo = &documentMemo{}
	require.ErrorIs(t, poisonedMemo.ValidateAuthentication(), errInvalidDocument)
	var zero Document
	require.ErrorIs(t, zero.ValidateAuthentication(), errInvalidDocument)

	poisonedOutput := cloneOutputHandleState(output)
	poisonedOutput.state.bytes++
	var outputBuilder DocumentBuilder
	require.ErrorIs(t, outputBuilder.AppendOutput(poisonedOutput), errInvalidOutput)
	_, err = outputBuilder.Build(nil)
	require.ErrorIs(t, err, errInvalidOutput)

	fragment, err := TextFragmentFromSorted([]TextPart{{Key: "a", Text: "safe"}})
	require.NoError(t, err)
	poisonedFragment := cloneTextFragmentHandleState(fragment)
	poisonedFragment.state.bytes++
	var fragmentBuilder DocumentBuilder
	require.ErrorIs(t, fragmentBuilder.AppendTextFragment(poisonedFragment), errInvalidTextFragment)
	_, err = fragmentBuilder.Build(nil)
	require.ErrorIs(t, err, errInvalidTextFragment)

	poisonedDocument := cloneDocumentHandleState(document)
	poisonedDocument.state.bytes++
	var documentBuilder DocumentBuilder
	require.ErrorIs(t, documentBuilder.AppendDocument(poisonedDocument), errInvalidDocument)
	_, err = documentBuilder.Build(nil)
	require.ErrorIs(t, err, errInvalidDocument)

	retainedOutput, err := FromSorted([]Change{{Key: "part", Text: "retained"}})
	require.NoError(t, err)
	retainingDocument := buildMixedDocument(t, nil, "", &retainedOutput, nil)
	retainedOutput = Output{}
	_, err = retainingDocument.WriteTo(io.Discard)
	require.NoError(t, err)

	retainedFragment, err := TextFragmentFromSorted([]TextPart{{Key: "part", Text: "retained"}})
	require.NoError(t, err)
	retainingFragmentDocument := buildDocument(t, nil, func(builder *DocumentBuilder) {
		require.NoError(t, builder.AppendTextFragment(retainedFragment))
	})
	retainedFragment = TextFragment{}
	_, err = retainingFragmentDocument.WriteTo(io.Discard)
	require.NoError(t, err)
}

func TestDocumentWriterContracts(t *testing.T) {
	document := buildDocument(t, nil, func(builder *DocumentBuilder) {
		_, err := builder.WriteString("alpha")
		require.NoError(t, err)
	})
	_, err := document.WriteTo(nil)
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
		{name: "negative", count: -1, wantErr: errInvalidWriteCount},
		{name: "oversize", count: 6, wantErr: errInvalidWriteCount},
		{name: "partial with error", count: 4, writeErr: sentinel, wantWritten: 4, wantErr: sentinel},
		{name: "full with error", count: 5, writeErr: sentinel, wantWritten: 5, wantErr: sentinel},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			written, writeErr := document.WriteTo(fixedStringWriter{count: test.count, err: test.writeErr})
			require.ErrorIs(t, writeErr, test.wantErr)
			assert.Equal(t, test.wantWritten, written)
		})
	}
	var complete strings.Builder
	written, err := document.WriteTo(&complete)
	require.NoError(t, err)
	assert.EqualValues(t, 5, written)
	assert.Equal(t, "alpha", complete.String())
}

func TestDocumentStringMemoizationIsConcurrent(t *testing.T) {
	document := buildDocument(t, nil, func(builder *DocumentBuilder) {
		_, err := builder.WriteString(strings.Repeat("a", 4096))
		require.NoError(t, err)
	})
	const workers = 64
	var wait sync.WaitGroup
	wait.Add(workers)
	for range workers {
		go func() {
			defer wait.Done()
			text, err := document.String()
			assert.NoError(t, err)
			assert.Len(t, text, 4096)
		}()
	}
	wait.Wait()
}

func TestDocumentRejectsInvalidTreeMetadata(t *testing.T) {
	maximum := int(^uint(0) >> 1)
	left := &documentNode{bytes: maximum, leaves: maximum, first: 'a', last: 'a'}
	right := &documentNode{bytes: 1, leaves: 1, first: 'b', last: 'b'}
	_, err := newDocumentBranch(left, right)
	require.ErrorIs(t, err, errOutputTooLarge)
	_, err = newDocumentBranch(nil, right)
	require.ErrorIs(t, err, errInvalidDocument)

	invalid := sealDocument(newDocumentLeafNode(documentLeaf{kind: 255, bytes: 1, first: 'a', last: 'a'}))
	_, err = invalid.WriteTo(io.Discard)
	require.ErrorIs(t, err, errInvalidDocument)
	invalid = sealDocument(newDocumentLeafNode(documentLeaf{
		kind: documentTextFragmentLeaf, retained: &outputState{}, bytes: 1, first: 'a', last: 'a',
	}))
	_, err = invalid.WriteTo(io.Discard)
	require.ErrorIs(t, err, errInvalidDocument)
}

func buildMixedDocument(t *testing.T, previous *Document, prefix string, output *Output, child *Document) Document {
	t.Helper()
	return buildDocument(t, previous, func(builder *DocumentBuilder) {
		_, err := builder.WriteString(prefix)
		require.NoError(t, err)
		if output != nil {
			require.NoError(t, builder.AppendOutput(*output))
		}
		if child != nil {
			require.NoError(t, builder.AppendDocument(*child))
		}
	})
}

func buildDocument(t *testing.T, previous *Document, build func(*DocumentBuilder)) Document {
	t.Helper()
	var builder DocumentBuilder
	build(&builder)
	document, err := builder.Build(previous)
	require.NoError(t, err)
	return document
}

func assertDocumentString(t *testing.T, document Document, want string) {
	t.Helper()
	got, err := document.String()
	require.NoError(t, err)
	assert.Equal(t, want, got)
	length, err := document.Bytes()
	require.NoError(t, err)
	assert.Equal(t, len(want), length)
}

func assertDocumentInvariants(t *testing.T, document Document) {
	t.Helper()
	require.NoError(t, document.ValidateAuthentication())
	got, err := validateDocumentNode(document.state.root)
	require.NoError(t, err)
	assert.Equal(t, documentNodeHeight(document.state.root), got.height)
	assert.Equal(t, document.state.bytes, got.bytes)
	assert.Equal(t, document.state.leaves, got.leaves)
	assert.Equal(t, document.state.root.first, got.first)
	assert.Equal(t, document.state.root.last, got.last)
}

func cloneDocumentHandleState(document Document) Document {
	state := *document.state
	state.seal = &state
	return Document{state: &state}
}

type documentNodeInvariants struct {
	height int
	bytes  int
	leaves int
	first  byte
	last   byte
}

func validateDocumentNode(node *documentNode) (documentNodeInvariants, error) {
	if node == nil {
		return documentNodeInvariants{}, nil
	}
	if node.left == nil && node.right == nil {
		if node.height != 1 || node.bytes != node.leaf.bytes || node.leaves != 1 ||
			node.first != node.leaf.first || node.last != node.leaf.last {
			return documentNodeInvariants{}, errors.New("document leaf has inconsistent metadata")
		}
		return documentNodeInvariants{
			height: 1, bytes: node.bytes, leaves: 1, first: node.first, last: node.last,
		}, nil
	}
	if node.left == nil || node.right == nil {
		return documentNodeInvariants{}, errors.New("document branch has a missing child")
	}
	left, err := validateDocumentNode(node.left)
	if err != nil {
		return documentNodeInvariants{}, err
	}
	right, err := validateDocumentNode(node.right)
	if err != nil {
		return documentNodeInvariants{}, err
	}
	result := documentNodeInvariants{
		height: max(left.height, right.height) + 1,
		bytes:  left.bytes + right.bytes,
		leaves: left.leaves + right.leaves,
		first:  left.first,
		last:   right.last,
	}
	if node.height != result.height || node.bytes != result.bytes || node.leaves != result.leaves ||
		node.first != result.first || node.last != result.last {
		return documentNodeInvariants{}, errors.New("document branch has inconsistent metadata")
	}
	return result, nil
}
