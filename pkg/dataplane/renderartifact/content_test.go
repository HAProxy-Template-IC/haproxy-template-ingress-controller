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
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gitlab.com/haproxy-haptic/haptic/pkg/rendercontent"
)

func TestContentRepresentations(t *testing.T) {
	document := buildTestDocument(t, "final bytes")
	direct, err := NewDocumentContent(document, "final bytes", true)
	require.NoError(t, err)
	directAgain, err := NewDocumentContent(document, "final bytes", true)
	require.NoError(t, err)
	literal := NewLiteralContent("final bytes")
	processed, err := NewDocumentContent(document, "processed bytes", false)
	require.NoError(t, err)
	processedAgain, err := NewDocumentContent(document, "processed bytes", false)
	require.NoError(t, err)

	for _, test := range []struct {
		name    string
		content *Content
		want    string
	}{
		{name: "direct", content: direct, want: "final bytes"},
		{name: "literal", content: literal, want: "final bytes"},
		{name: "processed", content: processed, want: "processed bytes"},
	} {
		t.Run(test.name, func(t *testing.T) {
			require.NoError(t, test.content.ValidateAuthentication())
			length, bytesErr := test.content.Bytes()
			require.NoError(t, bytesErr)
			assert.Equal(t, len(test.want), length)
			value, stringErr := test.content.String()
			require.NoError(t, stringErr)
			assert.Equal(t, test.want, value)
			var output strings.Builder
			written, writeErr := test.content.WriteTo(&output)
			require.NoError(t, writeErr)
			assert.EqualValues(t, len(test.want), written)
			assert.Equal(t, test.want, output.String())
		})
	}

	same, err := direct.SameRoot(directAgain)
	require.NoError(t, err)
	assert.True(t, same)
	same, err = direct.SameRoot(literal)
	require.NoError(t, err)
	assert.False(t, same)
	same, err = processed.SameRoot(processedAgain)
	require.NoError(t, err)
	assert.False(t, same)
	equal, err := exactContentEqual(direct, literal)
	require.NoError(t, err)
	assert.True(t, equal)
	equal, err = exactContentEqual(processed, processedAgain)
	require.NoError(t, err)
	assert.True(t, equal)
}

func TestDocumentContentRejectsInvalidSourceAndDirectMismatch(t *testing.T) {
	_, err := NewDocumentContent(rendercontent.Document{}, "", true)
	require.ErrorIs(t, err, errInvalidContent)
	document := buildTestDocument(t, "safe")
	_, err = NewDocumentContent(document, "different", true)
	require.ErrorIs(t, err, errContentMismatch)

	var poisoned rendercontent.Document
	_, err = NewDocumentContent(poisoned, "safe", true)
	require.ErrorIs(t, err, errInvalidContent)
	_, err = NewDocumentContent(poisoned, "processed", false)
	require.ErrorIs(t, err, errInvalidContent)
}

func TestContentRejectsPoisonedValues(t *testing.T) {
	content := NewLiteralContent("safe")

	shallow := *content
	require.ErrorIs(t, shallow.ValidateAuthentication(), errInvalidContent)

	poisonedRoot := *content
	poisonedRoot.root = NewLiteralContent("evil").root
	require.ErrorIs(t, poisonedRoot.ValidateAuthentication(), errInvalidContent)

	poisonedBytes := *content
	poisonedBytes.bytes++
	require.ErrorIs(t, poisonedBytes.ValidateAuthentication(), errInvalidContent)

	poisonedDigest := *content
	poisonedDigest.digest[0]++
	require.ErrorIs(t, poisonedDigest.ValidateAuthentication(), errInvalidContent)

	poisonedSeal := *content
	poisonedSeal.seal = content
	require.ErrorIs(t, poisonedSeal.ValidateAuthentication(), errInvalidContent)

	originalRootSeal := content.root.seal
	content.root.seal = nil
	require.ErrorIs(t, content.ValidateAuthentication(), errInvalidContent)
	content.root.seal = originalRootSeal
	require.NoError(t, content.ValidateAuthentication())

	originalTextSeal := content.root.text.seal
	content.root.text.seal = nil
	require.ErrorIs(t, content.ValidateAuthentication(), errInvalidContent)
	content.root.text.seal = originalTextSeal
	require.NoError(t, content.ValidateAuthentication())

	var zero Content
	require.ErrorIs(t, zero.ValidateAuthentication(), errInvalidContent)
	_, err := (*Content)(nil).String()
	require.ErrorIs(t, err, errInvalidContent)
	_, err = content.SameRoot(nil)
	require.ErrorIs(t, err, errInvalidContent)
}

