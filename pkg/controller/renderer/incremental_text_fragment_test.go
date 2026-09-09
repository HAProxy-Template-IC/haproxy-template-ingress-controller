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

package renderer

import (
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/rendercontent"
	"gitlab.com/haproxy-haptic/haptic/pkg/templating"
)

type reportedTextFragment struct {
	text     string
	reported int64
	err      error
}

func (f reportedTextFragment) WriteTo(writer io.Writer) (int64, error) {
	if _, err := io.WriteString(writer, f.text); err != nil {
		return 0, err
	}
	return f.reported, f.err
}

func TestMaterializeIncrementalTextFragment(t *testing.T) {
	writeErr := errors.New("fragment write failed")
	for _, test := range []struct {
		name     string
		fragment templating.TextFragment
		want     string
		wantErr  string
	}{
		{name: "nil", wantErr: "fragment is nil"},
		{name: "invalid root", fragment: rendercontent.TextFragment{}, wantErr: "authentication"},
		{name: "empty", fragment: rendercontent.EmptyTextFragment()},
		{name: "string", fragment: incrementalStringFragment("a\x00b\xff"), want: "a\x00b\xff"},
		{name: "opaque writer", fragment: reportedTextFragment{text: "abc", reported: 3}, want: "abc"},
		{name: "underreported", fragment: reportedTextFragment{text: "abc", reported: 2}, wantErr: "reported 2 for 3 bytes"},
		{name: "overreported", fragment: reportedTextFragment{text: "abc", reported: 4}, wantErr: "reported 4 for 3 bytes"},
		{name: "negative count", fragment: reportedTextFragment{reported: -1}, wantErr: "reported -1 for 0 bytes"},
		{name: "partial failure", fragment: reportedTextFragment{text: "abc", reported: 3, err: writeErr}, wantErr: writeErr.Error()},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := materializeIncrementalTextFragment(test.fragment)
			if test.wantErr != "" {
				require.ErrorContains(t, err, test.wantErr)
				assert.Empty(t, got)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, test.want, got)
		})
	}
}

func TestMaterializeIncrementalTextFragmentKeepsRootsAndDelimitersDistinct(t *testing.T) {
	first, err := rendercontent.TextFragmentFromSorted([]rendercontent.TextPart{
		{Key: "a", Text: "first"}, {Key: "b", Text: "second"},
	})
	require.NoError(t, err)
	joined, err := first.WithDelimiter("\n---\x00\n")
	require.NoError(t, err)
	changed, err := joined.WithPart("a", "changed")
	require.NoError(t, err)
	deleted, err := changed.Delete("b")
	require.NoError(t, err)
	for range 2 {
		for _, test := range []struct {
			fragment rendercontent.TextFragment
			want     string
		}{
			{first, "firstsecond"}, {joined, "first\n---\x00\nsecond"},
			{changed, "changed\n---\x00\nsecond"}, {deleted, "changed"},
		} {
			got, err := materializeIncrementalTextFragment(test.fragment)
			require.NoError(t, err)
			assert.Equal(t, test.want, got)
		}
	}
}

func BenchmarkMaterializeIncrementalTextFragment(b *testing.B) {
	for _, count := range []int{300, 3000, 5000} {
		b.Run(fmt.Sprintf("parts=%d", count), func(b *testing.B) {
			parts := make([]rendercontent.TextPart, count)
			for index := range parts {
				parts[index] = rendercontent.TextPart{
					Key: fmt.Sprintf("%06d", index), Text: strings.Repeat("content\n", 16),
				}
			}
			fragment, err := rendercontent.TextFragmentFromSorted(parts)
			require.NoError(b, err)
			b.Run("one-change", func(b *testing.B) {
				benchmarkMaterializeChangedFragment(b, fragment, (count-1)*len(parts[0].Text)+len("changed\n"))
			})
			b.Run("unchanged", func(b *testing.B) {
				benchmarkMaterializeUnchangedFragment(b, fragment, count*len(parts[0].Text))
			})
		})
	}
}

func benchmarkMaterializeChangedFragment(b *testing.B, fragment rendercontent.TextFragment, wantLength int) {
	b.Helper()
	b.ReportAllocs()
	for b.Loop() {
		changed, err := fragment.WithPart("000000", "changed\n")
		if err != nil {
			b.Fatal(err)
		}
		text, err := materializeIncrementalTextFragment(changed)
		if err != nil || len(text) != wantLength {
			b.Fatalf("materialized %d bytes: %v", len(text), err)
		}
	}
}

func benchmarkMaterializeUnchangedFragment(b *testing.B, fragment templating.TextFragment, wantLength int) {
	b.Helper()
	_, err := materializeIncrementalTextFragment(fragment)
	require.NoError(b, err)
	b.ReportAllocs()
	for b.Loop() {
		text, err := materializeIncrementalTextFragment(fragment)
		if err != nil || len(text) != wantLength {
			b.Fatalf("materialized %d bytes: %v", len(text), err)
		}
	}
}
