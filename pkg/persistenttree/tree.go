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

// Package persistenttree provides an immutable ordered map with structural
// sharing: an update returns a new tree sharing every untouched subtree with
// the old one, so both remain valid and comparable. Delta hashing over shared
// subtrees lets a caller find what changed between two versions without
// walking either in full.
package persistenttree

import (
	"bytes"
	"errors"
	"hash/maphash"
	"slices"
	"strings"
)

const frozenLookupThreshold = 32

var frozenLookupSeed = maphash.MakeSeed()

// Entry is an immutable-key input to the bulk tree constructors.
type Entry[V any] struct {
	Key   string
	Value V
}

// NewEntry owns key before returning it as an immutable string.
func NewEntry[V any](key []byte, value V) Entry[V] {
	return Entry[V]{Key: string(key), Value: value}
}

// Tree is an immutable ordered map.
type Tree[V any] struct {
	root *Node[V]
}

// Node is an opaque immutable snapshot used for identity and reads.
type Node[V any] struct {
	base      []frozenEntry[V]
	lookup    frozenLookup
	delta     *deltaNode[V]
	deltaHash *deltaHashNode[V]
	deltaSize int
	size      int
}

type frozenEntry[V any] struct {
	key   string
	value V
}

type frozenLookup struct {
	seed  maphash.Seed
	slots []uint32
}

type deltaEntry[V any] struct {
	key     string
	value   V
	present bool
}

type deltaNode[V any] struct {
	entry       *deltaEntry[V]
	left, right *deltaNode[V]
	height      int
}

// Txn accumulates persistent updates without mutating its source tree.
type Txn[V any] struct {
	base      []frozenEntry[V]
	lookup    frozenLookup
	delta     *deltaNode[V]
	deltaHash *deltaHashNode[V]
	deltaSize int
	size      int
	original  *Tree[V]
	snapshot  *Node[V]
}

// New returns an empty tree.
func New[V any]() *Tree[V] {
	return &Tree[V]{}
}

// NewFromSorted builds a frozen tree from strictly ordered entries.
func NewFromSorted[V any](entries []Entry[V]) (*Tree[V], error) {
	keyBytes := 0
	for index := 1; index < len(entries); index++ {
		if strings.Compare(entries[index-1].Key, entries[index].Key) >= 0 {
			return nil, errors.New("persistent tree entries are not strictly ordered")
		}
	}
	if len(entries) == 0 {
		return New[V](), nil
	}
	for index := range entries {
		keyBytes += len(entries[index].Key)
	}
	var keys strings.Builder
	keys.Grow(keyBytes)
	for index := range entries {
		_, _ = keys.WriteString(entries[index].Key)
	}
	ownedKeys := keys.String()
	base := make([]frozenEntry[V], len(entries))
	keyOffset := 0
	for index := range entries {
		keyEnd := keyOffset + len(entries[index].Key)
		base[index] = frozenEntry[V]{key: ownedKeys[keyOffset:keyEnd], value: entries[index].Value}
		keyOffset = keyEnd
	}
	return &Tree[V]{root: &Node[V]{
		base: base, lookup: buildFrozenLookup(base), size: len(base),
	}}, nil
}

// NewFrom builds a frozen tree without retaining or reordering entries.
func NewFrom[V any](entries []Entry[V]) (*Tree[V], error) {
	owned := slices.Clone(entries)
	slices.SortFunc(owned, func(left, right Entry[V]) int {
		return strings.Compare(left.Key, right.Key)
	})
	return NewFromSorted(owned)
}

// Len returns the number of visible entries.
func (t *Tree[V]) Len() int {
	if t == nil || t.root == nil {
		return 0
	}
	return t.root.size
}

// Root returns the opaque immutable snapshot used for reads.
func (t *Tree[V]) Root() *Node[V] {
	if t == nil {
		return nil
	}
	return t.root
}

// Txn starts a persistent update.
func (t *Tree[V]) Txn() *Txn[V] {
	if t == nil || t.root == nil {
		return &Txn[V]{original: t}
	}
	return &Txn[V]{
		base: t.root.base, lookup: t.root.lookup, delta: t.root.delta, deltaHash: t.root.deltaHash,
		deltaSize: t.root.deltaSize, size: t.root.size, original: t,
	}
}

// Insert returns a tree containing key and leaves the source tree unchanged.
func (t *Tree[V]) Insert(key []byte, value V) (*Tree[V], V, bool) {
	txn := t.Txn()
	previous, replaced := txn.Insert(key, value)
	return txn.Commit(), previous, replaced
}

