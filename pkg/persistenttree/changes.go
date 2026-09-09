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

package persistenttree

// Change describes one key whose presence or immutable value differs.
type Change[V any] struct {
	Key           string
	Before        V
	After         V
	BeforePresent bool
	AfterPresent  bool
}

// WalkChanges skips shared subtrees and visits differences in unspecified order.
// equal compares immutable values; returning true from visit stops the walk.
func (n *Node[V]) WalkChanges(previous *Node[V], equal func(V, V) bool, visit func(Change[V]) bool) bool {
	if n == previous {
		return false
	}
	emit := func(key string) bool {
		before, beforePresent := previous.Get([]byte(key))
		after, afterPresent := n.Get([]byte(key))
		if beforePresent == afterPresent && (!afterPresent || equal(before, after)) {
			return false
		}
		return visit(Change[V]{
			Key: key, Before: before, After: after,
			BeforePresent: beforePresent, AfterPresent: afterPresent,
		})
	}
	if sameFrozenBase(n, previous) {
		var currentDelta, previousDelta *deltaHashNode[V]
		if n != nil {
			currentDelta = n.deltaHash
		}
		if previous != nil {
			previousDelta = previous.deltaHash
		}
		return walkChangedHashSlots(
			deltaHashSlot[V]{child: currentDelta}, deltaHashSlot[V]{child: previousDelta}, 0, emit,
		)
	}
	if n.Walk(func(key string, _ V) bool { return emit(key) }) {
		return true
	}
	return previous.Walk(func(key string, _ V) bool {
		_, present := n.Get([]byte(key))
		return !present && emit(key)
	})
}

func sameFrozenBase[V any](left, right *Node[V]) bool {
	var leftBase, rightBase []frozenEntry[V]
	if left != nil {
		leftBase = left.base
	}
	if right != nil {
		rightBase = right.base
	}
	return len(leftBase) == len(rightBase) && (len(leftBase) == 0 || &leftBase[0] == &rightBase[0])
}

func walkChangedHashSlots[V any](left, right deltaHashSlot[V], shift uint, visit func(string) bool) bool {
	if left.child == right.child && left.leaf == right.leaf {
		return false
	}
	if left.child == nil && right.child == nil {
		return walkChangedHashLeaves(left.leaf, right.leaf, visit)
	}
	bitmap := hashSlotBitmap(left, shift) | hashSlotBitmap(right, shift)
	for bitmap != 0 {
		bit := bitmap & -bitmap
		if walkChangedHashSlots(hashSlotAt(left, bit, shift), hashSlotAt(right, bit, shift), shift+deltaHashBits, visit) {
			return true
		}
		bitmap &^= bit
	}
	return false
}

func hashSlotBitmap[V any](slot deltaHashSlot[V], shift uint) uint32 {
	if slot.child != nil {
		return slot.child.bitmap
	}
	if slot.leaf != nil {
		return deltaHashBit(slot.leaf.hash, shift)
	}
	return 0
}

func hashSlotAt[V any](slot deltaHashSlot[V], bit uint32, shift uint) deltaHashSlot[V] {
	if slot.child != nil && slot.child.bitmap&bit != 0 {
		return slot.child.slots[deltaHashSlotIndex(slot.child.bitmap, bit)]
	}
	if slot.leaf != nil && deltaHashBit(slot.leaf.hash, shift) == bit {
		return slot
	}
	return deltaHashSlot[V]{}
}

func walkChangedHashLeaves[V any](left, right *deltaHashLeaf[V], visit func(string) bool) bool {
	if visitHashLeaf(left, func(entry *deltaEntry[V]) bool {
		return entry != hashLeafEntry(right, entry.key) && visit(entry.key)
	}) {
		return true
	}
	return visitHashLeaf(right, func(entry *deltaEntry[V]) bool {
		return hashLeafEntry(left, entry.key) == nil && visit(entry.key)
	})
}

func hashLeafEntry[V any](leaf *deltaHashLeaf[V], key string) *deltaEntry[V] {
	if leaf == nil {
		return nil
	}
	if leaf.entry.key == key {
		return leaf.entry
	}
	for _, entry := range leaf.collisions {
		if entry.key == key {
			return entry
		}
	}
	return nil
}

func visitHashLeaf[V any](leaf *deltaHashLeaf[V], visit func(*deltaEntry[V]) bool) bool {
	if leaf == nil {
		return false
	}
	if visit(leaf.entry) {
		return true
	}
	for _, entry := range leaf.collisions {
		if visit(entry) {
			return true
		}
	}
	return false
}
