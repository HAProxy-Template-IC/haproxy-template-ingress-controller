// Copyright 2025 Philipp Hossner
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

package deployplan_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/agent/api"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/deployplan"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/renderplan"
)

const routeMap = "maps/route-backend.map"

// TestDiffUnorderedMap covers rule 5 for a map whose lookup order does not
// matter: every change is reachable per entry.
func TestDiffUnorderedMap(t *testing.T) {
	tests := []struct {
		name   string
		before []renderplan.Entry
		after  []renderplan.Entry
		want   []api.Op
	}{
		{
			name:   "new key",
			before: []renderplan.Entry{entry("a.example.com", "be-a")},
			after:  []renderplan.Entry{entry("a.example.com", "be-a"), entry("b.example.com", "be-b")},
			want:   []api.Op{{Kind: api.OpMapAdd, Path: routeMap, Key: "b.example.com", Value: "be-b"}},
		},
		{
			name:   "single value change is set in place",
			before: []renderplan.Entry{entry("a.example.com", "be-a")},
			after:  []renderplan.Entry{entry("a.example.com", "be-z")},
			want:   []api.Op{{Kind: api.OpMapSet, Path: routeMap, Key: "a.example.com", Value: "be-z"}},
		},
		{
			name:   "removed key",
			before: []renderplan.Entry{entry("a.example.com", "be-a"), entry("b.example.com", "be-b")},
			after:  []renderplan.Entry{entry("a.example.com", "be-a")},
			want:   []api.Op{{Kind: api.OpMapDel, Path: routeMap, Key: "b.example.com"}},
		},
		{
			name:   "a value the line form would mangle is replaced",
			before: []renderplan.Entry{entry("a.example.com", "be-a")},
			after:  []renderplan.Entry{entry("a.example.com", "301|https|example.com; x")},
			want: []api.Op{
				{Kind: api.OpMapDel, Path: routeMap, Key: "a.example.com"},
				{Kind: api.OpMapAdd, Path: routeMap, Key: "a.example.com", Value: "301|https|example.com; x"},
			},
		},
		{
			name:   "a multiset change is deleted and re-added",
			before: []renderplan.Entry{entry("a.example.com", "be-a")},
			after:  []renderplan.Entry{entry("a.example.com", "be-a"), entry("a.example.com", "be-b")},
			want: []api.Op{
				{Kind: api.OpMapDel, Path: routeMap, Key: "a.example.com"},
				{Kind: api.OpMapAdd, Path: routeMap, Key: "a.example.com", Value: "be-a"},
				{Kind: api.OpMapAdd, Path: routeMap, Key: "a.example.com", Value: "be-b"},
			},
		},
		{
			name:   "reordering alone is not a change",
			before: []renderplan.Entry{entry("a.example.com", "be-a"), entry("b.example.com", "be-b")},
			after:  []renderplan.Entry{entry("b.example.com", "be-b"), entry("a.example.com", "be-a")},
			want:   nil,
		},
		{
			name:   "a key no line-form command can name replaces the map",
			before: []renderplan.Entry{entry("/api;v1", "be-a")},
			after:  []renderplan.Entry{entry("/api;v1", "be-z")},
			want:   []api.Op{{Kind: api.OpMapReplace, Path: routeMap}},
		},
		{
			name:   "an angle bracket in a key replaces the map",
			before: []renderplan.Entry{entry("/a>b", "be-a")},
			after:  []renderplan.Entry{entry("/a>b", "be-z")},
			want:   []api.Op{{Kind: api.OpMapReplace, Path: routeMap}},
		},
		{
			name:   "a new key the agent would refuse replaces the map",
			before: []renderplan.Entry{entry("a.example.com", "be-a")},
			after:  []renderplan.Entry{entry("a.example.com", "be-a"), entry("z;v1", "be-z")},
			want:   []api.Op{{Kind: api.OpMapReplace, Path: routeMap}},
		},
		{
			name:   "an emptied value takes the payload form",
			before: []renderplan.Entry{entry("a.example.com", "be-a")},
			after:  []renderplan.Entry{entry("a.example.com", "")},
			want: []api.Op{
				{Kind: api.OpMapDel, Path: routeMap, Key: "a.example.com"},
				{Kind: api.OpMapAdd, Path: routeMap, Key: "a.example.com", Value: ""},
			},
		},
		{
			name:   "an angle bracket in a value takes the payload form",
			before: []renderplan.Entry{entry("a.example.com", "be-a")},
			after:  []renderplan.Entry{entry("a.example.com", "x>y")},
			want: []api.Op{
				{Kind: api.OpMapDel, Path: routeMap, Key: "a.example.com"},
				{Kind: api.OpMapAdd, Path: routeMap, Key: "a.example.com", Value: "x>y"},
			},
		},
		{
			name:   "a value that spans lines replaces the map",
			before: []renderplan.Entry{entry("a.example.com", "be-a")},
			after:  []renderplan.Entry{entry("a.example.com", "be-a\nb.example.com be-b")},
			want:   []api.Op{{Kind: api.OpMapReplace, Path: routeMap}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			prev := basePlan(withMap(renderplan.Map{Path: routeMap, Entries: tt.before}))
			next := basePlan(withMap(renderplan.Map{Path: routeMap, Entries: tt.after}))

			got := deployplan.Diff(next, withMapsLoaded(on34(prev), routeMap))

			assert.Equal(t, tt.want, got.Ops)
			if len(tt.want) > 0 {
				assert.Equal(t, deployplan.VerdictRuntime, got.Verdict)
			}
		})
	}
}