func TestContentDetectsPoisonedRetainedDocument(t *testing.T) {
	document := buildTestDocument(t, "safe")
	direct, err := NewDocumentContent(document, "safe", true)
	require.NoError(t, err)
	processed, err := NewDocumentContent(document, "processed", false)
	require.NoError(t, err)

	documentCopy := buildTestDocument(t, "safe")
	direct.root.document = documentCopy
	require.ErrorIs(t, direct.ValidateAuthentication(), errInvalidContent)
	direct.root.document = document
	require.NoError(t, direct.ValidateAuthentication())

	processed.root.document = documentCopy
	require.ErrorIs(t, processed.ValidateAuthentication(), errInvalidContent)
	processed.root.document = document
	require.NoError(t, processed.ValidateAuthentication())
}

func TestContentWriteContracts(t *testing.T) {
	content := NewLiteralContent("alpha")
	_, err := content.WriteTo(nil)
	require.Error(t, err)

	sentinel := errors.New("write failed")
	tests := []struct {
		name        string
		count       int
		writeErr    error
		wantWritten int64
		wantErr     error
	}{
		{name: "zero", count: 0, wantErr: io.ErrShortWrite},
		{name: "short", count: 4, wantWritten: 4, wantErr: io.ErrShortWrite},
		{name: "negative", count: -1, wantErr: errInvalidWriteCount},
		{name: "oversize", count: 6, wantErr: errInvalidWriteCount},
		{name: "partial error", count: 4, writeErr: sentinel, wantWritten: 4, wantErr: sentinel},
		{name: "full error", count: 5, writeErr: sentinel, wantWritten: 5, wantErr: sentinel},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			written, writeErr := content.WriteTo(fixedContentWriter{count: test.count, err: test.writeErr})
			require.ErrorIs(t, writeErr, test.wantErr)
			assert.Equal(t, test.wantWritten, written)
		})
	}
}

func TestExactContentComparisonDoesNotTrustDigest(t *testing.T) {
	left := NewLiteralContent("left")
	right := NewLiteralContent("evil")
	right.digest = left.digest
	right.auth.digest = left.digest
	require.NoError(t, right.ValidateAuthentication())

	equal, err := exactContentEqual(left, right)
	require.NoError(t, err)
	assert.False(t, equal)
}

func TestExactContentComparisonUsesFinalBytesNotSourceIdentity(t *testing.T) {
	firstDocument := buildTestDocument(t, "first source")
	secondDocument := buildTestDocument(t, "second source")
	first, err := NewDocumentContent(firstDocument, "same final", false)
	require.NoError(t, err)
	second, err := NewDocumentContent(secondDocument, "same final", false)
	require.NoError(t, err)

	same, err := first.SameRoot(second)
	require.NoError(t, err)
	assert.False(t, same)
	equal, err := exactContentEqual(first, second)
	require.NoError(t, err)
	assert.True(t, equal)

	changed, err := NewDocumentContent(firstDocument, "changed final", false)
	require.NoError(t, err)
	equal, err = exactContentEqual(first, changed)
	require.NoError(t, err)
	assert.False(t, equal)
}

func buildTestDocument(t *testing.T, value string) rendercontent.Document {
	t.Helper()
	var builder rendercontent.DocumentBuilder
	_, err := builder.WriteString(value)
	require.NoError(t, err)
	document, err := builder.Build(nil)
	require.NoError(t, err)
	return document
}

type fixedContentWriter struct {
	count int
	err   error
}

func (w fixedContentWriter) Write([]byte) (int, error) {
	return 0, errors.New("WriteString was not used")
}

func (w fixedContentWriter) WriteString(string) (int, error) {
	return w.count, w.err
}
