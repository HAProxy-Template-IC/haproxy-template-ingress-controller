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
)

var errInvalidDocument = errors.New("render document authentication seal does not match its root")

type documentLeafKind uint8

const (
	documentTextLeaf documentLeafKind = iota
	documentOutputLeaf
	documentTextFragmentLeaf
	documentChildLeaf
)

type documentLeaf struct {
	kind     documentLeafKind
	text     string
	retained any
	bytes    int
	first    byte
	last     byte
}

type documentNode struct {
	leaf   documentLeaf
	left   *documentNode
	right  *documentNode
	height int
	bytes  int
	leaves int
	first  byte
	last   byte
}

type documentMemo struct {
	once sync.Once
	text string
	err  error
}

type documentAuthentication struct {
	root   *documentNode
	memo   *documentMemo
	bytes  int
	leaves int
}

type documentState struct {
	root   *documentNode
	bytes  int
	leaves int
	auth   documentAuthentication
	memo   *documentMemo
	seal   *documentState
}

// Document is an authenticated immutable concatenation of retained text roots.
type Document struct {
	state *documentState
}

var emptyDocument = sealDocument(nil)

// EmptyDocument returns the authenticated empty document.
func EmptyDocument() Document {
	return emptyDocument
}

// DocumentBuilder builds one immutable document while retaining authenticated children.
type DocumentBuilder struct {
	leaves  []documentLeaf
	pending strings.Builder
	err     error
}

// Grow reserves capacity for n more bytes of pending literal text. A hint
// only: a wrong n costs nothing but the usual growth.
func (b *DocumentBuilder) Grow(n int) {
	if b.err != nil || n <= 0 {
		return
	}
	b.pending.Grow(n)
}

// Write copies bytes into the pending literal fragment.
func (b *DocumentBuilder) Write(value []byte) (int, error) {
	if b.err != nil {
		return 0, b.err
	}
	return b.pending.Write(value)
}

// WriteString appends immutable literal text.
func (b *DocumentBuilder) WriteString(value string) (int, error) {
	if b.err != nil {
		return 0, b.err
	}
	return b.pending.WriteString(value)
}

// AppendOutput retains an authenticated keyed output root.
func (b *DocumentBuilder) AppendOutput(output Output) error {
	if b.err != nil {
		return b.err
	}
	if err := output.ValidateAuthentication(); err != nil {
		b.err = err
		return err
	}
	length, err := output.Bytes()
	if err != nil {
		b.err = err
		return err
	}
	if length == 0 {
		return nil
	}
	first, _, err := output.FirstByte()
	if err != nil {
		b.err = err
		return err
	}
	last, _, err := output.LastByte()
	if err != nil {
		b.err = err
		return err
	}
	b.flushText()
	b.leaves = append(b.leaves, documentLeaf{
		kind: documentOutputLeaf, retained: output.state, bytes: length, first: first, last: last,
	})
	return nil
}

// AppendTextFragment retains an authenticated ordered text fragment.
func (b *DocumentBuilder) AppendTextFragment(fragment TextFragment) error {
	if b.err != nil {
		return b.err
	}
	if err := fragment.ValidateAuthentication(); err != nil {
		b.err = err
		return err
	}
	length := fragment.state.bytes
	if length == 0 {
		return nil
	}
	first, firstPresent, err := authenticatedTextFragmentFirstByte(fragment.state)
	if err != nil {
		b.err = err
		return err
	}
	last, lastPresent, err := authenticatedTextFragmentLastByte(fragment.state)
	if err != nil {
		b.err = err
		return err
	}
	if !firstPresent || !lastPresent {
		b.err = errInvalidTextFragment
		return b.err
	}
	b.flushText()
	b.leaves = append(b.leaves, documentLeaf{
		kind: documentTextFragmentLeaf, retained: fragment.state, bytes: length, first: first, last: last,
	})
	return nil
}

// AppendDocument retains an authenticated child document.
func (b *DocumentBuilder) AppendDocument(document Document) error {
	if b.err != nil {
		return b.err
	}
	if err := document.ValidateAuthentication(); err != nil {
		b.err = err
		return err
	}
	if document.state.bytes == 0 {
		return nil
	}
	b.flushText()
	b.leaves = append(b.leaves, documentLeaf{
		kind: documentChildLeaf, retained: document.state, bytes: document.state.bytes,
		first: document.state.root.first, last: document.state.root.last,
	})
	return nil
}