// TestDiffOrderedMap covers rule 5 for a map HAProxy matches in order, where
// only an append that sorts last keeps the file and the worker in step.
func TestDiffOrderedMap(t *testing.T) {
	tests := []struct {
		name   string
		before []renderplan.Entry
		after  []renderplan.Entry
		want   []api.Op
	}{
		{
			name:   "append sorts after every existing key",
			before: []renderplan.Entry{entry("a", "1"), entry("b", "2")},
			after:  []renderplan.Entry{entry("a", "1"), entry("b", "2"), entry("c", "3")},
			want:   []api.Op{{Kind: api.OpMapAdd, Path: routeMap, Key: "c", Value: "3"}},
		},
		{
			name:   "an insertion in the middle replaces the map",
			before: []renderplan.Entry{entry("a", "1"), entry("c", "3")},
			after:  []renderplan.Entry{entry("a", "1"), entry("b", "2"), entry("c", "3")},
			want:   []api.Op{{Kind: api.OpMapReplace, Path: routeMap}},
		},
		{
			name:   "a new first entry replaces the map, however it sorts",
			before: []renderplan.Entry{entry("example.com", "be-generic")},
			after:  []renderplan.Entry{entry("z.example.com", "be-specific"), entry("example.com", "be-generic")},
			want:   []api.Op{{Kind: api.OpMapReplace, Path: routeMap}},
		},
		{
			name:   "appended keys are judged by file position, not by sort order",
			before: []renderplan.Entry{entry("m", "1")},
			after:  []renderplan.Entry{entry("m", "1"), entry("z", "2"), entry("b", "3")},
			want: []api.Op{
				{Kind: api.OpMapAdd, Path: routeMap, Key: "z", Value: "2"},
				{Kind: api.OpMapAdd, Path: routeMap, Key: "b", Value: "3"},
			},
		},
		{
			name:   "an append after a middle insertion replaces the map",
			before: []renderplan.Entry{entry("m", "1")},
			after:  []renderplan.Entry{entry("z", "2"), entry("m", "1"), entry("n", "3")},
			want:   []api.Op{{Kind: api.OpMapReplace, Path: routeMap}},
		},
		{
			name:   "an in-place value change keeps its position",
			before: []renderplan.Entry{entry("a", "1"), entry("b", "2")},
			after:  []renderplan.Entry{entry("a", "1"), entry("b", "9")},
			want:   []api.Op{{Kind: api.OpMapSet, Path: routeMap, Key: "b", Value: "9"}},
		},
		{
			name:   "a delete keeps the order of the rest",
			before: []renderplan.Entry{entry("a", "1"), entry("b", "2")},
			after:  []renderplan.Entry{entry("a", "1")},
			want:   []api.Op{{Kind: api.OpMapDel, Path: routeMap, Key: "b"}},
		},
		{
			name:   "reordering replaces the map",
			before: []renderplan.Entry{entry("a", "1"), entry("b", "2")},
			after:  []renderplan.Entry{entry("b", "2"), entry("a", "1")},
			want:   []api.Op{{Kind: api.OpMapReplace, Path: routeMap}},
		},
		{
			name:   "a value the line form would mangle replaces the map",
			before: []renderplan.Entry{entry("a", "1")},
			after:  []renderplan.Entry{entry("a", "1 2")},
			want:   []api.Op{{Kind: api.OpMapReplace, Path: routeMap}},
		},
		{
			name:   "an appended key the agent would refuse replaces the map",
			before: []renderplan.Entry{entry("a", "1")},
			after:  []renderplan.Entry{entry("a", "1"), entry("b;c", "2")},
			want:   []api.Op{{Kind: api.OpMapReplace, Path: routeMap}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			prev := basePlan(withMap(renderplan.Map{Path: routeMap, Ordered: true, Entries: tt.before}))
			next := basePlan(withMap(renderplan.Map{Path: routeMap, Ordered: true, Entries: tt.after}))

			got := deployplan.Diff(next, withMapsLoaded(on34(prev), routeMap))

			assert.Equal(t, tt.want, got.Ops)
		})
	}
}

