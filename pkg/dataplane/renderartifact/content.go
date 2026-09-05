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

package renderartifact

import (
	"crypto/sha256"
	"errors"
	"io"
	"strings"

	"gitlab.com/haproxy-haptic/haptic/pkg/rendercontent"
)

type contentKind uint8

const (
	literalContent contentKind = iota + 1
	directDocumentContent
	processedDocumentContent
)

type immutableText struct {
	value string
	seal  *immutableText
}

type contentRoot struct {
	kind     contentKind
	text     *immutableText
	document rendercontent.Document
	seal     *contentRoot
	auth     contentRootAuthentication
}

type contentRootAuthentication struct {
	kind     contentKind
	text     *immutableText
	document rendercontent.Document
}

type contentAuthentication struct {
	owner  *Content
	root   *contentRoot
	bytes  int
	digest [sha256.Size]byte
}

// Content is one authenticated immutable artifact payload.
type Content struct {
	root   *contentRoot
	bytes  int
	digest [sha256.Size]byte
	seal   *Content
	auth   contentAuthentication
}

// NewLiteralContent detaches and seals literal artifact bytes.
func NewLiteralContent(value string) *Content {
	owned := strings.Clone(value)
	text := &immutableText{value: owned}
	text.seal = text
	root := &contentRoot{kind: literalContent, text: text}
	root.seal = root
	root.auth = contentRootAuthentication{kind: root.kind, text: root.text}
	return sealContent(root, owned)
}

// NewDocumentContent retains an authenticated source document and seals the
// final artifact bytes. Direct content must equal the document exactly.
func NewDocumentContent(document rendercontent.Document, final string, direct bool) (*Content, error) {
	if err := document.ValidateAuthentication(); err != nil {
		return nil, errors.Join(errInvalidContent, err)
	}
	if direct {
		rendered, err := document.String()
		if err != nil {
			return nil, errors.Join(errInvalidContent, err)
		}
		if rendered != final {
			return nil, errContentMismatch
		}
		root := &contentRoot{kind: directDocumentContent, document: document}
		root.seal = root
		root.auth = contentRootAuthentication{kind: root.kind, document: root.document}
		return sealContent(root, final), nil
	}
	owned := strings.Clone(final)
	text := &immutableText{value: owned}
	text.seal = text
	root := &contentRoot{kind: processedDocumentContent, text: text, document: document}
	root.seal = root
	root.auth = contentRootAuthentication{kind: root.kind, text: root.text, document: root.document}
	return sealContent(root, owned), nil
}

func sealContent(root *contentRoot, final string) *Content {
	content := &Content{
		root:   root,
		bytes:  len(final),
		digest: sha256.Sum256([]byte(final)),
	}
	content.seal = content
	content.auth = contentAuthentication{
		owner:  content,
		root:   content.root,
		bytes:  content.bytes,
		digest: content.digest,
	}
	return content
}

// ValidateAuthentication verifies the exact immutable representation in constant time.
func (c *Content) ValidateAuthentication() error {
	if c == nil || c.seal != c || c.root == nil || c.auth.owner != c ||
		c.auth.root != c.root || c.auth.bytes != c.bytes || c.auth.digest != c.digest || c.bytes < 0 {
		return errInvalidContent
	}
	return c.root.validate()
}

func (r *contentRoot) validate() error {
	if r == nil || r.seal != r || r.auth.kind != r.kind || r.auth.text != r.text ||
		r.auth.document != r.document {
		return errInvalidContent
	}
	switch r.kind {
	case literalContent:
		if r.text == nil || r.text.seal != r.text || r.document != (rendercontent.Document{}) {
			return errInvalidContent
		}
	case directDocumentContent:
		if r.text != nil {
			return errInvalidContent
		}
		if err := r.document.ValidateAuthentication(); err != nil {
			return errors.Join(errInvalidContent, err)
		}
	case processedDocumentContent:
		if r.text == nil || r.text.seal != r.text {
			return errInvalidContent
		}
		if err := r.document.ValidateAuthentication(); err != nil {
			return errors.Join(errInvalidContent, err)
		}
	default:
		return errInvalidContent
	}
	return nil
}

