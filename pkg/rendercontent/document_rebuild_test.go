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
	"math/bits"
	"math/rand/v2"
	"slices"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDocumentBuilderRetainsMembershipSubtrees(t *testing.T) {
	base := rebuiltDocumentChildren(t, 128)
	added := rebuiltDocumentChildren(t, 1)[0]
	for _, scenario := range []struct {
		name string
		next []Document
	}{
		{name: "append", next: slices.Concat(base, []Document{added})},
		{name: "prepend", next: slices.Concat([]Document{added}, base)},
		{name: "insert-middle", next: slices.Insert(slices.Clone(base), 63, added)},
		{name: "delete-first", next: base[1:]},
		{name: "delete-middle", next: slices.Delete(slices.Clone(base), 63, 64)},
		{name: "delete-last", next: base[:len(base)-1]},
		{name: "replace-middle", next: slices.Replace(slices.Clone(base), 63, 64, added)},
		{name: "retain-one", next: base[:1]},
		{name: "delete-all", next: nil},
		{name: "unchanged", next: base},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			previous := documentFromChildren(t, nil, base)
			next := documentFromChildren(t, &previous, scenario.next)
			assertRebuiltDocument(t, previous, next, scenario.next)
			assertDocumentChildren(t, previous, base)
			originalLeaves := make(map[*documentState]*documentNode, len(base))
			for index, child := range base {
				node, err := documentLeafNodeAt(previous.state.root, index)
				require.NoError(t, err)
				originalLeaves[child.state] = node
			}
			for index, child := range scenario.next {
				if original, exists := originalLeaves[child.state]; exists {
					retained, err := documentLeafNodeAt(next.state.root, index)
					require.NoError(t, err)
					require.Same(t, original, retained, "unchanged child %d", index)
				}
			}
		})
	}
}

func TestDocumentBuilderMembershipUsesExactChildIdentity(t *testing.T) {
	base := rebuiltDocumentChildren(t, 3)
	next := slices.Clone(base)
	next[1] = rebuiltDocumentChildren(t, 2)[1]
	previous := documentFromChildren(t, nil, base)
	rebuilt := documentFromChildren(t, &previous, next)
	assertRebuiltDocument(t, previous, rebuilt, next)
	for _, index := range []int{0, 2} {
		oldLeaf, err := documentLeafNodeAt(previous.state.root, index)
		require.NoError(t, err)
		newLeaf, err := documentLeafNodeAt(rebuilt.state.root, index)
		require.NoError(t, err)
		require.Same(t, oldLeaf, newLeaf)
	}
	oldLeaf, err := documentLeafNodeAt(previous.state.root, 1)
	require.NoError(t, err)
	newLeaf, err := documentLeafNodeAt(rebuilt.state.root, 1)
	require.NoError(t, err)
	require.NotSame(t, oldLeaf, newLeaf)
	require.Same(t, next[1].state, newLeaf.leaf.retained)
}

func TestDocumentBuilderMembershipRandomChanges(t *testing.T) {
	for seed := range uint64(8) {
		t.Run(fmt.Sprintf("seed=%d", seed), func(t *testing.T) {
			random := rand.New(rand.NewPCG(seed, seed+1))
			children := rebuiltDocumentChildren(t, 128)
			previous := documentFromChildren(t, nil, children)
			for iteration := range 256 {
				before := slices.Clone(children)
				children = mutateDocumentChildren(t, random, children)
				next := documentFromChildren(t, &previous, children)
				assertRebuiltDocument(t, previous, next, children)
				assertDocumentChildren(t, previous, before)
				previous = next
				require.LessOrEqual(t, documentNodeHeight(next.state.root), 2*bits.Len(uint(len(children)+1)), "iteration %d", iteration)
			}
		})
	}
}

func mutateDocumentChildren(t *testing.T, random *rand.Rand, children []Document) []Document {
	t.Helper()
	switch random.IntN(4) {
	case 0:
		added := rebuiltDocumentChildren(t, random.IntN(8)+1)
		return slices.Insert(children, random.IntN(len(children)+1), added...)
	case 1:
		if len(children) > 0 {
			start := random.IntN(len(children))
			return slices.Delete(children, start, start+random.IntN(len(children)-start)+1)
		}
	case 2:
		if len(children) > 0 {
			children[random.IntN(len(children))] = rebuiltDocumentChildren(t, 1)[0]
		}
	case 3:
		random.Shuffle(len(children), func(i, j int) {
			children[i], children[j] = children[j], children[i]
		})
	}
	return children
}

func rebuiltDocumentChildren(t *testing.T, count int) []Document {
	t.Helper()
	children := make([]Document, count)
	for index := range children {
		children[index] = buildDocument(t, nil, func(builder *DocumentBuilder) {
			_, err := fmt.Fprintf(builder, "child-%d\n", index)
			require.NoError(t, err)
		})
	}
	return children
}

func documentFromChildren(t *testing.T, previous *Document, children []Document) Document {
	t.Helper()
	return buildDocument(t, previous, func(builder *DocumentBuilder) {
		for _, child := range children {
			require.NoError(t, builder.AppendDocument(child))
		}
	})
}

func assertRebuiltDocument(t *testing.T, previous, next Document, children []Document) {
	t.Helper()
	assertDocumentChildren(t, next, children)
	assertBalancedDocumentTree(t, next.state.root)
	require.NoError(t, previous.ValidateAuthentication())
	if len(children) > 0 {
		assertDocumentInvariants(t, next)
	}
}

func assertDocumentChildren(t *testing.T, document Document, children []Document) {
	t.Helper()
	cold := documentFromChildren(t, nil, children)
	want, err := cold.String()
	require.NoError(t, err)
	assertDocumentString(t, document, want)
	require.Equal(t, cold.state.leaves, document.state.leaves)
	for index := range children {
		wantNode, err := documentLeafNodeAt(cold.state.root, index)
		require.NoError(t, err)
		gotNode, err := documentLeafNodeAt(document.state.root, index)
		require.NoError(t, err)
		require.Equal(t, wantNode.leaf, gotNode.leaf)
		require.Same(t, children[index].state, gotNode.leaf.retained)
	}
}

func assertBalancedDocumentTree(t *testing.T, node *documentNode) {
	t.Helper()
	if node == nil {
		return
	}
	left, right := documentNodeHeight(node.left), documentNodeHeight(node.right)
	require.LessOrEqual(t, max(left, right)-min(left, right), 1)
	assertBalancedDocumentTree(t, node.left)
	assertBalancedDocumentTree(t, node.right)
}
