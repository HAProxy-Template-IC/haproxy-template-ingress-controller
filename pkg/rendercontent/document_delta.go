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
	"slices"
	"sync"
)

var (
	errInvalidDocumentLeafHandle  = errors.New("render document leaf handle is invalid")
	errInvalidDocumentGapHandle   = errors.New("render document gap handle is invalid")
	errInvalidDocumentDelta       = errors.New("render document delta is invalid")
	errInvalidDocumentTransaction = errors.New("render document transaction is invalid")
	errDocumentTransactionSealed  = errors.New("render document transaction is sealed")
	errDocumentEditConflict       = errors.New("render document transaction repeats an edit position")
	errDocumentLeafOutOfRange     = errors.New("render document leaf index is out of range")
	errDocumentGapOutOfRange      = errors.New("render document gap index is out of range")
	errEmptyDocumentLeaf          = errors.New("render document leaf is empty")
)

// DocumentLeafHandle authenticates one exact top-level leaf and its position.
type DocumentLeafHandle struct {
	base  *documentState
	node  *documentNode
	index int
	seal  *DocumentLeafHandle
}

// DocumentGapHandle authenticates one exact gap between top-level leaves.
type DocumentGapHandle struct {
	base        *documentState
	index       int
	predecessor *documentNode
	successor   *documentNode
	seal        *DocumentGapHandle
}

type documentChangeAuthentication struct {
	owner  *sealedDocumentChange
	kind   documentEditKind
	index  int
	before *documentNode
	after  *documentNode
}

type sealedDocumentChange struct {
	kind   documentEditKind
	index  int
	before *documentNode
	after  *documentNode
	seal   *sealedDocumentChange
	auth   documentChangeAuthentication
}

// DocumentLeafChange is one exact top-level leaf transition. A zero Document is absent.
type DocumentLeafChange struct {
	Index  int
	Before Document
	After  Document
}

type documentDeltaAuthentication struct {
	owner      *DocumentDelta
	base       *documentState
	next       *documentState
	changes    []*sealedDocumentChange
	structural bool
}

// DocumentDelta is an authenticated transition between exact document roots.
type DocumentDelta struct {
	base       *documentState
	next       *documentState
	changes    []*sealedDocumentChange
	structural bool
	seal       *DocumentDelta
	auth       documentDeltaAuthentication
}

type documentEditKind uint8

const (
	documentReplaceEdit documentEditKind = iota + 1
	documentDeleteEdit
	documentInsertEdit
)

type documentEdit struct {
	kind  documentEditKind
	index int
	leaf  documentLeaf
}

// DocumentTransaction atomically path-copies edits from one exact base root.
type DocumentTransaction struct {
	mu     sync.Mutex
	base   Document
	edits  map[int]documentEdit
	built  Document
	delta  *DocumentDelta
	err    error
	seal   *DocumentTransaction
	sealed bool
}

// LeafHandle returns a proof for the top-level leaf at index.
func (d Document) LeafHandle(index int) (*DocumentLeafHandle, error) {
	if err := d.ValidateAuthentication(); err != nil {
		return nil, err
	}
	node, err := documentLeafNodeAt(d.state.root, index)
	if err != nil {
		return nil, err
	}
	handle := &DocumentLeafHandle{base: d.state, node: node, index: index}
	handle.seal = handle
	return handle, nil
}

// GapHandle returns a proof for the top-level insertion gap at index.
func (d Document) GapHandle(index int) (*DocumentGapHandle, error) {
	if err := d.ValidateAuthentication(); err != nil {
		return nil, err
	}
	if index < 0 || index > d.state.leaves {
		return nil, errDocumentGapOutOfRange
	}
	var predecessor, successor *documentNode
	var err error
	if index > 0 {
		predecessor, err = documentLeafNodeAt(d.state.root, index-1)
		if err != nil {
			return nil, err
		}
	}
	if index < d.state.leaves {
		successor, err = documentLeafNodeAt(d.state.root, index)
		if err != nil {
			return nil, err
		}
	}
	handle := &DocumentGapHandle{
		base: d.state, index: index, predecessor: predecessor, successor: successor,
	}
	handle.seal = handle
	return handle, nil
}

// BeginTransaction starts an atomic edit against this exact document root.
func (d Document) BeginTransaction() (*DocumentTransaction, error) {
	if err := d.ValidateAuthentication(); err != nil {
		return nil, err
	}
	transaction := &DocumentTransaction{base: d, edits: make(map[int]documentEdit)}
	transaction.seal = transaction
	return transaction, nil
}

