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

package controller

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCurrentFilesAuthority_PublishedExactSourceRootIsStable(t *testing.T) {
	published := newPublishedAuxFiles("haptic")
	setPublishedFiles(published, map[string]map[string]string{
		haproxyMapFileGVR.String(): {"hosts.map": "example.test backend"},
	})
	authority := newCurrentFilesAuthority(published)

	first, err := authority.PublishedExactSource()
	require.NoError(t, err)

	// An unchanged watch refresh must keep the exact root: a follower proves
	// its currentFiles input unchanged through SameRoot, and without a stable
	// root every render misses the exact-cycle replay and pays a full render.
	setPublishedFiles(published, map[string]map[string]string{
		haproxyMapFileGVR.String(): {"hosts.map": "example.test backend"},
	})
	second, err := authority.PublishedExactSource()
	require.NoError(t, err)
	same, err := first.SameRoot(second)
	require.NoError(t, err)
	assert.True(t, same, "an unchanged published set must serve the same exact root")

	files, err := second.MaterializeCurrentAuxFiles()
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"hosts.map": "example.test backend"}, files)

	setPublishedFiles(published, map[string]map[string]string{
		haproxyMapFileGVR.String(): {"hosts.map": "example.test backend2"},
	})
	third, err := authority.PublishedExactSource()
	require.NoError(t, err)
	same, err = first.SameRoot(third)
	require.NoError(t, err)
	assert.False(t, same, "a changed published set must retire the root")
}

func TestCurrentFilesAuthority_PublishedExactSourceOutsideLeaderTerm(t *testing.T) {
	published := newPublishedAuxFiles("haptic")
	setPublishedFiles(published, map[string]map[string]string{
		haproxyMapFileGVR.String(): {"hosts.map": "example.test backend"},
	})
	authority := newCurrentFilesAuthority(published)
	generation := authority.BeginTerm()
	authority.EndTerm(generation)

	source, err := authority.PublishedExactSource()
	require.NoError(t, err)
	files, err := source.MaterializeCurrentAuxFiles()
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"hosts.map": "example.test backend"}, files,
		"outside a term the source serves the published set, like Snapshot does")
}