// Delete returns a tree without key and leaves the source tree unchanged.
func (t *Tree[V]) Delete(key []byte) (*Tree[V], V, bool) {
	txn := t.Txn()
	previous, removed := txn.Delete(key)
	if !removed && t != nil {
		return t, previous, false
	}
	return txn.Commit(), previous, removed
}

// Get returns the value stored for key in the transaction snapshot.
func (t *Txn[V]) Get(key []byte) (V, bool) {
	if t == nil {
		var zero V
		return zero, false
	}
	return get(t.base, t.lookup, t.deltaHash, key)
}

// Root returns the transaction's current opaque immutable snapshot.
func (t *Txn[V]) Root() *Node[V] {
	if t == nil {
		return nil
	}
	if t.matchesOriginal() {
		return t.original.root
	}
	if t.snapshot != nil {
		return t.snapshot
	}
	if t.size == 0 && len(t.base) == 0 && t.delta == nil {
		return nil
	}
	t.snapshot = &Node[V]{
		base: t.base, lookup: t.lookup, delta: t.delta, deltaHash: t.deltaHash,
		deltaSize: t.deltaSize, size: t.size,
	}
	return t.snapshot
}

// Insert adds or replaces key in the transaction.
func (t *Txn[V]) Insert(key []byte, value V) (V, bool) {
	hash := maphash.Bytes(frozenLookupSeed, key)
	indexed, indexedExists := getDeltaHash(t.deltaHash, key, hash)
	baseIndex, baseExists := 0, false
	if !indexedExists {
		baseIndex, baseExists = getBaseIndexHashed(t.base, t.lookup, key, hash)
	}
	var previous V
	replaced := false
	ownedKey := ""
	if indexedExists {
		ownedKey = indexed.key
		if indexed.present {
			previous = indexed.value
			replaced = true
		}
	} else if baseExists {
		ownedKey = t.base[baseIndex].key
		previous = t.base[baseIndex].value
		replaced = true
	} else {
		ownedKey = string(key)
	}
	entry := &deltaEntry[V]{key: ownedKey, value: value, present: true}
	var orderedReplaced, hashReplaced bool
	t.delta, orderedReplaced = insertDelta(t.delta, entry)
	t.deltaHash, _, hashReplaced = insertDeltaHash(t.deltaHash, hash, entry)
	if orderedReplaced != hashReplaced || hashReplaced != indexedExists {
		panic("persistent tree delta indexes diverged")
	}
	if !indexedExists {
		t.deltaSize++
	}
	if !replaced {
		t.size++
	}
	t.snapshot = nil
	return previous, replaced
}

// Delete removes key from the transaction.
func (t *Txn[V]) Delete(key []byte) (V, bool) {
	hash := maphash.Bytes(frozenLookupSeed, key)
	indexed, indexedExists := getDeltaHash(t.deltaHash, key, hash)
	baseIndex, inBase := getBaseIndexHashed(t.base, t.lookup, key, hash)
	var previous V
	if indexedExists {
		if !indexed.present {
			return previous, false
		}
		previous = indexed.value
	} else {
		if !inBase {
			return previous, false
		}
		previous = t.base[baseIndex].value
	}
	if inBase {
		ownedKey := t.base[baseIndex].key
		if indexedExists {
			ownedKey = indexed.key
		}
		entry := &deltaEntry[V]{key: ownedKey, present: false}
		var orderedReplaced, hashReplaced bool
		t.delta, orderedReplaced = insertDelta(t.delta, entry)
		t.deltaHash, _, hashReplaced = insertDeltaHash(t.deltaHash, hash, entry)
		if orderedReplaced != hashReplaced || hashReplaced != indexedExists {
			panic("persistent tree delta indexes diverged")
		}
		if !indexedExists {
			t.deltaSize++
		}
	} else {
		var orderedRemoved, hashRemoved bool
		t.delta, orderedRemoved = deleteDelta(t.delta, key)
		t.deltaHash, _, hashRemoved = deleteDeltaHash(t.deltaHash, key, hash)
		if !orderedRemoved || !hashRemoved {
			panic("persistent tree delta indexes diverged")
		}
		t.deltaSize--
	}
	t.size--
	t.snapshot = nil
	return previous, true
}

// Commit returns the transaction's immutable snapshot.
func (t *Txn[V]) Commit() *Tree[V] {
	if t == nil {
		return New[V]()
	}
	if t.matchesOriginal() {
		return t.original
	}
	return &Tree[V]{root: t.Root()}
}

