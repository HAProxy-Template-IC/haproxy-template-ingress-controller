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
	"slices"
	"strings"
	"sync"
)

var (
	errInvalidTextFragment         = errors.New("text fragment authentication seal does not match its root")
	errEmptyTextFragmentPartKey    = errors.New("text fragment part key is empty")
	errDuplicateTextFragmentChange = errors.New("text fragment changes repeat a part key")
	errUnsortedTextFragmentParts   = errors.New("text fragment parts are not strictly ordered")
	errTextFragmentTooLarge        = errors.New("text fragment size exceeds the platform limit")
	errInvalidTextFragmentWrite    = errors.New("text fragment writer returned an invalid byte count")
)

// TextPart is one present keyed part, including when Text is empty.
type TextPart struct {
	Key  string
	Text string
}

// TextFragmentChange replaces or deletes one keyed part.
type TextFragmentChange struct {
	Key     string
	Text    string
	Present bool
}

type textFragmentDelimiter struct {
	text string
}

type textFragmentMemo struct {
	once sync.Once
	text string
	err  error
}

type textFragmentAuthentication struct {
	root      *outputNode
	delimiter *textFragmentDelimiter
	memo      *textFragmentMemo
	bytes     int
	parts     int
}

type textFragmentState struct {
	root      *outputNode
	delimiter *textFragmentDelimiter
	bytes     int
	parts     int
	auth      textFragmentAuthentication
	memo      *textFragmentMemo
	seal      *textFragmentState
}

// TextFragment is an authenticated immutable ordered text sequence.
type TextFragment struct {
	state *textFragmentState
}

var emptyTextFragment = mustSealTextFragment(nil, nil)

// EmptyTextFragment returns an authenticated fragment without present parts.
func EmptyTextFragment() TextFragment {
	return emptyTextFragment
}

// TextFragmentFromSorted constructs a fragment from strictly increasing keys.
func TextFragmentFromSorted(parts []TextPart) (TextFragment, error) {
	for index := range parts {
		if parts[index].Key == "" {
			return TextFragment{}, errEmptyTextFragmentPartKey
		}
		if index > 0 {
			switch strings.Compare(parts[index-1].Key, parts[index].Key) {
			case 0:
				return TextFragment{}, errDuplicateTextFragmentChange
			case 1:
				return TextFragment{}, errUnsortedTextFragmentParts
			}
		}
	}
	if len(parts) == 0 {
		return EmptyTextFragment(), nil
	}
	root, err := buildBalancedTextFragment(len(parts), func(index int) (string, string) {
		return parts[index].Key, parts[index].Text
	})
	if err != nil {
		return TextFragment{}, err
	}
	return sealTextFragment(root, nil)
}

