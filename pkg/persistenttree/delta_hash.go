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

package persistenttree

import (
	"math/bits"
	"slices"
)

const deltaHashBits = 5

type deltaHashNode[V any] struct {
	bitmap uint32
	slots  []deltaHashSlot[V]
}

type deltaHashSlot[V any] struct {
	child *deltaHashNode[V]
	leaf  *deltaHashLeaf[V]
}

type deltaHashLeaf[V any] struct {
	hash       uint64
	entry      *deltaEntry[V]
	collisions []*deltaEntry[V]
}

func getDeltaHash[V any](node *deltaHashNode[V], key []byte, hash uint64) (*deltaEntry[V], bool) {
	for shift := uint(0); node != nil; shift += deltaHashBits {
		bit := deltaHashBit(hash, shift)
		if node.bitmap&bit == 0 {
			return nil, false
		}
		slot := node.slots[deltaHashSlotIndex(node.bitmap, bit)]
		if slot.child != nil {
			node = slot.child
			continue
		}
		if slot.leaf.hash != hash {
			return nil, false
		}
		if compareBytesString(key, slot.leaf.entry.key) == 0 {
			return slot.leaf.entry, true
		}
		for _, entry := range slot.leaf.collisions {
			if compareBytesString(key, entry.key) == 0 {
				return entry, true
			}
		}
		return nil, false
	}
	return nil, false
}

func getDeltaHashString[V any](node *deltaHashNode[V], key string, hash uint64) (*deltaEntry[V], bool) {
	for shift := uint(0); node != nil; shift += deltaHashBits {
		bit := deltaHashBit(hash, shift)
		if node.bitmap&bit == 0 {
			return nil, false
		}
		slot := node.slots[deltaHashSlotIndex(node.bitmap, bit)]
		if slot.child != nil {
			node = slot.child
			continue
		}
		if slot.leaf.hash != hash {
			return nil, false
		}
		if slot.leaf.entry.key == key {
			return slot.leaf.entry, true
		}
		for _, entry := range slot.leaf.collisions {
			if entry.key == key {
				return entry, true
			}
		}
		return nil, false
	}
	return nil, false
}

func insertDeltaHash[V any](
	node *deltaHashNode[V],
	hash uint64,
	entry *deltaEntry[V],
) (*deltaHashNode[V], *deltaEntry[V], bool) {
	return insertDeltaHashAt(node, hash, entry, 0)
}

func insertDeltaHashAt[V any](
	node *deltaHashNode[V],
	hash uint64,
	entry *deltaEntry[V],
	shift uint,
) (*deltaHashNode[V], *deltaEntry[V], bool) {
	bit := deltaHashBit(hash, shift)
	if node == nil {
		return &deltaHashNode[V]{
			bitmap: bit,
			slots:  []deltaHashSlot[V]{{leaf: newDeltaHashLeaf(hash, entry)}},
		}, nil, false
	}
	index := deltaHashSlotIndex(node.bitmap, bit)
	if node.bitmap&bit == 0 {
		slots := make([]deltaHashSlot[V], len(node.slots)+1)
		copy(slots, node.slots[:index])
		slots[index] = deltaHashSlot[V]{leaf: newDeltaHashLeaf(hash, entry)}
		copy(slots[index+1:], node.slots[index:])
		return &deltaHashNode[V]{bitmap: node.bitmap | bit, slots: slots}, nil, false
	}

	slots := slices.Clone(node.slots)
	slot := node.slots[index]
	if slot.child != nil {
		var previous *deltaEntry[V]
		var replaced bool
		slots[index].child, previous, replaced = insertDeltaHashAt(
			slot.child,
			hash,
			entry,
			shift+deltaHashBits,
		)
		return &deltaHashNode[V]{bitmap: node.bitmap, slots: slots}, previous, replaced
	}
	if slot.leaf.hash == hash {
		var previous *deltaEntry[V]
		var replaced bool
		slots[index].leaf, previous, replaced = insertDeltaHashLeaf(slot.leaf, entry)
		return &deltaHashNode[V]{bitmap: node.bitmap, slots: slots}, previous, replaced
	}

	slots[index] = deltaHashSlot[V]{child: mergeDeltaHashLeaves(
		slot.leaf,
		newDeltaHashLeaf(hash, entry),
		shift+deltaHashBits,
	)}
	return &deltaHashNode[V]{bitmap: node.bitmap, slots: slots}, nil, false
}

func insertDeltaHashLeaf[V any](
	leaf *deltaHashLeaf[V],
	entry *deltaEntry[V],
) (*deltaHashLeaf[V], *deltaEntry[V], bool) {
	if leaf.entry.key == entry.key {
		return &deltaHashLeaf[V]{
			hash: leaf.hash, entry: entry, collisions: leaf.collisions,
		}, leaf.entry, true
	}
	for index, collision := range leaf.collisions {
		if collision.key != entry.key {
			continue
		}
		collisions := slices.Clone(leaf.collisions)
		collisions[index] = entry
		return &deltaHashLeaf[V]{
			hash: leaf.hash, entry: leaf.entry, collisions: collisions,
		}, collision, true
	}
	collisions := make([]*deltaEntry[V], len(leaf.collisions)+1)
	copy(collisions, leaf.collisions)
	collisions[len(leaf.collisions)] = entry
	return &deltaHashLeaf[V]{
		hash: leaf.hash, entry: leaf.entry, collisions: collisions,
	}, nil, false
}