func (t *Txn[V]) matchesOriginal() bool {
	if t == nil || t.original == nil {
		return false
	}
	root := t.original.root
	if root == nil {
		return t.size == 0 && t.delta == nil && t.deltaHash == nil
	}
	return t.size == root.size && t.deltaSize == root.deltaSize &&
		t.delta == root.delta && t.deltaHash == root.deltaHash
}

// Get returns the value stored for key.
func (n *Node[V]) Get(key []byte) (V, bool) {
	if n == nil {
		var zero V
		return zero, false
	}
	return get(n.base, n.lookup, n.deltaHash, key)
}

// Minimum returns the first visible key and value in byte order.
func (n *Node[V]) Minimum() (key string, value V, ok bool) {
	if n == nil || n.size == 0 {
		var zero V
		return "", zero, false
	}
	baseIndex := 0
	for baseIndex < len(n.base) {
		key := n.base[baseIndex].key
		hash := maphash.String(frozenLookupSeed, key)
		if _, shadowed := getDeltaHashString(n.deltaHash, key, hash); !shadowed {
			break
		}
		baseIndex++
	}
	deltaKey, deltaValue, hasDelta := minimumPresentDelta(n.delta)
	if baseIndex >= len(n.base) {
		return deltaKey, deltaValue, hasDelta
	}
	if !hasDelta || n.base[baseIndex].key < deltaKey {
		return n.base[baseIndex].key, n.base[baseIndex].value, true
	}
	return deltaKey, deltaValue, true
}

// Maximum returns the last visible key and value in byte order.
func (n *Node[V]) Maximum() (key string, value V, ok bool) {
	if n == nil || n.size == 0 {
		var zero V
		return "", zero, false
	}
	baseIndex := len(n.base) - 1
	for baseIndex >= 0 {
		key := n.base[baseIndex].key
		hash := maphash.String(frozenLookupSeed, key)
		if _, shadowed := getDeltaHashString(n.deltaHash, key, hash); !shadowed {
			break
		}
		baseIndex--
	}
	deltaKey, deltaValue, hasDelta := maximumPresentDelta(n.delta)
	if baseIndex < 0 {
		return deltaKey, deltaValue, hasDelta
	}
	if !hasDelta || n.base[baseIndex].key > deltaKey {
		return n.base[baseIndex].key, n.base[baseIndex].value, true
	}
	return deltaKey, deltaValue, true
}

// Walk visits visible entries in byte order until visit returns true.
func (n *Node[V]) Walk(visit func(string, V) bool) bool {
	if n == nil || n.size == 0 {
		return false
	}
	state := mergedWalk[V]{base: n.base, end: len(n.base), visit: visit}
	state.walkDelta(n.delta, nil, "", false)
	if !state.stopped {
		state.flushBaseBefore("", false)
	}
	return state.stopped
}

// WalkPrefix visits matching entries in byte order until visit returns true.
func (n *Node[V]) WalkPrefix(prefix []byte, visit func(string, V) bool) bool {
	if n == nil || n.size == 0 {
		return false
	}
	upper, bounded := prefixUpperBound(prefix)
	state := mergedWalk[V]{
		base:  n.base,
		next:  lowerBoundBytes(n.base, prefix),
		end:   len(n.base),
		visit: visit,
	}
	if bounded {
		state.end = lowerBoundString(n.base, upper)
	}
	state.walkDelta(n.delta, prefix, upper, bounded)
	if !state.stopped {
		state.flushBaseBefore("", false)
	}
	return state.stopped
}

type mergedWalk[V any] struct {
	base    []frozenEntry[V]
	next    int
	end     int
	visit   func(string, V) bool
	stopped bool
}

func (w *mergedWalk[V]) walkDelta(
	node *deltaNode[V],
	lower []byte,
	upper string,
	bounded bool,
) {
	if node == nil || w.stopped {
		return
	}
	if compareStringBytes(node.entry.key, lower) >= 0 {
		w.walkDelta(node.left, lower, upper, bounded)
	}
	if w.stopped {
		return
	}
	inRange := compareStringBytes(node.entry.key, lower) >= 0 &&
		(!bounded || node.entry.key < upper)
	if inRange {
		w.flushBaseBefore(node.entry.key, true)
		if !w.stopped && node.entry.present {
			w.stopped = w.visit(node.entry.key, node.entry.value)
		}
	}
	if !w.stopped && (!bounded || node.entry.key < upper) {
		w.walkDelta(node.right, lower, upper, bounded)
	}
}