// Apply returns the authenticated next root only for this delta's exact base.
func (d *DocumentDelta) Apply(base Document) (Document, error) {
	if err := d.ValidateAuthentication(); err != nil {
		return Document{}, err
	}
	if err := base.ValidateAuthentication(); err != nil {
		return Document{}, err
	}
	if base.state != d.base {
		return Document{}, errInvalidDocumentDelta
	}
	return Document{state: d.next}, nil
}

// ValidateAuthentication verifies the exact base and next roots.
func (d *DocumentDelta) ValidateAuthentication() error {
	if d == nil || d.seal != d || d.auth.owner != d || d.base == nil ||
		d.auth.base != d.base || d.next == nil || d.auth.next != d.next ||
		d.auth.structural != d.structural || !sameDocumentChangePointers(d.auth.changes, d.changes) {
		return errInvalidDocumentDelta
	}
	if err := (Document{state: d.base}).ValidateAuthentication(); err != nil {
		return errors.Join(errInvalidDocumentDelta, err)
	}
	if err := (Document{state: d.next}).ValidateAuthentication(); err != nil {
		return errors.Join(errInvalidDocumentDelta, err)
	}
	return validateDocumentDeltaChanges(d)
}

// SameRoot reports whether the delta leaves the exact document unchanged.
func (d *DocumentDelta) SameRoot() (bool, error) {
	if err := d.ValidateAuthentication(); err != nil {
		return false, err
	}
	return d.base == d.next, nil
}

// RequiresFullValidation reports insertions and deletions whose consumers need a full rebuild.
func (d *DocumentDelta) RequiresFullValidation() (bool, error) {
	if err := d.ValidateAuthentication(); err != nil {
		return false, err
	}
	return d.structural, nil
}

// Changes returns authenticated leaf documents for only the changed positions.
func (d *DocumentDelta) Changes() ([]DocumentLeafChange, error) {
	if err := d.ValidateAuthentication(); err != nil {
		return nil, err
	}
	changes := make([]DocumentLeafChange, len(d.changes))
	for index, change := range d.changes {
		changes[index].Index = change.index
		if change.before != nil {
			changes[index].Before = sealDocument(change.before)
		}
		if change.after != nil {
			changes[index].After = sealDocument(change.after)
		}
	}
	return changes, nil
}

// ReplaceText replaces the leaf proven by expected with literal text.
func (t *DocumentTransaction) ReplaceText(expected *DocumentLeafHandle, text string) error {
	leaf, err := literalDocumentLeaf(text)
	if err != nil {
		return t.fail(err)
	}
	return t.replace(expected, leaf)
}

// ReplaceDocument replaces the leaf proven by expected with a child document.
func (t *DocumentTransaction) ReplaceDocument(
	expected *DocumentLeafHandle,
	document Document,
) error {
	leaf, err := childDocumentLeaf(document)
	if err != nil {
		return t.fail(err)
	}
	return t.replace(expected, leaf)
}

// Delete removes the exact leaf proven by expected.
func (t *DocumentTransaction) Delete(expected *DocumentLeafHandle) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if err := t.validateOpen(); err != nil {
		return t.recordError(err)
	}
	if err := validateDocumentLeafHandle(expected, t.base.state); err != nil {
		return t.recordError(err)
	}
	return t.addEdit(documentEdit{kind: documentDeleteEdit, index: expected.index})
}

// InsertText inserts literal text at the exact gap proven by expected.
func (t *DocumentTransaction) InsertText(expected *DocumentGapHandle, text string) error {
	leaf, err := literalDocumentLeaf(text)
	if err != nil {
		return t.fail(err)
	}
	return t.insert(expected, leaf)
}

// InsertDocument inserts a child document at the exact gap proven by expected.
func (t *DocumentTransaction) InsertDocument(
	expected *DocumentGapHandle,
	document Document,
) error {
	leaf, err := childDocumentLeaf(document)
	if err != nil {
		return t.fail(err)
	}
	return t.insert(expected, leaf)
}