// Build seals the document and reuses previous when every exact leaf is unchanged.
func (b *DocumentBuilder) Build(previous *Document) (Document, error) {
	if b.err != nil {
		return Document{}, b.err
	}
	b.flushText()
	if len(b.leaves) == 0 {
		return EmptyDocument(), nil
	}
	if previous != nil {
		if err := previous.ValidateAuthentication(); err != nil {
			return Document{}, err
		}
		index := 0
		same, err := sameDocumentLeaves(previous.state.root, b.leaves, &index)
		if err != nil {
			return Document{}, err
		}
		if previous.state.leaves == len(b.leaves) && same && index == len(b.leaves) {
			return *previous, nil
		}
	}
	root, err := buildDocumentTree(b.leaves)
	if err != nil {
		return Document{}, err
	}
	return sealDocument(root), nil
}

func (b *DocumentBuilder) flushText() {
	if b.pending.Len() == 0 {
		return
	}
	text := b.pending.String()
	b.leaves = append(b.leaves, documentLeaf{
		kind: documentTextLeaf, text: text, bytes: len(text), first: text[0], last: text[len(text)-1],
	})
	b.pending.Reset()
}

// ValidateAuthentication verifies the exact immutable root in constant time.
func (d Document) ValidateAuthentication() error {
	state := d.state
	if state == nil || state.seal != state || state.auth.root != state.root ||
		state.auth.memo != state.memo || state.memo == nil ||
		state.auth.bytes != state.bytes || state.auth.leaves != state.leaves {
		return errInvalidDocument
	}
	return nil
}

// Bytes returns the complete document length.
func (d Document) Bytes() (int, error) {
	if err := d.ValidateAuthentication(); err != nil {
		return 0, err
	}
	return d.state.bytes, nil
}

// Leaves returns the number of retained fragments.
func (d Document) Leaves() (int, error) {
	if err := d.ValidateAuthentication(); err != nil {
		return 0, err
	}
	return d.state.leaves, nil
}

// LeafBytes returns the byte length of one top-level retained fragment.
func (d Document) LeafBytes(index int) (int, error) {
	if err := d.ValidateAuthentication(); err != nil {
		return 0, err
	}
	node, err := documentLeafNodeAt(d.state.root, index)
	if err != nil {
		return 0, err
	}
	return node.bytes, nil
}

// FirstByte returns the initial byte without materializing the document.
func (d Document) FirstByte() (value byte, found bool, err error) {
	if err := d.ValidateAuthentication(); err != nil {
		return 0, false, err
	}
	if d.state.root == nil {
		return 0, false, nil
	}
	return d.state.root.first, true, nil
}

// LastByte returns the final byte without materializing the document.
func (d Document) LastByte() (value byte, found bool, err error) {
	if err := d.ValidateAuthentication(); err != nil {
		return 0, false, err
	}
	if d.state.root == nil {
		return 0, false, nil
	}
	return d.state.root.last, true, nil
}

// SameRoot reports exact structural identity after authenticating both documents.
func (d Document) SameRoot(other Document) (bool, error) {
	if err := d.ValidateAuthentication(); err != nil {
		return false, err
	}
	if err := other.ValidateAuthentication(); err != nil {
		return false, err
	}
	return d.state.root == other.state.root, nil
}

// WriteTo streams every retained fragment in order.
func (d Document) WriteTo(writer io.Writer) (int64, error) {
	if writer == nil {
		return 0, errors.New("render document writer is nil")
	}
	if err := d.ValidateAuthentication(); err != nil {
		return 0, err
	}
	written, err := writeDocumentNode(d.state.root, writer)
	if err == nil && written != int64(d.state.bytes) {
		return written, errInvalidDocument
	}
	return written, err
}

// String materializes the exact document once for this root.
func (d Document) String() (string, error) {
	length, err := d.Bytes()
	if err != nil {
		return "", err
	}
	d.state.memo.once.Do(func() {
		var output strings.Builder
		output.Grow(length)
		if _, err := d.WriteTo(&output); err != nil {
			d.state.memo.err = err
			return
		}
		if output.Len() != length {
			d.state.memo.err = errInvalidDocument
			return
		}
		d.state.memo.text = output.String()
	})
	return d.state.memo.text, d.state.memo.err
}

func sealDocument(root *documentNode) Document {
	state := &documentState{
		root: root, bytes: documentNodeBytes(root), leaves: documentNodeLeaves(root), memo: &documentMemo{},
	}
	state.seal = state
	state.auth = documentAuthentication{
		root: root, memo: state.memo, bytes: state.bytes, leaves: state.leaves,
	}
	return Document{state: state}
}

func buildDocumentTree(leaves []documentLeaf) (*documentNode, error) {
	if len(leaves) == 0 {
		return nil, errInvalidDocument
	}
	if len(leaves) == 1 {
		return newDocumentLeafNode(leaves[0]), nil
	}
	middle := len(leaves) / 2
	left, err := buildDocumentTree(leaves[:middle])
	if err != nil {
		return nil, err
	}
	right, err := buildDocumentTree(leaves[middle:])
	if err != nil {
		return nil, err
	}
	return newDocumentBranch(left, right)
}

