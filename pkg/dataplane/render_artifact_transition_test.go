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

package dataplane

import (
	"testing"

	"github.com/stretchr/testify/require"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/auxiliaryfiles"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/renderartifact"
)

func TestBuildAuxiliaryFileTransitionMatchesFullSnapshot(t *testing.T) {
	authority := renderartifact.NewAuthority()
	baseFiles := &AuxiliaryFiles{
		MapFiles: []auxiliaryfiles.MapFile{
			{Path: "maps/change.map", Content: "old"},
			{Path: "maps/delete.map", Content: "delete"},
		},
	}
	base, err := BuildAuxiliaryFileSnapshot(authority, nil, baseFiles)
	require.NoError(t, err)
	nextFiles := &AuxiliaryFiles{
		MapFiles: []auxiliaryfiles.MapFile{
			{Path: "maps/change.map", Content: "new"},
			{Path: "maps/insert.map", Content: "insert"},
		},
	}

	next, delta, err := BuildAuxiliaryFileTransition(authority, base, nextFiles)
	require.NoError(t, err)
	require.NoError(t, delta.ValidateAuthentication())
	want, err := BuildAuxiliaryFileSnapshot(authority, base, nextFiles)
	require.NoError(t, err)
	equal, err := next.ExactEqual(want)
	require.NoError(t, err)
	require.True(t, equal)
}

func TestBuildAuxiliaryFileTransitionColdAndNoOp(t *testing.T) {
	authority := renderartifact.NewAuthority()
	files := &AuxiliaryFiles{MapFiles: []auxiliaryfiles.MapFile{
		{Path: "maps/a.map", Content: "a"},
	}}

	cold, coldDelta, err := BuildAuxiliaryFileTransition(authority, nil, files)
	require.NoError(t, err)
	require.Nil(t, coldDelta)
	warm, warmDelta, err := BuildAuxiliaryFileTransition(authority, cold, files)
	require.NoError(t, err)
	require.Same(t, cold, warm)
	same, err := warmDelta.SameRoot()
	require.NoError(t, err)
	require.True(t, same)
}

func TestBuildAuxiliaryFileTransitionPreservesRuntimePathValidation(t *testing.T) {
	authority := renderartifact.NewAuthority()
	files := &AuxiliaryFiles{MapFiles: []auxiliaryfiles.MapFile{
		{Path: "maps/a.map", Content: "a"},
	}}
	base, _, err := BuildAuxiliaryFileTransitionWithRuntimePaths(
		authority, nil, files,
		func(renderartifact.Family, string) (string, error) { return "runtime/a.map", nil },
	)
	require.NoError(t, err)

	next, delta, err := BuildAuxiliaryFileTransitionWithRuntimePaths(
		authority, base, files,
		func(renderartifact.Family, string) (string, error) { return "runtime/b.map", nil },
	)
	require.NoError(t, err)
	require.NotSame(t, base, next)
	structural, err := delta.RequiresFullValidation()
	require.NoError(t, err)
	require.True(t, structural)
}
