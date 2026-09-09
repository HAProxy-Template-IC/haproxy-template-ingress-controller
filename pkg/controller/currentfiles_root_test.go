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

package controller

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCurrentAuxFilesRootUsesExactBytes(t *testing.T) {
	authority := newCurrentFilesAuthority(nil)
	for _, test := range []struct {
		name  string
		left  map[string]string
		right map[string]string
		same  bool
	}{
		{name: "empty", left: nil, right: map[string]string{}, same: true},
		{name: "equal", left: map[string]string{"file": "a\x00b"}, right: map[string]string{"file": "a\x00b"}, same: true},
		{name: "content", left: map[string]string{"file": "before"}, right: map[string]string{"file": "after"}},
		{name: "missing", left: map[string]string{"file": ""}, right: map[string]string{}},
		{name: "invalid utf8 content", left: map[string]string{"file": "\xff"}, right: map[string]string{"file": "\xfe"}},
		{name: "invalid utf8 key", left: map[string]string{"\xff": "same"}, right: map[string]string{"\xfe": "same"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			newSource := func(files map[string]string) *currentAuxFilesSource {
				root := newCurrentAuxFilesMapRoot(files)
				source := &currentAuxFilesSource{authority: authority, generation: 1, root: root}
				source.seal = source
				return source
			}
			left, right := newSource(test.left), newSource(test.right)
			same, err := left.SameRoot(right)
			require.NoError(t, err)
			assert.Equal(t, test.same, same)
			if test.left != nil {
				test.left["caller mutation"] = "ignored"
			}
			materialized, err := left.MaterializeCurrentAuxFiles()
			require.NoError(t, err)
			assert.NotContains(t, materialized, "caller mutation")
			materialized["output mutation"] = "ignored"
			assert.NotContains(t, left.root.files, "output mutation")
			copied := *left.root
			left.root = &copied
			_, err = left.SameRoot(right)
			require.ErrorContains(t, err, "invalid exact root")
		})
	}
}

func BenchmarkCurrentAuxFilesMapRoot(b *testing.B) {
	for _, count := range []int{1, 32} {
		b.Run(fmt.Sprintf("files=%d", count), func(b *testing.B) {
			files := make(map[string]string, count)
			for index := range count {
				files[fmt.Sprintf("file-%04d", index)] = strings.Repeat("key backend\n", 16384)
			}
			b.ReportAllocs()
			for b.Loop() {
				root := newCurrentAuxFilesMapRoot(files)
				if len(root.files) != count {
					b.Fatalf("root has %d files, want %d", len(root.files), count)
				}
			}
		})
	}
}
