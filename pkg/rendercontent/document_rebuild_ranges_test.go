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
	"fmt"
	"slices"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDocumentTreeRanges(t *testing.T) {
	children := rebuiltDocumentChildren(t, 32)
	previous := documentFromChildren(t, nil, children)
	for start := 0; start <= len(children); start++ {
		for end := start; end <= len(children); end++ {
			root, err := documentTreeRange(previous.state.root, start, end)
			require.NoError(t, err)
			assertRebuiltDocument(t, previous, sealDocument(root), children[start:end])
		}
	}
	for _, bounds := range [][2]int{{-1, 0}, {0, 33}, {2, 1}} {
		_, err := documentTreeRange(previous.state.root, bounds[0], bounds[1])
		require.ErrorIs(t, err, errDocumentLeafOutOfRange)
	}
}

func TestJoinDocumentTreesWithDifferentHeights(t *testing.T) {
	for _, leftCount := range []int{0, 1, 2, 3, 31, 128, 513} {
		for _, rightCount := range []int{0, 1, 2, 3, 31, 128, 513} {
			t.Run(fmt.Sprintf("%d/%d", leftCount, rightCount), func(t *testing.T) {
				leftChildren := rebuiltDocumentChildren(t, leftCount)
				rightChildren := rebuiltDocumentChildren(t, rightCount)
				left := documentFromChildren(t, nil, leftChildren)
				right := documentFromChildren(t, nil, rightChildren)
				root, err := joinDocumentTrees(left.state.root, right.state.root)
				require.NoError(t, err)
				assertRebuiltDocument(t, left, sealDocument(root), slices.Concat(leftChildren, rightChildren))
				assertDocumentChildren(t, left, leftChildren)
				assertDocumentChildren(t, right, rightChildren)
			})
		}
	}
}

func TestJoinDocumentTreesRejectsOverflow(t *testing.T) {
	maximum := int(^uint(0) >> 1)
	left := &documentNode{height: 1, bytes: maximum, leaves: 1}
	right := &documentNode{height: 1, bytes: 1, leaves: 1}
	_, err := joinDocumentTrees(left, right)
	require.ErrorIs(t, err, errOutputTooLarge)
	left.bytes, left.leaves = 1, maximum
	_, err = joinDocumentTrees(left, right)
	require.ErrorIs(t, err, errOutputTooLarge)
}

func TestDocumentRebuildRejectsPoisonedPrevious(t *testing.T) {
	children := rebuiltDocumentChildren(t, 3)
	previous := documentFromChildren(t, nil, children)
	poisoned := cloneDocumentHandleState(previous)
	poisoned.state.bytes++
	var builder DocumentBuilder
	require.NoError(t, builder.AppendDocument(children[0]))
	_, err := builder.Build(&poisoned)
	require.ErrorIs(t, err, errInvalidDocument)
}

func TestDocumentRebuildRetainsMixedSuffix(t *testing.T) {
	output, err := FromSorted([]Change{{Key: "output", Text: "output"}})
	require.NoError(t, err)
	fragment, err := TextFragmentFromSorted([]TextPart{{Key: "fragment", Text: "fragment"}})
	require.NoError(t, err)
	child := rebuiltDocumentChildren(t, 1)[0]
	build := func(previous *Document, prefix string) Document {
		return buildDocument(t, previous, func(builder *DocumentBuilder) {
			_, err := builder.WriteString(prefix)
			require.NoError(t, err)
			require.NoError(t, builder.AppendOutput(output))
			require.NoError(t, builder.AppendTextFragment(fragment))
			require.NoError(t, builder.AppendDocument(child))
			_, err = builder.WriteString("tail")
			require.NoError(t, err)
		})
	}
	previous := build(nil, "before-")
	next := build(&previous, "after-")
	assertDocumentString(t, previous, "before-outputfragmentchild-0\ntail")
	assertDocumentString(t, next, "after-outputfragmentchild-0\ntail")
	assertDocumentInvariants(t, next)
	assertBalancedDocumentTree(t, next.state.root)
	for index := 1; index < next.state.leaves; index++ {
		oldLeaf, err := documentLeafNodeAt(previous.state.root, index)
		require.NoError(t, err)
		newLeaf, err := documentLeafNodeAt(next.state.root, index)
		require.NoError(t, err)
		require.Same(t, oldLeaf, newLeaf)
	}
}
