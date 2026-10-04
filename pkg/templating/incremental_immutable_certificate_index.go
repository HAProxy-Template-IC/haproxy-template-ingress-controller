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

package templating

import "reflect"

// immutableCertificateIndex replaces a scan of every registered view, which cost
// O(resources) per template mutation; candidates still re-authenticate per lookup.
type immutableCertificateIndex struct {
	views      []*incrementalImmutableCertificateView
	registered map[*IncrementalImmutableCertificate]struct{}
	identities map[immutableIdentity][]*incrementalImmutableCertificateView
	ranges     *immutableCertificateRangeNode
}

type immutableCertificateRangeNode struct {
	rangeValue immutableRange
	view       *incrementalImmutableCertificateView
	maxEnd     uintptr
	height     int
	left       *immutableCertificateRangeNode
	right      *immutableCertificateRangeNode
}

func newImmutableCertificateIndex(capacity int) *immutableCertificateIndex {
	return &immutableCertificateIndex{
		views:      make([]*incrementalImmutableCertificateView, 0, capacity),
		registered: make(map[*IncrementalImmutableCertificate]struct{}, capacity),
		identities: make(map[immutableIdentity][]*incrementalImmutableCertificateView, capacity),
	}
}

func (i *immutableCertificateIndex) len() int {
	if i == nil {
		return 0
	}
	return len(i.views)
}

func (i *immutableCertificateIndex) has(certificate *IncrementalImmutableCertificate) bool {
	if i == nil {
		return false
	}
	_, exists := i.registered[certificate]
	return exists
}

// add indexes the proof-owned copies: the view-owned slices can be redirected.
func (i *immutableCertificateIndex) add(view *incrementalImmutableCertificateView) {
	i.views = append(i.views, view)
	i.registered[view.certificate] = struct{}{}
	for _, identity := range view.proof.registeredIdentitySlots {
		if identity.kind != reflect.Invalid {
			i.identities[identity] = append(i.identities[identity], view)
		}
	}
	for _, rangeValue := range view.proof.registeredRanges {
		i.ranges = insertImmutableCertificateRange(i.ranges, rangeValue, view)
	}
}

func (i *immutableCertificateIndex) contains(target immutableTarget) bool {
	if i == nil || target.identity.kind == reflect.Invalid {
		return false
	}
	for _, view := range i.identities[target.identity] {
		if view.containsRegisteredTarget(target) {
			return true
		}
	}
	return i.ranges.stab(target.pointer, func(view *incrementalImmutableCertificateView) bool {
		return view.containsRegisteredTarget(target)
	})
}

// stab reports whether match accepts the view of any range holding pointer.
func (n *immutableCertificateRangeNode) stab(
	pointer uintptr,
	match func(*incrementalImmutableCertificateView) bool,
) bool {
	if n == nil || n.maxEnd <= pointer {
		return false
	}
	if n.left.stab(pointer, match) {
		return true
	}
	if pointer < n.rangeValue.start {
		return false
	}
	if pointer < n.rangeValue.end && match(n.view) {
		return true
	}
	return n.right.stab(pointer, match)
}

// Ranges are kept per view, never merged, so each hit names its own view.
func insertImmutableCertificateRange(
	node *immutableCertificateRangeNode,
	value immutableRange,
	view *incrementalImmutableCertificateView,
) *immutableCertificateRangeNode {
	if value.start >= value.end {
		return node
	}
	if node == nil {
		return &immutableCertificateRangeNode{rangeValue: value, view: view, maxEnd: value.end, height: 1}
	}
	if value.start < node.rangeValue.start {
		node.left = insertImmutableCertificateRange(node.left, value, view)
	} else {
		node.right = insertImmutableCertificateRange(node.right, value, view)
	}
	return balanceImmutableCertificateRange(node)
}

func balanceImmutableCertificateRange(node *immutableCertificateRangeNode) *immutableCertificateRangeNode {
	refreshImmutableCertificateRange(node)
	balance := node.left.heightOrZero() - node.right.heightOrZero()
	if balance > 1 {
		if node.left.left.heightOrZero() < node.left.right.heightOrZero() {
			node.left = rotateImmutableCertificateRangeLeft(node.left)
		}
		return rotateImmutableCertificateRangeRight(node)
	}
	if balance < -1 {
		if node.right.right.heightOrZero() < node.right.left.heightOrZero() {
			node.right = rotateImmutableCertificateRangeRight(node.right)
		}
		return rotateImmutableCertificateRangeLeft(node)
	}
	return node
}

func rotateImmutableCertificateRangeLeft(node *immutableCertificateRangeNode) *immutableCertificateRangeNode {
	root := node.right
	node.right = root.left
	root.left = node
	refreshImmutableCertificateRange(node)
	refreshImmutableCertificateRange(root)
	return root
}

func rotateImmutableCertificateRangeRight(node *immutableCertificateRangeNode) *immutableCertificateRangeNode {
	root := node.left
	node.left = root.right
	root.right = node
	refreshImmutableCertificateRange(node)
	refreshImmutableCertificateRange(root)
	return root
}

func refreshImmutableCertificateRange(node *immutableCertificateRangeNode) {
	node.height = max(node.left.heightOrZero(), node.right.heightOrZero()) + 1
	node.maxEnd = max(node.rangeValue.end, node.left.maxEndOrZero(), node.right.maxEndOrZero())
}

func (n *immutableCertificateRangeNode) heightOrZero() int {
	if n == nil {
		return 0
	}
	return n.height
}

func (n *immutableCertificateRangeNode) maxEndOrZero() uintptr {
	if n == nil {
		return 0
	}
	return n.maxEnd
}