// Bytes returns the final payload length.
func (c *Content) Bytes() (int, error) {
	if err := c.ValidateAuthentication(); err != nil {
		return 0, err
	}
	return c.bytes, nil
}

// String returns the final payload bytes as a string.
func (c *Content) String() (string, error) {
	if err := c.ValidateAuthentication(); err != nil {
		return "", err
	}
	if c.root.kind == directDocumentContent {
		return c.root.document.String()
	}
	return c.root.text.value, nil
}

// WriteTo streams the final payload to writer.
func (c *Content) WriteTo(writer io.Writer) (int64, error) {
	if err := c.ValidateAuthentication(); err != nil {
		return 0, err
	}
	if writer == nil {
		return 0, errors.New("render artifact writer is nil")
	}
	if c.root.kind == directDocumentContent {
		return c.root.document.WriteTo(writer)
	}
	return writeString(writer, c.root.text.value)
}

// SameRoot reports exact authenticated representation identity.
func (c *Content) SameRoot(other *Content) (bool, error) {
	if err := c.ValidateAuthentication(); err != nil {
		return false, err
	}
	if err := other.ValidateAuthentication(); err != nil {
		return false, err
	}
	if c.root == other.root {
		return true, nil
	}
	if c.root.kind != directDocumentContent || other.root.kind != directDocumentContent {
		return false, nil
	}
	return c.root.document.SameRoot(other.root.document)
}

func exactContentEqual(left, right *Content) (bool, error) {
	if err := left.ValidateAuthentication(); err != nil {
		return false, err
	}
	if err := right.ValidateAuthentication(); err != nil {
		return false, err
	}
	same, err := left.SameRoot(right)
	if err != nil || same {
		return same, err
	}
	if left.bytes != right.bytes || left.digest != right.digest {
		return false, nil
	}
	leftText, leftHasText := left.literalText()
	rightText, rightHasText := right.literalText()
	if leftHasText && rightHasText {
		return leftText == rightText, nil
	}
	if leftHasText {
		return contentEqualsString(right, leftText)
	}
	if rightHasText {
		return contentEqualsString(left, rightText)
	}
	leftText, err = left.String()
	if err != nil {
		return false, err
	}
	return contentEqualsString(right, leftText)
}

func (c *Content) literalText() (string, bool) {
	if c.root.kind == directDocumentContent {
		return "", false
	}
	return c.root.text.value, true
}

func contentEqualsString(content *Content, expected string) (bool, error) {
	writer := &exactStringWriter{expected: expected}
	_, err := content.WriteTo(writer)
	if errors.Is(err, errContentMismatch) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return writer.offset == len(expected), nil
}

type exactStringWriter struct {
	expected string
	offset   int
}

func (w *exactStringWriter) Write(value []byte) (int, error) {
	if len(value) > len(w.expected)-w.offset {
		return 0, errContentMismatch
	}
	for index := range value {
		if value[index] != w.expected[w.offset+index] {
			return 0, errContentMismatch
		}
	}
	w.offset += len(value)
	return len(value), nil
}

func (w *exactStringWriter) WriteString(value string) (int, error) {
	if len(value) > len(w.expected)-w.offset || value != w.expected[w.offset:w.offset+len(value)] {
		return 0, errContentMismatch
	}
	w.offset += len(value)
	return len(value), nil
}

func writeString(writer io.Writer, value string) (int64, error) {
	written, err := io.WriteString(writer, value)
	if written < 0 || written > len(value) {
		return 0, errInvalidWriteCount
	}
	if written != len(value) && err == nil {
		err = io.ErrShortWrite
	}
	return int64(written), err
}
