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

package dataplane

import (
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/auxiliaryfiles"
)

func TestContentEqualDoesNotTrustChecksum(t *testing.T) {
	left := &AuxiliaryFiles{MapFiles: []auxiliaryfiles.MapFile{{Path: "a", Content: "bc"}}}
	right := &AuxiliaryFiles{MapFiles: []auxiliaryfiles.MapFile{{Path: "ab", Content: "c"}}}
	require.Equal(t, ComputeContentChecksum("config", left), ComputeContentChecksum("config", right))
	assert.False(t, ContentEqual("config", left, "config", right))
}

func TestContentEqualCoversFileProperties(t *testing.T) {
	reload := true
	base := &AuxiliaryFiles{GeneralFiles: []auxiliaryfiles.GeneralFile{{
		Filename: "error.http", Path: "general/error.http", Content: "body", ReloadOnPush: &reload,
	}}}
	equivalent := &AuxiliaryFiles{GeneralFiles: []auxiliaryfiles.GeneralFile{{
		Filename: "error.http", Path: "general/error.http", Content: "body",
	}}}
	assert.True(t, ContentEqual("config", base, "config", equivalent))
	assert.True(t, ContentEqual("config", nil, "config", &AuxiliaryFiles{}))

	changed := *equivalent
	changed.GeneralFiles = slices.Clone(equivalent.GeneralFiles)
	changed.GeneralFiles[0].Path = "general/other.http"
	require.Equal(t, ComputeContentChecksum("config", equivalent), ComputeContentChecksum("config", &changed))
	assert.False(t, ContentEqual("config", equivalent, "config", &changed))
}