// Commit seals the next root and its exact base-to-next proof.
func (t *DocumentTransaction) Commit() (Document, *DocumentDelta, error) {
	if t == nil {
		return Document{}, nil, errInvalidDocumentTransaction
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.seal != t {
		return Document{}, nil, errInvalidDocumentTransaction
	}
	if t.sealed {
		if t.err != nil {
			return Document{}, nil, t.err
		}
		if err := t.delta.ValidateAuthentication(); err != nil {
			return Document{}, nil, err
		}
		return t.built, t.delta, nil
	}
	t.sealed = true
	if t.err != nil {
		return Document{}, nil, t.err
	}
	root, changes, structural, err := t.applyEditsLocked()
	if err != nil {
		return Document{}, nil, err
	}
	t.built = t.base
	if root != t.base.state.root {
		t.built = sealDocument(root)
	}
	t.delta = sealDocumentDelta(t.base.state, t.built.state, changes, structural)
	return t.built, t.delta, nil
}

func (t *DocumentTransaction) applyEditsLocked() (
	root *documentNode,
	changes []*sealedDocumentChange,
	structural bool,
	err error,
) {
	root = t.base.state.root
	offset := 0
	changes = make([]*sealedDocumentChange, 0, len(t.edits))
	indices := make([]int, 0, len(t.edits))
	for index := range t.edits {
		indices = append(indices, index)
	}
	slices.Sort(indices)
	for _, index := range indices {
		edit := t.edits[index]
		currentIndex := index + offset
		var err error
		var before *documentNode
		if edit.kind != documentInsertEdit {
			before, err = documentLeafNodeAt(t.base.state.root, index)
			if err != nil {
				t.err = err
				return nil, nil, false, err
			}
		}
		changed := true
		switch edit.kind {
		case documentReplaceEdit:
			root, changed, err = replaceDocumentLeafNode(root, currentIndex, edit.leaf)
		case documentDeleteEdit:
			root, _, err = deleteDocumentLeafNode(root, currentIndex)
			offset--
			structural = true
		case documentInsertEdit:
			root, err = insertDocumentLeafNode(root, currentIndex, edit.leaf)
			offset++
			structural = true
		default:
			err = errInvalidDocumentTransaction
		}
		if err != nil {
			t.err = err
			return nil, nil, false, err
		}
		if !changed {
			continue
		}
		var after *documentNode
		if edit.kind != documentDeleteEdit {
			after, err = documentLeafNodeAt(root, currentIndex)
			if err != nil {
				t.err = err
				return nil, nil, false, err
			}
		}
		changes = append(changes, sealDocumentChange(edit.kind, index, before, after))
	}
	return root, changes, structural, nil
}

func (t *DocumentTransaction) replace(expected *DocumentLeafHandle, leaf documentLeaf) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if err := t.validateOpen(); err != nil {
		return t.recordError(err)
	}
	if err := validateDocumentLeafHandle(expected, t.base.state); err != nil {
		return t.recordError(err)
	}
	return t.addEdit(documentEdit{kind: documentReplaceEdit, index: expected.index, leaf: leaf})
}

func (t *DocumentTransaction) insert(expected *DocumentGapHandle, leaf documentLeaf) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if err := t.validateOpen(); err != nil {
		return t.recordError(err)
	}
	if err := validateDocumentGapHandle(expected, t.base.state); err != nil {
		return t.recordError(err)
	}
	return t.addEdit(documentEdit{kind: documentInsertEdit, index: expected.index, leaf: leaf})
}

func (t *DocumentTransaction) addEdit(edit documentEdit) error {
	if _, exists := t.edits[edit.index]; exists {
		return t.recordError(errDocumentEditConflict)
	}
	t.edits[edit.index] = edit
	return nil
}

