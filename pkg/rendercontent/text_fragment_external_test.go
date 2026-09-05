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

package rendercontent_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gitlab.com/haproxy-haptic/haptic/pkg/rendercontent"
)

func TestTextFragmentHandleOverwriteCannotPoisonRetainedCopy(t *testing.T) {
	fragment := externalTextFragment(t)
	retained := fragment
	fragmentPointer := &fragment
	*fragmentPointer = rendercontent.TextFragment{}

	require.Error(t, fragment.ValidateAuthentication())
	require.NoError(t, retained.ValidateAuthentication())
	text, err := retained.String()
	require.NoError(t, err)
	assert.Equal(t, "|value", text)
}

func TestEmptyTextFragmentHandleOverwriteCannotPoisonGlobalRoot(t *testing.T) {
	fragment := rendercontent.EmptyTextFragment()
	retained := fragment
	fragmentPointer := &fragment
	*fragmentPointer = rendercontent.TextFragment{}
	fresh := rendercontent.EmptyTextFragment()

	require.Error(t, fragment.ValidateAuthentication())
	require.NoError(t, retained.ValidateAuthentication())
	require.NoError(t, fresh.ValidateAuthentication())
	same, err := retained.SameRoot(fresh)
	require.NoError(t, err)
	assert.True(t, same)
}

func TestDocumentRetainsTextFragmentAfterCallerOverwrite(t *testing.T) {
	fragment := externalTextFragment(t)
	var builder rendercontent.DocumentBuilder
	require.NoError(t, builder.AppendTextFragment(fragment))
	fragmentPointer := &fragment
	*fragmentPointer = rendercontent.TextFragment{}
	document, err := builder.Build(nil)
	require.NoError(t, err)
	retained := document
	documentPointer := &document
	*documentPointer = rendercontent.Document{}

	require.Error(t, fragment.ValidateAuthentication())
	require.Error(t, document.ValidateAuthentication())
	require.NoError(t, retained.ValidateAuthentication())
	text, err := retained.String()
	require.NoError(t, err)
	assert.Equal(t, "|value", text)
}

func TestDocumentCanonicalizesOutputIrrelevantTextFragmentDelimiter(t *testing.T) {
	fragment, err := rendercontent.TextFragmentFromSorted([]rendercontent.TextPart{{Key: "part", Text: "value"}})
	require.NoError(t, err)
	first, err := fragment.WithDelimiter("|")
	require.NoError(t, err)
	second, err := fragment.WithDelimiter("different")
	require.NoError(t, err)
	same, err := first.SameRoot(second)
	require.NoError(t, err)
	assert.True(t, same)

	var firstBuilder rendercontent.DocumentBuilder
	require.NoError(t, firstBuilder.AppendTextFragment(first))
	firstDocument, err := firstBuilder.Build(nil)
	require.NoError(t, err)
	var secondBuilder rendercontent.DocumentBuilder
	require.NoError(t, secondBuilder.AppendTextFragment(second))
	secondDocument, err := secondBuilder.Build(&firstDocument)
	require.NoError(t, err)
	same, err = firstDocument.SameRoot(secondDocument)
	require.NoError(t, err)
	assert.True(t, same)
}

func TestDocumentRejectsForeignEqualTextFragmentRootForReuse(t *testing.T) {
	first := externalTextFragment(t)
	second := externalTextFragment(t)
	same, err := first.SameRoot(second)
	require.NoError(t, err)
	assert.False(t, same)

	var firstBuilder rendercontent.DocumentBuilder
	require.NoError(t, firstBuilder.AppendTextFragment(first))
	firstDocument, err := firstBuilder.Build(nil)
	require.NoError(t, err)
	var secondBuilder rendercontent.DocumentBuilder
	require.NoError(t, secondBuilder.AppendTextFragment(second))
	secondDocument, err := secondBuilder.Build(&firstDocument)
	require.NoError(t, err)
	same, err = firstDocument.SameRoot(secondDocument)
	require.NoError(t, err)
	assert.False(t, same)
	text, err := secondDocument.String()
	require.NoError(t, err)
	assert.Equal(t, "|value", text)
}

