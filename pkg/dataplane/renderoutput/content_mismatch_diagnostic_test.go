// Copyright 2026 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package renderoutput

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/renderartifact"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/renderplan"
)

func TestArtifactContentMismatchEvidenceOmitsPayload(t *testing.T) {
	const planText = "private-plan-a"
	const artifactText = "private-file-b"
	file := &renderplan.File{Content: planText, Size: int64(len(planText)), Digest: renderplan.DigestString(planText)}
	err := validateArtifactContent(renderartifact.NewLiteralContent(artifactText), "maps/test.map", file)
	require.ErrorContains(t, err, "content differs from its plan file")
	var evidence *ArtifactContentMismatchError
	require.ErrorAs(t, err, &evidence)
	assert.Equal(t, &ArtifactContentMismatchError{
		Path: "maps/test.map", PlanBytes: len(planText), ArtifactBytes: len(artifactText),
		ReadOK: true, PlanDigestValid: true,
	}, evidence)
	assert.NotContains(t, err.Error(), planText)
	assert.NotContains(t, err.Error(), artifactText)
}

func TestArtifactContentMismatchEvidenceRetainsValidation(t *testing.T) {
	file := &renderplan.File{Content: "exact", Size: 5, Digest: renderplan.DigestString("exact")}
	require.NoError(t, validateArtifactContent(renderartifact.NewLiteralContent("exact"), "maps/test.map", file))
	require.ErrorContains(t, validateArtifactContent(renderartifact.NewLiteralContent("shorter"), "maps/test.map", file), "size differs")
	file.Digest = "invalid"
	err := validateArtifactContent(renderartifact.NewLiteralContent("other"), "maps/test.map", file)
	var evidence *ArtifactContentMismatchError
	require.ErrorAs(t, err, &evidence)
	assert.False(t, evidence.PlanDigestValid)
}