func (w *mergedWalk[V]) flushBaseBefore(key string, bounded bool) {
	for w.next < w.end && (!bounded || w.base[w.next].key < key) {
		entry := w.base[w.next]
		w.next++
		w.stopped = w.visit(entry.key, entry.value)
		if w.stopped {
			return
		}
	}
	if bounded && w.next < w.end && w.base[w.next].key == key {
		w.next++
	}
}

func get[V any](
	base []frozenEntry[V],
	lookup frozenLookup,
	deltaHash *deltaHashNode[V],
	key []byte,
) (V, bool) {
	if deltaHash == nil {
		return getBase(base, lookup, key)
	}
	hash := maphash.Bytes(frozenLookupSeed, key)
	if entry, exists := getDeltaHash(deltaHash, key, hash); exists {
		return entry.value, entry.present
	}
	return getBaseHashed(base, lookup, key, hash)
}

func getBase[V any](base []frozenEntry[V], lookup frozenLookup, key []byte) (V, bool) {
	hash := uint64(0)
	if len(lookup.slots) != 0 {
		hash = maphash.Bytes(lookup.seed, key)
	}
	return getBaseHashed(base, lookup, key, hash)
}

func getBaseHashed[V any](
	base []frozenEntry[V],
	lookup frozenLookup,
	key []byte,
	hash uint64,
) (V, bool) {
	index, exists := getBaseIndexHashed(base, lookup, key, hash)
	if exists {
		return base[index].value, true
	}
	var zero V
	return zero, false
}

func getBaseIndexHashed[V any](
	base []frozenEntry[V],
	lookup frozenLookup,
	key []byte,
	hash uint64,
) (int, bool) {
	if len(lookup.slots) != 0 {
		return getFrozenLookupHashed(lookup, base, key, hash)
	}
	index := lowerBoundBytes(base, key)
	return index, index < len(base) && compareBytesString(key, base[index].key) == 0
}

func buildFrozenLookup[V any](base []frozenEntry[V]) frozenLookup {
	if len(base) < frozenLookupThreshold {
		return frozenLookup{}
	}
	capacity := 1
	for capacity < len(base)*4/3 {
		capacity *= 2
	}
	lookup := frozenLookup{seed: frozenLookupSeed, slots: make([]uint32, capacity)}
	mask := uint64(len(lookup.slots)) - 1
	for index := range base {
		slot := maphash.String(lookup.seed, base[index].key) & mask
		for lookup.slots[slot] != 0 {
			slot = (slot + 1) & mask
		}
		lookup.slots[slot] = uint32(index + 1)
	}
	return lookup
}

func getFrozenLookup[V any](l frozenLookup, base []frozenEntry[V], key []byte) (int, bool) {
	return getFrozenLookupHashed(l, base, key, maphash.Bytes(l.seed, key))
}

func getFrozenLookupHashed[V any](
	l frozenLookup,
	base []frozenEntry[V],
	key []byte,
	hash uint64,
) (int, bool) {
	mask := uint64(len(l.slots)) - 1
	slot := hash & mask
	for {
		stored := l.slots[slot]
		if stored == 0 {
			return 0, false
		}
		index := int(stored - 1)
		if compareBytesString(key, base[index].key) == 0 {
			return index, true
		}
		slot = (slot + 1) & mask
	}
}

func lowerBoundBytes[V any](base []frozenEntry[V], key []byte) int {
	left, right := 0, len(base)
	for left < right {
		middle := int(uint(left+right) >> 1)
		if compareStringBytes(base[middle].key, key) < 0 {
			left = middle + 1
		} else {
			right = middle
		}
	}
	return left
}

func lowerBoundString[V any](base []frozenEntry[V], key string) int {
	left, right := 0, len(base)
	for left < right {
		middle := int(uint(left+right) >> 1)
		if base[middle].key < key {
			left = middle + 1
		} else {
			right = middle
		}
	}
	return left
}

func insertDelta[V any](
	node *deltaNode[V],
	entry *deltaEntry[V],
) (*deltaNode[V], bool) {
	if node == nil {
		return &deltaNode[V]{entry: entry, height: 1}, false
	}
	updated := *node
	var replaced bool
	switch comparison := strings.Compare(entry.key, node.entry.key); {
	case comparison < 0:
		updated.left, replaced = insertDelta(node.left, entry)
	case comparison > 0:
		updated.right, replaced = insertDelta(node.right, entry)
	default:
		updated.entry = entry
		return &updated, true
	}
	return balanceDelta(&updated), replaced
}

