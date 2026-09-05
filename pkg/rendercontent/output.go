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

// Package rendercontent holds the value types a render produces — Output,
// TextFragment, Document — and the deltas between two of them. The renderer
// and the dataplane both speak these types; neither HAProxy vocabulary nor
// Kubernetes vocabulary appears here.
package rendercontent

import (
	"errors"
	"io"
	"slices"
	"strings"
	"sync"
)

var (
	errInvalidOutput       = errors.New("render output authentication seal does not match its root")
	errEmptyPartKey        = errors.New("render output part key is empty")
	errDuplicatePartChange = errors.New("render output changes repeat a part key")
	errUnsortedParts       = errors.New("render output parts are not strictly ordered")
	errOutputTooLarge      = errors.New("render output size exceeds the platform limit")
	errInvalidWriteCount   = errors.New("render output writer returned an invalid byte count")
)

// Change replaces one ordered part. Empty text deletes the part.
type Change struct {
	Key  string
	Text string
}

type outputNode struct {
	key    string
	text   string
	left   *outputNode
	right  *outputNode
	height int
	bytes  int
	parts  int
}

type outputAuthentication struct {
	root  *outputNode
	memo  *outputMemo
	bytes int
	parts int
}

type outputMemo struct {
	once sync.Once
	text string
	err  error
}

type outputState struct {
	root  *outputNode
	bytes int
	parts int
	auth  outputAuthentication
	memo  *outputMemo
	seal  *outputState
}

// Output is an authenticated immutable ordered text sequence.
type Output struct {
	state *outputState
}

var emptyOutput = seal(nil)

// Empty returns an authenticated empty output.
func Empty() Output {
	return emptyOutput
}

// FromSorted constructs an output from strictly increasing keys.
func FromSorted(parts []Change) (Output, error) {
	owned := slices.Clone(parts)
	for index := range owned {
		if owned[index].Key == "" {
			return Output{}, errEmptyPartKey
		}
		if index > 0 {
			switch strings.Compare(owned[index-1].Key, owned[index].Key) {
			case 0:
				return Output{}, errDuplicatePartChange
			case 1:
				return Output{}, errUnsortedParts
			}
		}
	}
	compact := owned[:0]
	for _, part := range owned {
		if part.Text != "" {
			compact = append(compact, part)
		}
	}
	if len(compact) == 0 {
		return Empty(), nil
	}
	root, err := buildBalanced(compact)
	if err != nil {
		return Output{}, err
	}
	return seal(root), nil
}

// Get returns one part without materializing the complete output.
func (o Output) Get(key string) (text string, found bool, err error) {
	if err := o.ValidateAuthentication(); err != nil {
		return "", false, err
	}
	if key == "" {
		return "", false, errEmptyPartKey
	}
	for node := o.state.root; node != nil; {
		switch strings.Compare(key, node.key) {
		case -1:
			node = node.left
		case 1:
			node = node.right
		default:
			return node.text, true, nil
		}
	}
	return "", false, nil
}

// WithText replaces one part and path-copies only the affected search path.
func (o Output) WithText(key, text string) (Output, error) {
	if err := o.ValidateAuthentication(); err != nil {
		return Output{}, err
	}
	if key == "" {
		return Output{}, errEmptyPartKey
	}
	if text == "" {
		return o.Delete(key)
	}
	root, changed, err := insertNode(o.state.root, key, text)
	if err != nil {
		return Output{}, err
	}
	if !changed {
		return o, nil
	}
	return seal(root), nil
}

// Delete removes one part and returns the same output when it was absent.
func (o Output) Delete(key string) (Output, error) {
	if err := o.ValidateAuthentication(); err != nil {
		return Output{}, err
	}
	if key == "" {
		return Output{}, errEmptyPartKey
	}
	root, changed, err := deleteNode(o.state.root, key)
	if err != nil {
		return Output{}, err
	}
	if !changed {
		return o, nil
	}
	return seal(root), nil
}

// Apply replaces a set of parts atomically.
func (o Output) Apply(changes []Change) (Output, error) {
	if err := o.ValidateAuthentication(); err != nil {
		return Output{}, err
	}
	if len(changes) == 0 {
		return o, nil
	}
	owned := slices.Clone(changes)
	slices.SortFunc(owned, func(left, right Change) int {
		return strings.Compare(left.Key, right.Key)
	})
	for index := range owned {
		if owned[index].Key == "" {
			return Output{}, errEmptyPartKey
		}
		if index > 0 && owned[index-1].Key == owned[index].Key {
			return Output{}, errDuplicatePartChange
		}
	}
	if o.state.parts == 0 {
		return FromSorted(owned)
	}
	result := o
	var err error
	for _, change := range owned {
		result, err = result.WithText(change.Key, change.Text)
		if err != nil {
			return Output{}, err
		}
	}
	return result, nil
}

// ValidateAuthentication verifies the exact immutable root in constant time.
func (o Output) ValidateAuthentication() error {
	state := o.state
	if state == nil || state.seal != state || state.auth.root != state.root ||
		state.auth.memo != state.memo || state.memo == nil ||
		state.auth.bytes != state.bytes || state.auth.parts != state.parts {
		return errInvalidOutput
	}
	return nil
}

