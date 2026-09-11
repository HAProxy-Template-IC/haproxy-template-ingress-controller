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

func rebuildDocumentTree(previous *documentNode, leaves []documentLeaf) (*documentNode, error) {
	prefix := 0
	if _, err := sameDocumentLeaves(previous, leaves, &prefix); err != nil {
		return nil, err
	}
	if prefix == len(leaves) && prefix == documentNodeLeaves(previous) {
		return previous, nil
	}
	suffix := 0
	limit := min(documentNodeLeaves(previous)-prefix, len(leaves)-prefix)
	if _, err := sameDocumentSuffix(previous, leaves, limit, &suffix); err != nil {
		return nil, err
	}
	left, err := documentTreeRange(previous, 0, prefix)
	if err != nil {
		return nil, err
	}
	if middle := leaves[prefix : len(leaves)-suffix]; len(middle) > 0 {
		rebuilt, err := buildDocumentTree(middle)
		if err != nil {
			return nil, err
		}
		left, err = joinDocumentTrees(left, rebuilt)
		if err != nil {
			return nil, err
		}
	}
	right, err := documentTreeRange(previous, documentNodeLeaves(previous)-suffix, documentNodeLeaves(previous))
	if err != nil {
		return nil, err
	}
	return joinDocumentTrees(left, right)
}

func sameDocumentSuffix(node *documentNode, leaves []documentLeaf, limit int, matched *int) (bool, error) {
	if node == nil || *matched == limit {
		return false, nil
	}
	if node.left != nil || node.right != nil {
		same, err := sameDocumentSuffix(node.right, leaves, limit, matched)
		if err != nil || !same {
			return same, err
		}
		return sameDocumentSuffix(node.left, leaves, limit, matched)
	}
	same, err := sameDocumentLeaf(node.leaf, leaves[len(leaves)-1-*matched])
	if err != nil || !same {
		return same, err
	}
	*matched++
	return true, nil
}

func documentTreeRange(node *documentNode, start, end int) (*documentNode, error) {
	if start < 0 || end < start || end > documentNodeLeaves(node) {
		return nil, errDocumentLeafOutOfRange
	}
	if start == end {
		return emptyDocument.state.root, nil
	}
	if start == 0 && end == documentNodeLeaves(node) {
		return node, nil
	}
	middle := documentNodeLeaves(node.left)
	if end <= middle {
		return documentTreeRange(node.left, start, end)
	}
	if start >= middle {
		return documentTreeRange(node.right, start-middle, end-middle)
	}
	left, err := documentTreeRange(node.left, start, middle)
	if err != nil {
		return nil, err
	}
	right, err := documentTreeRange(node.right, 0, end-middle)
	if err != nil {
		return nil, err
	}
	return joinDocumentTrees(left, right)
}

func joinDocumentTrees(left, right *documentNode) (*documentNode, error) {
	if left == nil {
		return right, nil
	}
	if right == nil {
		return left, nil
	}
	switch {
	case left.height > right.height+1:
		joined, err := joinDocumentTrees(left.right, right)
		if err != nil {
			return nil, err
		}
		return balanceDocumentNodes(left.left, joined)
	case right.height > left.height+1:
		joined, err := joinDocumentTrees(left, right.left)
		if err != nil {
			return nil, err
		}
		return balanceDocumentNodes(joined, right.right)
	default:
		return newDocumentBranch(left, right)
	}
}
