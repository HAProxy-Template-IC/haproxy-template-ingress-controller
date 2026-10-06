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

package incremental

import (
	"strings"
	"testing"
	"unsafe"

	"github.com/stretchr/testify/require"
)

func TestKeysShareStorageForEqualContent(t *testing.T) {
	first := NewInputKey(strings.Repeat("binding-input/", 4))
	second := NewInputKey(strings.Repeat("binding-input/", 4))
	require.Equal(t, first, second)
	require.Same(t, unsafe.StringData(first.Opaque()), unsafe.StringData(second.Opaque()))

	query := NewQueryKey(strings.Repeat("component/", 4))
	require.Same(t, unsafe.StringData(query.Opaque()), unsafe.StringData(NewQueryKey(query.Opaque()).Opaque()))
}

func TestEmptyKeysAreZeroValues(t *testing.T) {
	require.Equal(t, InputKey{}, NewInputKey(""))
	require.Equal(t, QueryKey{}, NewQueryKey(""))
	require.Empty(t, InputKey{}.Opaque())
	require.Empty(t, QueryKey{}.Opaque())
	require.False(t, validInputKey(InputKey{}))
	require.False(t, validQueryKey(QueryKey{}))
}