// Bytes returns the complete output length.
func (o Output) Bytes() (int, error) {
	if err := o.ValidateAuthentication(); err != nil {
		return 0, err
	}
	return o.state.bytes, nil
}

// Parts returns the number of non-empty text parts.
func (o Output) Parts() (int, error) {
	if err := o.ValidateAuthentication(); err != nil {
		return 0, err
	}
	return o.state.parts, nil
}

// FirstByte returns the first output byte without materializing the output.
func (o Output) FirstByte() (value byte, found bool, err error) {
	if err := o.ValidateAuthentication(); err != nil {
		return 0, false, err
	}
	node := o.state.root
	if node == nil {
		return 0, false, nil
	}
	for node.left != nil {
		node = node.left
	}
	return node.text[0], true, nil
}

// LastByte returns the last output byte without materializing the output.
func (o Output) LastByte() (value byte, found bool, err error) {
	if err := o.ValidateAuthentication(); err != nil {
		return 0, false, err
	}
	node := o.state.root
	if node == nil {
		return 0, false, nil
	}
	for node.right != nil {
		node = node.right
	}
	return node.text[len(node.text)-1], true, nil
}

// SameRoot reports exact structural identity after authenticating both values.
func (o Output) SameRoot(other Output) (bool, error) {
	if err := o.ValidateAuthentication(); err != nil {
		return false, err
	}
	if err := other.ValidateAuthentication(); err != nil {
		return false, err
	}
	return o.state.root == other.state.root, nil
}

// Walk visits non-empty parts in key order.
func (o Output) Walk(visit func(key, text string) error) error {
	if err := o.ValidateAuthentication(); err != nil {
		return err
	}
	if visit == nil {
		return errors.New("render output visitor is nil")
	}
	return walkNode(o.state.root, visit)
}

// WalkText visits non-empty text in key order.
func (o Output) WalkText(visit func(text string) error) error {
	if visit == nil {
		return errors.New("render output visitor is nil")
	}
	return o.Walk(func(_ string, text string) error {
		return visit(text)
	})
}

// WriteTo streams the output without creating an intermediate string.
func (o Output) WriteTo(writer io.Writer) (int64, error) {
	if writer == nil {
		return 0, errors.New("render output writer is nil")
	}
	written := 0
	err := o.WalkText(func(text string) error {
		count, err := io.WriteString(writer, text)
		if count < 0 || count > len(text) {
			return errInvalidWriteCount
		}
		if written > int(^uint(0)>>1)-count {
			return errOutputTooLarge
		}
		written += count
		if err == nil && count != len(text) {
			return io.ErrShortWrite
		}
		return err
	})
	return int64(written), err
}

// String materializes the output once for this immutable root.
func (o Output) String() (string, error) {
	length, err := o.Bytes()
	if err != nil {
		return "", err
	}
	o.state.memo.once.Do(func() {
		var output strings.Builder
		output.Grow(length)
		if _, err := o.WriteTo(&output); err != nil {
			o.state.memo.err = err
			return
		}
		if output.Len() != length {
			o.state.memo.err = errInvalidOutput
			return
		}
		o.state.memo.text = output.String()
	})
	return o.state.memo.text, o.state.memo.err
}

func seal(root *outputNode) Output {
	state := &outputState{root: root, bytes: nodeBytes(root), parts: nodeParts(root), memo: &outputMemo{}}
	state.seal = state
	state.auth = outputAuthentication{
		root: root, memo: state.memo, bytes: state.bytes, parts: state.parts,
	}
	return Output{state: state}
}

func buildBalanced(parts []Change) (*outputNode, error) {
	if len(parts) == 0 {
		return nil, errInvalidOutput
	}
	middle := len(parts) / 2
	var left *outputNode
	if middle > 0 {
		var err error
		left, err = buildBalanced(parts[:middle])
		if err != nil {
			return nil, err
		}
	}
	var right *outputNode
	if middle+1 < len(parts) {
		var err error
		right, err = buildBalanced(parts[middle+1:])
		if err != nil {
			return nil, err
		}
	}
	return makeNode(
		parts[middle].Key,
		parts[middle].Text,
		left,
		right,
	)
}

func insertNode(node *outputNode, key, text string) (*outputNode, bool, error) {
	if node == nil {
		created, err := makeNode(key, text, nil, nil)
		return created, err == nil, err
	}
	switch strings.Compare(key, node.key) {
	case -1:
		left, changed, err := insertNode(node.left, key, text)
		if err != nil {
			return nil, false, err
		}
		if !changed {
			return node, false, nil
		}
		updated, err := makeNode(node.key, node.text, left, node.right)
		if err != nil {
			return nil, false, err
		}
		updated, err = rebalance(updated)
		return updated, err == nil, err
	case 1:
		right, changed, err := insertNode(node.right, key, text)
		if err != nil {
			return nil, false, err
		}
		if !changed {
			return node, false, nil
		}
		updated, err := makeNode(node.key, node.text, node.left, right)
		if err != nil {
			return nil, false, err
		}
		updated, err = rebalance(updated)
		return updated, err == nil, err
	default:
		if node.text == text {
			return node, false, nil
		}
		updated, err := makeNode(node.key, text, node.left, node.right)
		return updated, err == nil, err
	}
}

