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

func TestOutputHandleOverwriteCannotPoisonRetainedCopy(t *testing.T) {
	output, err := rendercontent.FromSorted([]rendercontent.Change{{Key: "part", Text: "safe"}})
	require.NoError(t, err)
	retained := output
	outputPointer := &output
	*outputPointer = rendercontent.Output{}

	require.Error(t, output.ValidateAuthentication())
	require.NoError(t, retained.ValidateAuthentication())
	text, err := retained.String()
	require.NoError(t, err)
	assert.Equal(t, "safe", text)
}

func TestEmptyOutputHandleOverwriteCannotPoisonGlobalRoot(t *testing.T) {
	output := rendercontent.Empty()
	retained := output
	outputPointer := &output
	*outputPointer = rendercontent.Output{}
	fresh := rendercontent.Empty()

	require.Error(t, output.ValidateAuthentication())
	require.NoError(t, retained.ValidateAuthentication())
	require.NoError(t, fresh.ValidateAuthentication())
	same, err := retained.SameRoot(fresh)
	require.NoError(t, err)
	assert.True(t, same)
}

func TestEmptyDocumentHandleOverwriteCannotPoisonGlobalRoot(t *testing.T) {
	document := rendercontent.EmptyDocument()
	retained := document
	documentPointer := &document
	*documentPointer = rendercontent.Document{}
	fresh := rendercontent.EmptyDocument()

	require.Error(t, document.ValidateAuthentication())
	require.NoError(t, retained.ValidateAuthentication())
	require.NoError(t, fresh.ValidateAuthentication())
	same, err := retained.SameRoot(fresh)
	require.NoError(t, err)
	assert.True(t, same)
}

func TestDocumentHandleOverwriteCannotPoisonRetainedChildren(t *testing.T) {
	output, err := rendercontent.FromSorted([]rendercontent.Change{{Key: "part", Text: "output"}})
	require.NoError(t, err)
	var childBuilder rendercontent.DocumentBuilder
	_, err = childBuilder.WriteString("child")
	require.NoError(t, err)
	child, err := childBuilder.Build(nil)
	require.NoError(t, err)

	var builder rendercontent.DocumentBuilder
	require.NoError(t, builder.AppendOutput(output))
	require.NoError(t, builder.AppendDocument(child))
	document, err := builder.Build(nil)
	require.NoError(t, err)

	outputPointer := &output
	*outputPointer = rendercontent.Output{}
	childPointer := &child
	*childPointer = rendercontent.Document{}
	retained := document
	documentPointer := &document
	*documentPointer = rendercontent.Document{}

	require.Error(t, output.ValidateAuthentication())
	require.Error(t, child.ValidateAuthentication())
	require.Error(t, document.ValidateAuthentication())
	require.NoError(t, retained.ValidateAuthentication())
	text, err := retained.String()
	require.NoError(t, err)
	assert.Equal(t, "outputchild", text)
}

func TestIndependentEqualRootsRemainDistinct(t *testing.T) {
	first, err := rendercontent.FromSorted([]rendercontent.Change{{Key: "part", Text: "same"}})
	require.NoError(t, err)
	second, err := rendercontent.FromSorted([]rendercontent.Change{{Key: "part", Text: "same"}})
	require.NoError(t, err)
	same, err := first.SameRoot(second)
	require.NoError(t, err)
	assert.False(t, same)

	var zeroOutput rendercontent.Output
	var zeroDocument rendercontent.Document
	require.Error(t, zeroOutput.ValidateAuthentication())
	require.Error(t, zeroDocument.ValidateAuthentication())
}
