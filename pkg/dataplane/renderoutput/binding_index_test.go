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

package renderoutput

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/renderplan"
)

func TestOutputBindingTreePathCopiesInsertionsAndDeletions(t *testing.T) {
	const entries = 257
	tree := sealOutputBindingTree(nil)
	expected := make(map[string]*outputBinding, entries)
	paths := make([]string, entries)
	for index := range entries {
		paths[index] = fmt.Sprintf("general/%03d", index*73%entries)
	}
	for _, path := range paths {
		previous := tree
		binding := testOutputBinding(path)
		var err error
		tree, err = tree.put(path, binding)
		require.NoError(t, err)
		_, found, err := previous.lookup(path)
		require.NoError(t, err)
		assert.False(t, found)
		expected[path] = binding
		assertOutputBindingTree(t, tree, expected)
	}
	deletionOrder := make([]string, 0, entries)
	for parity := range 2 {
		for index := parity; index < len(paths); index += 2 {
			deletionOrder = append(deletionOrder, paths[index])
		}
	}
	for _, path := range deletionOrder {
		previous := tree
		var err error
		tree, err = tree.delete(path)
		require.NoError(t, err)
		binding, found, err := previous.lookup(path)
		require.NoError(t, err)
		require.True(t, found)
		assert.Same(t, expected[path], binding)
		delete(expected, path)
		assertOutputBindingTree(t, tree, expected)
	}
}

func TestOutputBindingTreeRejectsTamperedPath(t *testing.T) {
	tree := sealOutputBindingTree(nil)
	for index := range 32 {
		path := fmt.Sprintf("general/%02d", index)
		var err error
		tree, err = tree.put(path, testOutputBinding(path))
		require.NoError(t, err)
	}
	node := tree.root
	for node.left != nil {
		node = node.left
	}
	originalKey := node.key
	node.key = "tampered"
	_, _, err := tree.lookup(originalKey)
	require.ErrorIs(t, err, errInvalidSnapshot)
	node.key = originalKey
	require.NoError(t, tree.validate())

	copied := *node
	require.ErrorIs(t, copied.validateShallow(), errInvalidSnapshot)
	originalContent := node.binding.file.legacy.Content
	node.binding.file.legacy.Content = "tampered"
	_, _, err = tree.lookup(originalKey)
	require.ErrorIs(t, err, errInvalidSnapshot)
	node.binding.file.legacy.Content = originalContent
	require.NoError(t, tree.validate())
}

func testOutputBinding(path string) *outputBinding {
	file := exactPlanFile(path, renderplan.FileKindGeneral, true, path+"\n")
	return sealOutputBinding(sealLegacyOutputFileBinding(&file), nil)
}

func assertOutputBindingTree(
	t *testing.T,
	tree *outputBindingTree,
	expected map[string]*outputBinding,
) {
	t.Helper()
	require.NoError(t, tree.validate())
	seen := make(map[string]*outputBinding, len(expected))
	height, files, artifacts := assertOutputBindingNode(t, tree.root, "", "", seen)
	assert.Equal(t, height, outputBindingNodeHeight(tree.root))
	assert.Equal(t, files, tree.files)
	assert.Equal(t, artifacts, tree.artifacts)
	assert.Equal(t, expected, seen)
	for path, binding := range expected {
		actual, found, err := tree.lookup(path)
		require.NoError(t, err)
		require.True(t, found)
		assert.Same(t, binding, actual)
	}
}

func assertOutputBindingNode(
	t *testing.T,
	node *outputBindingNode,
	minimum, maximum string,
	seen map[string]*outputBinding,
) (height, files, artifacts int) {
	t.Helper()
	if node == nil {
		return 0, 0, 0
	}
	require.NoError(t, node.validateShallow())
	if minimum != "" {
		assert.Greater(t, node.key, minimum)
	}
	if maximum != "" {
		assert.Less(t, node.key, maximum)
	}
	leftHeight, leftFiles, leftArtifacts := assertOutputBindingNode(
		t, node.left, minimum, node.key, seen,
	)
	rightHeight, rightFiles, rightArtifacts := assertOutputBindingNode(
		t, node.right, node.key, maximum, seen,
	)
	assert.LessOrEqual(t, leftHeight-rightHeight, 1)
	assert.LessOrEqual(t, rightHeight-leftHeight, 1)
	seen[node.key] = node.binding
	return max(leftHeight, rightHeight) + 1,
		leftFiles + rightFiles + 1,
		leftArtifacts + rightArtifacts + artifactPresence(node.binding)
}