func (t *DocumentTransaction) fail(err error) error {
	if t == nil {
		return errInvalidDocumentTransaction
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.recordError(err)
}

func (t *DocumentTransaction) recordError(err error) error {
	if t.err == nil {
		t.err = err
	}
	return err
}

func (t *DocumentTransaction) validateOpen() error {
	if t == nil || t.seal != t {
		return errInvalidDocumentTransaction
	}
	if t.sealed {
		return errDocumentTransactionSealed
	}
	if t.err != nil {
		return t.err
	}
	return t.base.ValidateAuthentication()
}

func validateDocumentLeafHandle(handle *DocumentLeafHandle, base *documentState) error {
	if handle == nil || handle.seal != handle || handle.base != base || handle.node == nil {
		return errInvalidDocumentLeafHandle
	}
	node, err := documentLeafNodeAt(base.root, handle.index)
	if err != nil || node != handle.node {
		return errInvalidDocumentLeafHandle
	}
	return nil
}

func validateDocumentGapHandle(handle *DocumentGapHandle, base *documentState) error {
	if handle == nil || handle.seal != handle || handle.base != base ||
		handle.index < 0 || handle.index > base.leaves {
		return errInvalidDocumentGapHandle
	}
	if handle.index == 0 {
		if handle.predecessor != nil {
			return errInvalidDocumentGapHandle
		}
	} else {
		predecessor, err := documentLeafNodeAt(base.root, handle.index-1)
		if err != nil || predecessor != handle.predecessor {
			return errInvalidDocumentGapHandle
		}
	}
	if handle.index == base.leaves {
		if handle.successor != nil {
			return errInvalidDocumentGapHandle
		}
	} else {
		successor, err := documentLeafNodeAt(base.root, handle.index)
		if err != nil || successor != handle.successor {
			return errInvalidDocumentGapHandle
		}
	}
	return nil
}

func sealDocumentChange(
	kind documentEditKind,
	index int,
	before, after *documentNode,
) *sealedDocumentChange {
	change := &sealedDocumentChange{kind: kind, index: index, before: before, after: after}
	change.seal = change
	change.auth = documentChangeAuthentication{
		owner: change, kind: kind, index: index, before: before, after: after,
	}
	return change
}

func sealDocumentDelta(
	base, next *documentState,
	changes []*sealedDocumentChange,
	structural bool,
) *DocumentDelta {
	delta := &DocumentDelta{
		base: base, next: next, changes: slices.Clone(changes), structural: structural,
	}
	delta.seal = delta
	delta.auth = documentDeltaAuthentication{
		owner: delta, base: base, next: next,
		changes: slices.Clone(delta.changes), structural: structural,
	}
	return delta
}

func validateDocumentDeltaChanges(delta *DocumentDelta) error {
	offset := 0
	structural := false
	previousIndex := -1
	for _, change := range delta.changes {
		if err := validateDocumentChangeAuthentication(change, previousIndex); err != nil {
			return errInvalidDocumentDelta
		}
		previousIndex = change.index
		if err := validateDocumentChangeBefore(delta.base, change); err != nil {
			return err
		}
		var err error
		offset, structural, err = validateDocumentChangeAfter(delta.next, change, offset, structural)
		if err != nil {
			return err
		}
	}
	if delta.next.leaves != delta.base.leaves+offset || delta.structural != structural {
		return errInvalidDocumentDelta
	}
	if len(delta.changes) == 0 && delta.base != delta.next {
		return errInvalidDocumentDelta
	}
	return nil
}

func validateDocumentChangeAuthentication(change *sealedDocumentChange, previousIndex int) error {
	if change == nil || change.seal != change || change.index <= previousIndex {
		return errInvalidDocumentDelta
	}
	expected := documentChangeAuthentication{
		owner: change, kind: change.kind, index: change.index,
		before: change.before, after: change.after,
	}
	if change.auth != expected {
		return errInvalidDocumentDelta
	}
	return nil
}

func validateDocumentChangeBefore(base *documentState, change *sealedDocumentChange) error {
	if change.before == nil {
		return nil
	}
	before, err := documentLeafNodeAt(base.root, change.index)
	if err != nil || before != change.before {
		return errInvalidDocumentDelta
	}
	return nil
}

func validateDocumentChangeAfter(
	next *documentState,
	change *sealedDocumentChange,
	offset int,
	structural bool,
) (nextOffset int, nextStructural bool, err error) {
	nextIndex := change.index + offset
	switch change.kind {
	case documentReplaceEdit:
		if change.before == nil || change.after == nil {
			return 0, false, errInvalidDocumentDelta
		}
	case documentDeleteEdit:
		if change.before == nil || change.after != nil {
			return 0, false, errInvalidDocumentDelta
		}
		return offset - 1, true, nil
	case documentInsertEdit:
		if change.before != nil || change.after == nil {
			return 0, false, errInvalidDocumentDelta
		}
		offset++
		structural = true
	default:
		return 0, false, errInvalidDocumentDelta
	}
	after, err := documentLeafNodeAt(next.root, nextIndex)
	if err != nil || after != change.after {
		return 0, false, errInvalidDocumentDelta
	}
	return offset, structural, nil
}

func sameDocumentChangePointers(left, right []*sealedDocumentChange) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func literalDocumentLeaf(text string) (documentLeaf, error) {
	if text == "" {
		return documentLeaf{}, errEmptyDocumentLeaf
	}
	return documentLeaf{
		kind: documentTextLeaf, text: text, bytes: len(text), first: text[0], last: text[len(text)-1],
	}, nil
}

func childDocumentLeaf(document Document) (documentLeaf, error) {
	if err := document.ValidateAuthentication(); err != nil {
		return documentLeaf{}, err
	}
	if document.state.bytes == 0 {
		return documentLeaf{}, errEmptyDocumentLeaf
	}
	return documentLeaf{
		kind: documentChildLeaf, retained: document.state, bytes: document.state.bytes,
		first: document.state.root.first, last: document.state.root.last,
	}, nil
}

func documentLeafNodeAt(root *documentNode, index int) (*documentNode, error) {
	if index < 0 || index >= documentNodeLeaves(root) {
		return nil, errDocumentLeafOutOfRange
	}
	node := root
	for node.left != nil || node.right != nil {
		leftLeaves := documentNodeLeaves(node.left)
		if index < leftLeaves {
			node = node.left
			continue
		}
		index -= leftLeaves
		node = node.right
	}
	return node, nil
}

func replaceDocumentLeafNode(
	root *documentNode,
	index int,
	leaf documentLeaf,
) (*documentNode, bool, error) {
	if index < 0 || index >= documentNodeLeaves(root) {
		return nil, false, errDocumentLeafOutOfRange
	}
	if root.left == nil && root.right == nil {
		same, err := sameDocumentLeaf(root.leaf, leaf)
		if err != nil || same {
			return root, false, err
		}
		return newDocumentLeafNode(leaf), true, nil
	}
	leftLeaves := documentNodeLeaves(root.left)
	left, right := root.left, root.right
	var changed bool
	var err error
	if index < leftLeaves {
		left, changed, err = replaceDocumentLeafNode(left, index, leaf)
	} else {
		right, changed, err = replaceDocumentLeafNode(right, index-leftLeaves, leaf)
	}
	if err != nil || !changed {
		return root, changed, err
	}
	joined, err := newDocumentBranch(left, right)
	return joined, true, err
}

func insertDocumentLeafNode(
	root *documentNode,
	index int,
	leaf documentLeaf,
) (*documentNode, error) {
	if index < 0 || index > documentNodeLeaves(root) {
		return nil, errDocumentGapOutOfRange
	}
	inserted := newDocumentLeafNode(leaf)
	if root == nil {
		return inserted, nil
	}
	if root.left == nil && root.right == nil {
		if index == 0 {
			return newDocumentBranch(inserted, root)
		}
		return newDocumentBranch(root, inserted)
	}
	leftLeaves := documentNodeLeaves(root.left)
	left, right := root.left, root.right
	var err error
	if index <= leftLeaves {
		left, err = insertDocumentLeafNode(left, index, leaf)
	} else {
		right, err = insertDocumentLeafNode(right, index-leftLeaves, leaf)
	}
	if err != nil {
		return nil, err
	}
	return balanceDocumentNodes(left, right)
}

func deleteDocumentLeafNode(root *documentNode, index int) (*documentNode, bool, error) {
	if index < 0 || index >= documentNodeLeaves(root) {
		return nil, false, errDocumentLeafOutOfRange
	}
	if root.left == nil && root.right == nil {
		return nil, true, nil
	}
	leftLeaves := documentNodeLeaves(root.left)
	left, right := root.left, root.right
	var changed bool
	var err error
	if index < leftLeaves {
		left, changed, err = deleteDocumentLeafNode(left, index)
	} else {
		right, changed, err = deleteDocumentLeafNode(right, index-leftLeaves)
	}
	if err != nil || !changed {
		return root, changed, err
	}
	if left == nil {
		return right, true, nil
	}
	if right == nil {
		return left, true, nil
	}
	joined, err := balanceDocumentNodes(left, right)
	return joined, true, err
}

func balanceDocumentNodes(left, right *documentNode) (*documentNode, error) {
	switch {
	case documentNodeHeight(left) > documentNodeHeight(right)+1:
		pivot := left
		if documentNodeHeight(pivot.left) >= documentNodeHeight(pivot.right) {
			newRight, err := newDocumentBranch(pivot.right, right)
			if err != nil {
				return nil, err
			}
			return newDocumentBranch(pivot.left, newRight)
		}
		middle := pivot.right
		newLeft, err := newDocumentBranch(pivot.left, middle.left)
		if err != nil {
			return nil, err
		}
		newRight, err := newDocumentBranch(middle.right, right)
		if err != nil {
			return nil, err
		}
		return newDocumentBranch(newLeft, newRight)
	case documentNodeHeight(right) > documentNodeHeight(left)+1:
		pivot := right
		if documentNodeHeight(pivot.right) >= documentNodeHeight(pivot.left) {
			newLeft, err := newDocumentBranch(left, pivot.left)
			if err != nil {
				return nil, err
			}
			return newDocumentBranch(newLeft, pivot.right)
		}
		middle := pivot.left
		newLeft, err := newDocumentBranch(left, middle.left)
		if err != nil {
			return nil, err
		}
		newRight, err := newDocumentBranch(middle.right, pivot.right)
		if err != nil {
			return nil, err
		}
		return newDocumentBranch(newLeft, newRight)
	default:
		return newDocumentBranch(left, right)
	}
}