// Get returns one part without materializing the complete fragment.
func (f TextFragment) Get(key string) (text string, present bool, err error) {
	if err := f.ValidateAuthentication(); err != nil {
		return "", false, err
	}
	if key == "" {
		return "", false, errEmptyTextFragmentPartKey
	}
	for node := f.state.root; node != nil; {
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

// WithPart replaces one present part and path-copies its search path.
func (f TextFragment) WithPart(key, text string) (TextFragment, error) {
	if err := f.ValidateAuthentication(); err != nil {
		return TextFragment{}, err
	}
	if key == "" {
		return TextFragment{}, errEmptyTextFragmentPartKey
	}
	root, changed, err := insertNode(f.state.root, key, text)
	if err != nil {
		return TextFragment{}, textFragmentNodeError(err)
	}
	if !changed {
		return f, nil
	}
	return sealTextFragment(root, f.state.delimiter)
}

// Delete removes one part and returns the same fragment when it was absent.
func (f TextFragment) Delete(key string) (TextFragment, error) {
	if err := f.ValidateAuthentication(); err != nil {
		return TextFragment{}, err
	}
	if key == "" {
		return TextFragment{}, errEmptyTextFragmentPartKey
	}
	root, changed, err := deleteNode(f.state.root, key)
	if err != nil {
		return TextFragment{}, textFragmentNodeError(err)
	}
	if !changed {
		return f, nil
	}
	if root == nil {
		return EmptyTextFragment(), nil
	}
	return sealTextFragment(root, f.state.delimiter)
}

// Apply replaces and deletes a set of parts atomically.
func (f TextFragment) Apply(changes []TextFragmentChange) (TextFragment, error) {
	if err := f.ValidateAuthentication(); err != nil {
		return TextFragment{}, err
	}
	if len(changes) == 0 {
		return f, nil
	}
	owned, err := prepareTextFragmentChanges(changes)
	if err != nil {
		return TextFragment{}, err
	}
	if f.state.parts == 0 {
		return applyTextFragmentChangesToEmpty(f, owned)
	}
	root, changed, err := applyTextFragmentChanges(f.state.root, owned)
	if err != nil {
		return TextFragment{}, err
	}
	if !changed {
		return f, nil
	}
	if root == nil {
		return EmptyTextFragment(), nil
	}
	return sealTextFragment(root, f.state.delimiter)
}

// WithDelimiter returns a view that writes delimiter between present parts.
func (f TextFragment) WithDelimiter(delimiter string) (TextFragment, error) {
	if err := f.ValidateAuthentication(); err != nil {
		return TextFragment{}, err
	}
	if f.state.parts < 2 {
		delimiter = ""
	}
	if delimiter == textFragmentDelimiterText(f.state.delimiter) {
		return f, nil
	}
	var retained *textFragmentDelimiter
	if delimiter != "" {
		retained = &textFragmentDelimiter{text: delimiter}
	}
	return sealTextFragment(f.state.root, retained)
}

// ValidateAuthentication verifies the exact immutable root in constant time.
func (f TextFragment) ValidateAuthentication() error {
	state := f.state
	if state == nil || state.seal != state || state.auth.root != state.root ||
		state.auth.delimiter != state.delimiter || state.auth.memo != state.memo || state.memo == nil ||
		state.auth.bytes != state.bytes || state.auth.parts != state.parts ||
		state.parts != nodeParts(state.root) || state.delimiter != nil && state.delimiter.text == "" ||
		state.parts < 2 && state.delimiter != nil {
		return errInvalidTextFragment
	}
	expectedBytes, ok := textFragmentByteSize(state.root, state.delimiter)
	if !ok || expectedBytes != state.bytes {
		return errInvalidTextFragment
	}
	return nil
}

// Parts returns the number of present parts, including empty ones.
func (f TextFragment) Parts() (int, error) {
	if err := f.ValidateAuthentication(); err != nil {
		return 0, err
	}
	return f.state.parts, nil
}

// Bytes returns the serialized fragment length.
func (f TextFragment) Bytes() (int, error) {
	if err := f.ValidateAuthentication(); err != nil {
		return 0, err
	}
	return f.state.bytes, nil
}

// FirstByte returns the initial serialized byte without materializing the fragment.
func (f TextFragment) FirstByte() (value byte, present bool, err error) {
	if err := f.ValidateAuthentication(); err != nil {
		return 0, false, err
	}
	return authenticatedTextFragmentFirstByte(f.state)
}

func authenticatedTextFragmentFirstByte(state *textFragmentState) (value byte, present bool, err error) {
	if state.bytes == 0 {
		return 0, false, nil
	}
	first := minimumNode(state.root)
	if first.text != "" {
		return first.text[0], true, nil
	}
	delimiter := textFragmentDelimiterText(state.delimiter)
	if delimiter != "" {
		return delimiter[0], true, nil
	}
	value, present = firstTextByte(state.root)
	if !present {
		return 0, false, errInvalidTextFragment
	}
	return value, true, nil
}

// LastByte returns the final serialized byte without materializing the fragment.
func (f TextFragment) LastByte() (value byte, present bool, err error) {
	if err := f.ValidateAuthentication(); err != nil {
		return 0, false, err
	}
	return authenticatedTextFragmentLastByte(f.state)
}

func authenticatedTextFragmentLastByte(state *textFragmentState) (value byte, present bool, err error) {
	if state.bytes == 0 {
		return 0, false, nil
	}
	last := maximumNode(state.root)
	if last.text != "" {
		return last.text[len(last.text)-1], true, nil
	}
	delimiter := textFragmentDelimiterText(state.delimiter)
	if delimiter != "" {
		return delimiter[len(delimiter)-1], true, nil
	}
	value, present = lastTextByte(state.root)
	if !present {
		return 0, false, errInvalidTextFragment
	}
	return value, true, nil
}

// SameRoot reports exact structural identity after authenticating both fragments.
func (f TextFragment) SameRoot(other TextFragment) (bool, error) {
	if err := f.ValidateAuthentication(); err != nil {
		return false, err
	}
	if err := other.ValidateAuthentication(); err != nil {
		return false, err
	}
	return f.state.root == other.state.root &&
		textFragmentDelimiterText(f.state.delimiter) == textFragmentDelimiterText(other.state.delimiter), nil
}

// Walk visits every present part in key order, including empty parts.
func (f TextFragment) Walk(visit func(key, text string) error) error {
	if err := f.ValidateAuthentication(); err != nil {
		return err
	}
	if visit == nil {
		return errors.New("text fragment visitor is nil")
	}
	return walkNode(f.state.root, visit)
}

// WriteTo streams the fragment without creating an intermediate string.
func (f TextFragment) WriteTo(writer io.Writer) (int64, error) {
	if writer == nil {
		return 0, errors.New("text fragment writer is nil")
	}
	if err := f.ValidateAuthentication(); err != nil {
		return 0, err
	}
	written := 0
	first := true
	delimiter := textFragmentDelimiterText(f.state.delimiter)
	err := walkNode(f.state.root, func(_ string, text string) error {
		if !first {
			if err := writeTextFragmentString(writer, delimiter, &written); err != nil {
				return err
			}
		}
		first = false
		return writeTextFragmentString(writer, text, &written)
	})
	if err == nil && written != f.state.bytes {
		return int64(written), errInvalidTextFragment
	}
	return int64(written), err
}

// String materializes the fragment once for this immutable root and delimiter.
func (f TextFragment) String() (string, error) {
	length, err := f.Bytes()
	if err != nil {
		return "", err
	}
	f.state.memo.once.Do(func() {
		var output strings.Builder
		output.Grow(length)
		if _, err := f.WriteTo(&output); err != nil {
			f.state.memo.err = err
			return
		}
		if output.Len() != length {
			f.state.memo.err = errInvalidTextFragment
			return
		}
		f.state.memo.text = output.String()
	})
	return f.state.memo.text, f.state.memo.err
}

func sealTextFragment(root *outputNode, delimiter *textFragmentDelimiter) (TextFragment, error) {
	if nodeParts(root) < 2 || textFragmentDelimiterText(delimiter) == "" {
		delimiter = nil
	}
	bytes, ok := textFragmentByteSize(root, delimiter)
	if !ok {
		return TextFragment{}, errTextFragmentTooLarge
	}
	state := &textFragmentState{
		root: root, delimiter: delimiter, bytes: bytes, parts: nodeParts(root), memo: &textFragmentMemo{},
	}
	state.seal = state
	state.auth = textFragmentAuthentication{
		root: root, delimiter: delimiter, memo: state.memo, bytes: state.bytes, parts: state.parts,
	}
	return TextFragment{state: state}, nil
}

func mustSealTextFragment(root *outputNode, delimiter *textFragmentDelimiter) TextFragment {
	fragment, err := sealTextFragment(root, delimiter)
	if err != nil {
		panic(err)
	}
	return fragment
}

func buildBalancedTextFragment(length int, partAt func(int) (key, text string)) (*outputNode, error) {
	if length < 1 || partAt == nil {
		return nil, errInvalidTextFragment
	}
	return buildBalancedTextFragmentRange(0, length, partAt)
}

func buildBalancedTextFragmentRange(
	start int,
	end int,
	partAt func(int) (key, text string),
) (*outputNode, error) {
	middle := start + (end-start)/2
	var left *outputNode
	if middle > start {
		var err error
		left, err = buildBalancedTextFragmentRange(start, middle, partAt)
		if err != nil {
			return nil, err
		}
	}
	var right *outputNode
	if middle+1 < end {
		var err error
		right, err = buildBalancedTextFragmentRange(middle+1, end, partAt)
		if err != nil {
			return nil, err
		}
	}
	key, text := partAt(middle)
	node, err := makeNode(key, text, left, right)
	return node, textFragmentNodeError(err)
}

func prepareTextFragmentChanges(changes []TextFragmentChange) ([]TextFragmentChange, error) {
	owned := slices.Clone(changes)
	slices.SortFunc(owned, func(left, right TextFragmentChange) int {
		return strings.Compare(left.Key, right.Key)
	})
	for index := range owned {
		if owned[index].Key == "" {
			return nil, errEmptyTextFragmentPartKey
		}
		if index > 0 && owned[index-1].Key == owned[index].Key {
			return nil, errDuplicateTextFragmentChange
		}
	}
	return owned, nil
}

func applyTextFragmentChangesToEmpty(
	empty TextFragment,
	changes []TextFragmentChange,
) (TextFragment, error) {
	present := changes[:0]
	for _, change := range changes {
		if change.Present {
			present = append(present, change)
		}
	}
	if len(present) == 0 {
		return empty, nil
	}
	root, err := buildBalancedTextFragment(len(present), func(index int) (string, string) {
		return present[index].Key, present[index].Text
	})
	if err != nil {
		return TextFragment{}, err
	}
	return sealTextFragment(root, nil)
}

func applyTextFragmentChanges(
	root *outputNode,
	changes []TextFragmentChange,
) (*outputNode, bool, error) {
	changedAny := false
	for _, change := range changes {
		var changed bool
		var err error
		if change.Present {
			root, changed, err = insertNode(root, change.Key, change.Text)
		} else {
			root, changed, err = deleteNode(root, change.Key)
		}
		if err != nil {
			return nil, false, textFragmentNodeError(err)
		}
		changedAny = changedAny || changed
	}
	return root, changedAny, nil
}

func textFragmentByteSize(root *outputNode, delimiter *textFragmentDelimiter) (int, bool) {
	parts := nodeParts(root)
	textBytes := nodeBytes(root)
	if parts < 0 || textBytes < 0 || root != nil && parts == 0 {
		return 0, false
	}
	delimiterLength := len(textFragmentDelimiterText(delimiter))
	if parts < 2 || delimiterLength == 0 {
		return textBytes, true
	}
	maximum := int(^uint(0) >> 1)
	if parts-1 > maximum/delimiterLength {
		return 0, false
	}
	return addNonNegative(textBytes, (parts-1)*delimiterLength)
}

func textFragmentDelimiterText(delimiter *textFragmentDelimiter) string {
	if delimiter == nil {
		return ""
	}
	return delimiter.text
}

func textFragmentNodeError(err error) error {
	if errors.Is(err, errOutputTooLarge) {
		return errTextFragmentTooLarge
	}
	return err
}

func firstTextByte(node *outputNode) (byte, bool) {
	if node == nil || node.bytes == 0 {
		return 0, false
	}
	if nodeBytes(node.left) > 0 {
		return firstTextByte(node.left)
	}
	if node.text != "" {
		return node.text[0], true
	}
	return firstTextByte(node.right)
}

func lastTextByte(node *outputNode) (byte, bool) {
	if node == nil || node.bytes == 0 {
		return 0, false
	}
	if nodeBytes(node.right) > 0 {
		return lastTextByte(node.right)
	}
	if node.text != "" {
		return node.text[len(node.text)-1], true
	}
	return lastTextByte(node.left)
}

func maximumNode(node *outputNode) *outputNode {
	for node.right != nil {
		node = node.right
	}
	return node
}

func writeTextFragmentString(writer io.Writer, text string, written *int) error {
	if text == "" {
		return nil
	}
	count, err := io.WriteString(writer, text)
	if count < 0 || count > len(text) {
		return errInvalidTextFragmentWrite
	}
	maximum := int(^uint(0) >> 1)
	if *written > maximum-count {
		return errTextFragmentTooLarge
	}
	*written += count
	if err == nil && count != len(text) {
		return io.ErrShortWrite
	}
	return err
}