func newDocumentLeafNode(leaf documentLeaf) *documentNode {
	return &documentNode{
		leaf: leaf, height: 1, bytes: leaf.bytes, leaves: 1, first: leaf.first, last: leaf.last,
	}
}

func newDocumentBranch(left, right *documentNode) (*documentNode, error) {
	if left == nil || right == nil {
		return nil, errInvalidDocument
	}
	bytes, ok := addNonNegative(documentNodeBytes(left), documentNodeBytes(right))
	if !ok {
		return nil, errOutputTooLarge
	}
	leaves, ok := addNonNegative(documentNodeLeaves(left), documentNodeLeaves(right))
	if !ok {
		return nil, errOutputTooLarge
	}
	return &documentNode{
		left: left, right: right, height: max(documentNodeHeight(left), documentNodeHeight(right)) + 1,
		bytes: bytes, leaves: leaves, first: left.first, last: right.last,
	}, nil
}

func sameDocumentLeaves(node *documentNode, leaves []documentLeaf, index *int) (bool, error) {
	if node == nil {
		return true, nil
	}
	if node.left != nil || node.right != nil {
		leftSame, err := sameDocumentLeaves(node.left, leaves, index)
		if err != nil || !leftSame {
			return leftSame, err
		}
		return sameDocumentLeaves(node.right, leaves, index)
	}
	if *index >= len(leaves) {
		return false, nil
	}
	same, err := sameDocumentLeaf(node.leaf, leaves[*index])
	if err != nil || !same {
		return same, err
	}
	*index++
	return true, nil
}

func sameDocumentLeaf(left, right documentLeaf) (bool, error) {
	if left.kind != right.kind || left.bytes != right.bytes || left.first != right.first || left.last != right.last {
		return false, nil
	}
	switch left.kind {
	case documentTextLeaf:
		return left.text == right.text, nil
	case documentOutputLeaf:
		leftState, leftOK := left.retained.(*outputState)
		rightState, rightOK := right.retained.(*outputState)
		if !leftOK || !rightOK {
			return false, errInvalidDocument
		}
		return leftState == rightState, nil
	case documentTextFragmentLeaf:
		leftState, leftOK := left.retained.(*textFragmentState)
		rightState, rightOK := right.retained.(*textFragmentState)
		if !leftOK || !rightOK {
			return false, errInvalidDocument
		}
		if leftState == rightState {
			return true, nil
		}
		return (TextFragment{state: leftState}).SameRoot(TextFragment{state: rightState})
	case documentChildLeaf:
		leftState, leftOK := left.retained.(*documentState)
		rightState, rightOK := right.retained.(*documentState)
		if !leftOK || !rightOK {
			return false, errInvalidDocument
		}
		return leftState == rightState, nil
	default:
		return false, errInvalidDocument
	}
}

func writeDocumentNode(node *documentNode, writer io.Writer) (int64, error) {
	if node == nil {
		return 0, nil
	}
	if node.left != nil || node.right != nil {
		left, err := writeDocumentNode(node.left, writer)
		if err != nil {
			return left, err
		}
		right, err := writeDocumentNode(node.right, writer)
		return left + right, err
	}
	switch node.leaf.kind {
	case documentTextLeaf:
		written, err := io.WriteString(writer, node.leaf.text)
		if written < 0 || written > len(node.leaf.text) {
			return 0, errInvalidWriteCount
		}
		if err == nil && written != len(node.leaf.text) {
			return int64(written), io.ErrShortWrite
		}
		return int64(written), err
	case documentOutputLeaf:
		state, ok := node.leaf.retained.(*outputState)
		if !ok {
			return 0, errInvalidDocument
		}
		return (Output{state: state}).WriteTo(writer)
	case documentTextFragmentLeaf:
		state, ok := node.leaf.retained.(*textFragmentState)
		if !ok {
			return 0, errInvalidDocument
		}
		return (TextFragment{state: state}).WriteTo(writer)
	case documentChildLeaf:
		state, ok := node.leaf.retained.(*documentState)
		if !ok {
			return 0, errInvalidDocument
		}
		return (Document{state: state}).WriteTo(writer)
	default:
		return 0, errInvalidDocument
	}
}

func documentNodeHeight(node *documentNode) int {
	if node == nil {
		return 0
	}
	return node.height
}

func documentNodeBytes(node *documentNode) int {
	if node == nil {
		return 0
	}
	return node.bytes
}

func documentNodeLeaves(node *documentNode) int {
	if node == nil {
		return 0
	}
	return node.leaves
}