func TestDocumentReusesEqualDelimiterViewsOfExactTextFragmentRoot(t *testing.T) {
	base, err := rendercontent.TextFragmentFromSorted([]rendercontent.TextPart{
		{Key: "a", Text: ""}, {Key: "b", Text: "value"},
	})
	require.NoError(t, err)
	first, err := base.WithDelimiter("|")
	require.NoError(t, err)
	second, err := base.WithDelimiter(string([]byte{'|'}))
	require.NoError(t, err)
	same, err := first.SameRoot(second)
	require.NoError(t, err)
	assert.True(t, same)

	var firstBuilder rendercontent.DocumentBuilder
	require.NoError(t, firstBuilder.AppendTextFragment(first))
	firstDocument, err := firstBuilder.Build(nil)
	require.NoError(t, err)
	var secondBuilder rendercontent.DocumentBuilder
	require.NoError(t, secondBuilder.AppendTextFragment(second))
	secondDocument, err := secondBuilder.Build(&firstDocument)
	require.NoError(t, err)
	same, err = firstDocument.SameRoot(secondDocument)
	require.NoError(t, err)
	assert.True(t, same)
}

func TestDocumentOmitsByteEmptyTextFragmentRoots(t *testing.T) {
	var previousBuilder rendercontent.DocumentBuilder
	_, err := previousBuilder.WriteString("prefixsuffix")
	require.NoError(t, err)
	previous, err := previousBuilder.Build(nil)
	require.NoError(t, err)

	presentEmpty, err := rendercontent.EmptyTextFragment().WithPart("part", "")
	require.NoError(t, err)
	presentEmpty, err = presentEmpty.WithDelimiter("ignored")
	require.NoError(t, err)
	twoEmpty, err := presentEmpty.WithPart("second", "")
	require.NoError(t, err)

	var builder rendercontent.DocumentBuilder
	_, err = builder.WriteString("prefix")
	require.NoError(t, err)
	require.NoError(t, builder.AppendTextFragment(rendercontent.EmptyTextFragment()))
	require.NoError(t, builder.AppendTextFragment(presentEmpty))
	require.NoError(t, builder.AppendTextFragment(twoEmpty))
	_, err = builder.WriteString("suffix")
	require.NoError(t, err)
	document, err := builder.Build(&previous)
	require.NoError(t, err)
	same, err := previous.SameRoot(document)
	require.NoError(t, err)
	assert.True(t, same)

	visible, err := twoEmpty.WithDelimiter("|")
	require.NoError(t, err)
	var visibleBuilder rendercontent.DocumentBuilder
	_, err = visibleBuilder.WriteString("prefix")
	require.NoError(t, err)
	require.NoError(t, visibleBuilder.AppendTextFragment(visible))
	_, err = visibleBuilder.WriteString("suffix")
	require.NoError(t, err)
	visibleDocument, err := visibleBuilder.Build(&previous)
	require.NoError(t, err)
	same, err = previous.SameRoot(visibleDocument)
	require.NoError(t, err)
	assert.False(t, same)
	text, err := visibleDocument.String()
	require.NoError(t, err)
	assert.Equal(t, "prefix|suffix", text)
}

func TestZeroTextFragmentFailsClosed(t *testing.T) {
	var zero rendercontent.TextFragment
	require.Error(t, zero.ValidateAuthentication())
	_, err := zero.String()
	require.Error(t, err)

	var builder rendercontent.DocumentBuilder
	require.Error(t, builder.AppendTextFragment(zero))
	_, err = builder.Build(nil)
	require.Error(t, err)
}

func externalTextFragment(t *testing.T) rendercontent.TextFragment {
	t.Helper()
	fragment, err := rendercontent.TextFragmentFromSorted([]rendercontent.TextPart{
		{Key: "a", Text: ""}, {Key: "b", Text: "value"},
	})
	require.NoError(t, err)
	fragment, err = fragment.WithDelimiter("|")
	require.NoError(t, err)
	return fragment
}