func deleteNode(node *outputNode, key string) (*outputNode, bool, error) {
	if node == nil {
		return nil, false, nil
	}
	switch strings.Compare(key, node.key) {
	case -1:
		return deleteLeftNode(node, key)
	case 1:
		return deleteRightNode(node, key)
	default:
		return deleteCurrentNode(node)
	}
}

func deleteLeftNode(node *outputNode, key string) (*outputNode, bool, error) {
	left, changed, err := deleteNode(node.left, key)
	if err != nil || !changed {
		return node, changed, err
	}
	updated, err := makeNode(node.key, node.text, left, node.right)
	if err != nil {
		return nil, false, err
	}
	updated, err = rebalance(updated)
	return updated, err == nil, err
}

func deleteRightNode(node *outputNode, key string) (*outputNode, bool, error) {
	right, changed, err := deleteNode(node.right, key)
	if err != nil || !changed {
		return node, changed, err
	}
	updated, err := makeNode(node.key, node.text, node.left, right)
	if err != nil {
		return nil, false, err
	}
	updated, err = rebalance(updated)
	return updated, err == nil, err
}

func deleteCurrentNode(node *outputNode) (*outputNode, bool, error) {
	if node.left == nil {
		return node.right, true, nil
	}
	if node.right == nil {
		return node.left, true, nil
	}
	successor := minimumNode(node.right)
	right, _, err := deleteNode(node.right, successor.key)
	if err != nil {
		return nil, false, err
	}
	updated, err := makeNode(successor.key, successor.text, node.left, right)
	if err != nil {
		return nil, false, err
	}
	updated, err = rebalance(updated)
	return updated, err == nil, err
}

func minimumNode(node *outputNode) *outputNode {
	for node.left != nil {
		node = node.left
	}
	return node
}

func rebalance(node *outputNode) (*outputNode, error) {
	balance := nodeHeight(node.left) - nodeHeight(node.right)
	switch {
	case balance > 1:
		left := node.left
		if nodeHeight(left.left) < nodeHeight(left.right) {
			var err error
			left, err = rotateLeft(left)
			if err != nil {
				return nil, err
			}
			node, err = makeNode(node.key, node.text, left, node.right)
			if err != nil {
				return nil, err
			}
		}
		return rotateRight(node)
	case balance < -1:
		right := node.right
		if nodeHeight(right.right) < nodeHeight(right.left) {
			var err error
			right, err = rotateRight(right)
			if err != nil {
				return nil, err
			}
			node, err = makeNode(node.key, node.text, node.left, right)
			if err != nil {
				return nil, err
			}
		}
		return rotateLeft(node)
	default:
		return node, nil
	}
}

func rotateLeft(node *outputNode) (*outputNode, error) {
	right := node.right
	left, err := makeNode(node.key, node.text, node.left, right.left)
	if err != nil {
		return nil, err
	}
	return makeNode(right.key, right.text, left, right.right)
}

func rotateRight(node *outputNode) (*outputNode, error) {
	left := node.left
	right, err := makeNode(node.key, node.text, left.right, node.right)
	if err != nil {
		return nil, err
	}
	return makeNode(left.key, left.text, left.left, right)
}

func makeNode(key, text string, left, right *outputNode) (*outputNode, error) {
	bytes, ok := addNonNegative(nodeBytes(left), len(text), nodeBytes(right))
	if !ok {
		return nil, errOutputTooLarge
	}
	parts, ok := addNonNegative(nodeParts(left), 1, nodeParts(right))
	if !ok {
		return nil, errOutputTooLarge
	}
	return &outputNode{
		key:    key,
		text:   text,
		left:   left,
		right:  right,
		height: max(nodeHeight(left), nodeHeight(right)) + 1,
		bytes:  bytes,
		parts:  parts,
	}, nil
}

func addNonNegative(values ...int) (int, bool) {
	result := 0
	maximum := int(^uint(0) >> 1)
	for _, value := range values {
		if value < 0 || result > maximum-value {
			return 0, false
		}
		result += value
	}
	return result, true
}

func walkNode(node *outputNode, visit func(key, text string) error) error {
	if node == nil {
		return nil
	}
	if err := walkNode(node.left, visit); err != nil {
		return err
	}
	if err := visit(node.key, node.text); err != nil {
		return err
	}
	return walkNode(node.right, visit)
}

func nodeHeight(node *outputNode) int {
	if node == nil {
		return 0
	}
	return node.height
}

func nodeBytes(node *outputNode) int {
	if node == nil {
		return 0
	}
	return node.bytes
}

func nodeParts(node *outputNode) int {
	if node == nil {
		return 0
	}
	return node.parts
}