func deleteDelta[V any](node *deltaNode[V], key []byte) (*deltaNode[V], bool) {
	if node == nil {
		return nil, false
	}
	updated := *node
	var removed bool
	switch comparison := compareBytesString(key, node.entry.key); {
	case comparison < 0:
		updated.left, removed = deleteDelta(node.left, key)
	case comparison > 0:
		updated.right, removed = deleteDelta(node.right, key)
	default:
		if node.left == nil {
			return node.right, true
		}
		if node.right == nil {
			return node.left, true
		}
		successor := node.right
		for successor.left != nil {
			successor = successor.left
		}
		updated.entry = successor.entry
		updated.right, _ = deleteDeltaString(node.right, successor.entry.key)
		removed = true
	}
	if !removed {
		return node, false
	}
	return balanceDelta(&updated), true
}

func deleteDeltaString[V any](node *deltaNode[V], key string) (*deltaNode[V], bool) {
	if node == nil {
		return nil, false
	}
	updated := *node
	var removed bool
	switch comparison := strings.Compare(key, node.entry.key); {
	case comparison < 0:
		updated.left, removed = deleteDeltaString(node.left, key)
	case comparison > 0:
		updated.right, removed = deleteDeltaString(node.right, key)
	default:
		if node.left == nil {
			return node.right, true
		}
		if node.right == nil {
			return node.left, true
		}
		successor := node.right
		for successor.left != nil {
			successor = successor.left
		}
		updated.entry = successor.entry
		updated.right, _ = deleteDeltaString(node.right, successor.entry.key)
		removed = true
	}
	if !removed {
		return node, false
	}
	return balanceDelta(&updated), true
}

func balanceDelta[V any](node *deltaNode[V]) *deltaNode[V] {
	node.height = max(deltaHeight(node.left), deltaHeight(node.right)) + 1
	delta := deltaHeight(node.left) - deltaHeight(node.right)
	if delta > 1 {
		if deltaHeight(node.left.left) < deltaHeight(node.left.right) {
			left := *node.left
			node.left = rotateDeltaLeft(&left)
		}
		return rotateDeltaRight(node)
	}
	if delta < -1 {
		if deltaHeight(node.right.right) < deltaHeight(node.right.left) {
			right := *node.right
			node.right = rotateDeltaRight(&right)
		}
		return rotateDeltaLeft(node)
	}
	return node
}

func rotateDeltaLeft[V any](node *deltaNode[V]) *deltaNode[V] {
	right := *node.right
	node.right = right.left
	node.height = max(deltaHeight(node.left), deltaHeight(node.right)) + 1
	right.left = node
	right.height = max(deltaHeight(right.left), deltaHeight(right.right)) + 1
	return &right
}

func rotateDeltaRight[V any](node *deltaNode[V]) *deltaNode[V] {
	left := *node.left
	node.left = left.right
	node.height = max(deltaHeight(node.left), deltaHeight(node.right)) + 1
	left.right = node
	left.height = max(deltaHeight(left.left), deltaHeight(left.right)) + 1
	return &left
}

func deltaHeight[V any](node *deltaNode[V]) int {
	if node == nil {
		return 0
	}
	return node.height
}

func maximumPresentDelta[V any](node *deltaNode[V]) (maxKey string, maxValue V, ok bool) {
	if node == nil {
		var zero V
		return "", zero, false
	}
	if key, value, exists := maximumPresentDelta(node.right); exists {
		return key, value, true
	}
	if node.entry.present {
		return node.entry.key, node.entry.value, true
	}
	return maximumPresentDelta(node.left)
}

func minimumPresentDelta[V any](node *deltaNode[V]) (minKey string, minValue V, ok bool) {
	if node == nil {
		var zero V
		return "", zero, false
	}
	if key, value, exists := minimumPresentDelta(node.left); exists {
		return key, value, true
	}
	if node.entry.present {
		return node.entry.key, node.entry.value, true
	}
	return minimumPresentDelta(node.right)
}

func prefixUpperBound(prefix []byte) (string, bool) {
	upper := bytes.Clone(prefix)
	for index := len(upper) - 1; index >= 0; index-- {
		if upper[index] != 0xff {
			upper[index]++
			return string(upper[:index+1]), true
		}
	}
	return "", false
}

func compareBytesString(left []byte, right string) int {
	limit := min(len(left), len(right))
	for index := range limit {
		if left[index] < right[index] {
			return -1
		}
		if left[index] > right[index] {
			return 1
		}
	}
	return len(left) - len(right)
}

func compareStringBytes(left string, right []byte) int {
	return -compareBytesString(right, left)
}