func TestDiffMapNotLoadedIsWrittenOnly(t *testing.T) {
	prev := basePlan(withMap(renderplan.Map{Path: routeMap, Entries: []renderplan.Entry{entry("a", "1")}}))
	next := basePlan(withMap(renderplan.Map{Path: routeMap, Entries: []renderplan.Entry{entry("a", "2")}}))

	got := deployplan.Diff(next, on34(prev))

	assert.Equal(t, deployplan.VerdictFileOnly, got.Verdict)
	assert.Empty(t, got.Ops)
	reasonsContain(t, got.Reasons, "is not loaded at runtime, its file is written only")
}

func TestDiffMapPathMustBeASafeToken(t *testing.T) {
	const unsafe = "maps/route backend.map"
	prev := basePlan(withMap(renderplan.Map{Path: unsafe, Entries: []renderplan.Entry{entry("a", "1")}}))
	next := basePlan(withMap(renderplan.Map{Path: unsafe, Entries: []renderplan.Entry{entry("a", "2")}}))

	got := deployplan.Diff(next, withMapsLoaded(on34(prev), unsafe))

	require.Equal(t, deployplan.VerdictReload, got.Verdict)
	reasonsContain(t, got.Reasons, "the path is not a safe runtime token")
}

func TestDiffMapShortCircuitsOnExactFileContent(t *testing.T) {
	before := []renderplan.Entry{entry("a", "1")}
	prev := basePlan(withMap(renderplan.Map{Path: routeMap, Entries: before}))
	next := basePlan(
		withFile(&renderplan.File{
			Path: routeMap, Kind: renderplan.FileKindMap, Digest: entriesDigest(before),
			Content: entriesContent(before), ContentKnown: true,
		}),
		withMap(renderplan.Map{Path: routeMap, Entries: []renderplan.Entry{entry("a", "2")}}),
	)

	got := deployplan.Diff(next, withMapsLoaded(on34(prev), routeMap))

	assert.Equal(t, deployplan.VerdictFileOnly, got.Verdict)
	assert.Empty(t, got.Ops)
}

func TestDiffMapDeclaredReloadOnChangeReloads(t *testing.T) {
	prev := basePlan(withMap(renderplan.Map{Path: routeMap, Entries: []renderplan.Entry{entry("a", "1")}}))
	next := basePlan(
		withFile(&renderplan.File{Path: routeMap, Kind: renderplan.FileKindMap, Digest: "changed", ReloadOnChange: true}),
		withMap(renderplan.Map{Path: routeMap, Entries: []renderplan.Entry{entry("a", "2")}}),
	)

	got := deployplan.Diff(next, withMapsLoaded(on34(prev), routeMap))

	require.Equal(t, deployplan.VerdictReload, got.Verdict)
	reasonsContain(t, got.Reasons, "is declared reload-on-change")
}
