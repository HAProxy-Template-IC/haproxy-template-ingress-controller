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

package immutablevector

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRootOwnsValuesAndAuthenticatesExactIdentity(t *testing.T) {
	authority := NewAuthority[int]()
	source := []int{1, 2, 3}
	root, err := authority.Own(source)
	require.NoError(t, err)
	source[0] = 9

	values, err := root.Values(authority)
	require.NoError(t, err)
	assert.Equal(t, []int{1, 2, 3}, values)
	values[1] = 8
	value, err := root.At(authority, 1)
	require.NoError(t, err)
	assert.Equal(t, 2, value)

	copyRoot := root
	same, err := root.SameRoot(authority, copyRoot)
	require.NoError(t, err)
	assert.True(t, same)
	other, err := authority.Own([]int{1, 2, 3})
	require.NoError(t, err)
	same, err = root.SameRoot(authority, other)
	require.NoError(t, err)
	assert.False(t, same)
	require.Error(t, root.ValidateOwnership(NewAuthority[int]()))
}

func TestRootRejectsForgedStateAndAuthority(t *testing.T) {
	authority := NewAuthority[int]()
	root, err := authority.Own([]int{1})
	require.NoError(t, err)

	forgedState := *root.state
	forged := Root[int]{state: &forgedState}
	require.Error(t, forged.ValidateOwnership(authority))

	forgedAuthority := *authority
	require.Error(t, root.ValidateOwnership(&forgedAuthority))
	require.Error(t, (Root[int]{}).ValidateOwnership(authority))
}