func mergeDeltaHashLeaves[V any](
	left *deltaHashLeaf[V],
	right *deltaHashLeaf[V],
	shift uint,
) *deltaHashNode[V] {
	leftBit := deltaHashBit(left.hash, shift)
	rightBit := deltaHashBit(right.hash, shift)
	if leftBit == rightBit {
		return &deltaHashNode[V]{
			bitmap: leftBit,
			slots: []deltaHashSlot[V]{{
				child: mergeDeltaHashLeaves(left, right, shift+deltaHashBits),
			}},
		}
	}
	if leftBit < rightBit {
		return &deltaHashNode[V]{
			bitmap: leftBit | rightBit,
			slots:  []deltaHashSlot[V]{{leaf: left}, {leaf: right}},
		}
	}
	return &deltaHashNode[V]{
		bitmap: leftBit | rightBit,
		slots:  []deltaHashSlot[V]{{leaf: right}, {leaf: left}},
	}
}

func deleteDeltaHash[V any](
	node *deltaHashNode[V],
	key []byte,
	hash uint64,
) (*deltaHashNode[V], *deltaEntry[V], bool) {
	return deleteDeltaHashAt(node, key, hash, 0)
}

func deleteDeltaHashAt[V any](
	node *deltaHashNode[V],
	key []byte,
	hash uint64,
	shift uint,
) (*deltaHashNode[V], *deltaEntry[V], bool) {
	if node == nil {
		return nil, nil, false
	}
	bit := deltaHashBit(hash, shift)
	if node.bitmap&bit == 0 {
		return node, nil, false
	}
	index := deltaHashSlotIndex(node.bitmap, bit)
	slot := node.slots[index]
	if slot.child != nil {
		child, previous, removed := deleteDeltaHashAt(
			slot.child,
			key,
			hash,
			shift+deltaHashBits,
		)
		if !removed {
			return node, nil, false
		}
		if child == nil {
			return removeDeltaHashSlot(node, bit, index), previous, true
		}
		slots := slices.Clone(node.slots)
		if leaf, single := singleDeltaHashLeaf(child); single {
			slots[index] = deltaHashSlot[V]{leaf: leaf}
		} else {
			slots[index] = deltaHashSlot[V]{child: child}
		}
		return &deltaHashNode[V]{bitmap: node.bitmap, slots: slots}, previous, true
	}
	if slot.leaf.hash != hash {
		return node, nil, false
	}
	leaf, previous, removed := deleteDeltaHashLeaf(slot.leaf, key)
	if !removed {
		return node, nil, false
	}
	if leaf == nil {
		return removeDeltaHashSlot(node, bit, index), previous, true
	}
	slots := slices.Clone(node.slots)
	slots[index].leaf = leaf
	return &deltaHashNode[V]{bitmap: node.bitmap, slots: slots}, previous, true
}

func deleteDeltaHashLeaf[V any](
	leaf *deltaHashLeaf[V],
	key []byte,
) (*deltaHashLeaf[V], *deltaEntry[V], bool) {
	if compareBytesString(key, leaf.entry.key) == 0 {
		if len(leaf.collisions) == 0 {
			return nil, leaf.entry, true
		}
		return &deltaHashLeaf[V]{
			hash:       leaf.hash,
			entry:      leaf.collisions[0],
			collisions: slices.Clone(leaf.collisions[1:]),
		}, leaf.entry, true
	}
	for index, entry := range leaf.collisions {
		if compareBytesString(key, entry.key) != 0 {
			continue
		}
		collisions := make([]*deltaEntry[V], len(leaf.collisions)-1)
		copy(collisions, leaf.collisions[:index])
		copy(collisions[index:], leaf.collisions[index+1:])
		return &deltaHashLeaf[V]{
			hash: leaf.hash, entry: leaf.entry, collisions: collisions,
		}, entry, true
	}
	return leaf, nil, false
}

func removeDeltaHashSlot[V any](
	node *deltaHashNode[V],
	bit uint32,
	index int,
) *deltaHashNode[V] {
	if len(node.slots) == 1 {
		return nil
	}
	slots := make([]deltaHashSlot[V], len(node.slots)-1)
	copy(slots, node.slots[:index])
	copy(slots[index:], node.slots[index+1:])
	return &deltaHashNode[V]{bitmap: node.bitmap &^ bit, slots: slots}
}

func singleDeltaHashLeaf[V any](node *deltaHashNode[V]) (*deltaHashLeaf[V], bool) {
	if node == nil || len(node.slots) != 1 {
		return nil, false
	}
	slot := node.slots[0]
	if slot.leaf != nil {
		return slot.leaf, true
	}
	return singleDeltaHashLeaf(slot.child)
}

func newDeltaHashLeaf[V any](hash uint64, entry *deltaEntry[V]) *deltaHashLeaf[V] {
	return &deltaHashLeaf[V]{hash: hash, entry: entry}
}

func deltaHashBit(hash uint64, shift uint) uint32 {
	return uint32(1) << ((hash >> shift) & (1<<deltaHashBits - 1))
}

func deltaHashSlotIndex(bitmap, bit uint32) int {
	return bits.OnesCount32(bitmap & (bit - 1))
}
