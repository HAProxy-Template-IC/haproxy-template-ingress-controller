package comparator

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func intEqual(a, b int) bool { return a == b }

func TestDiffIndexedRules(t *testing.T) {
	tests := []struct {
		name string
		old  []int
		new  []int
		want []diffEntry[int]
	}{
		{
			name: "both empty",
			old:  nil,
			new:  nil,
			want: nil,
		},
		{
			name: "identical sequences",
			old:  []int{1, 2, 3},
			new:  []int{1, 2, 3},
			want: []diffEntry[int]{
				{Op: diffKeep, OldIndex: 0, NewIndex: 0, Value: 1},
				{Op: diffKeep, OldIndex: 1, NewIndex: 1, Value: 2},
				{Op: diffKeep, OldIndex: 2, NewIndex: 2, Value: 3},
			},
		},
		{
			name: "old empty inserts all",
			old:  nil,
			new:  []int{1, 2, 3},
			want: []diffEntry[int]{
				{Op: diffInsert, OldIndex: -1, NewIndex: 0, Value: 1},
				{Op: diffInsert, OldIndex: -1, NewIndex: 1, Value: 2},
				{Op: diffInsert, OldIndex: -1, NewIndex: 2, Value: 3},
			},
		},
		{
			name: "new empty deletes all",
			old:  []int{1, 2, 3},
			new:  nil,
			want: []diffEntry[int]{
				{Op: diffDelete, OldIndex: 0, NewIndex: -1, Value: 1},
				{Op: diffDelete, OldIndex: 1, NewIndex: -1, Value: 2},
				{Op: diffDelete, OldIndex: 2, NewIndex: -1, Value: 3},
			},
		},
		{
			name: "single insert at start",
			old:  []int{1, 2, 3},
			new:  []int{99, 1, 2, 3},
			want: []diffEntry[int]{
				{Op: diffInsert, OldIndex: -1, NewIndex: 0, Value: 99},
				{Op: diffKeep, OldIndex: 0, NewIndex: 1, Value: 1},
				{Op: diffKeep, OldIndex: 1, NewIndex: 2, Value: 2},
				{Op: diffKeep, OldIndex: 2, NewIndex: 3, Value: 3},
			},
		},
		{
			name: "single insert at middle",
			old:  []int{1, 2, 3},
			new:  []int{1, 99, 2, 3},
			want: []diffEntry[int]{
				{Op: diffKeep, OldIndex: 0, NewIndex: 0, Value: 1},
				{Op: diffInsert, OldIndex: -1, NewIndex: 1, Value: 99},
				{Op: diffKeep, OldIndex: 1, NewIndex: 2, Value: 2},
				{Op: diffKeep, OldIndex: 2, NewIndex: 3, Value: 3},
			},
		},
		{
			name: "single insert at end",
			old:  []int{1, 2, 3},
			new:  []int{1, 2, 3, 99},
			want: []diffEntry[int]{
				{Op: diffKeep, OldIndex: 0, NewIndex: 0, Value: 1},
				{Op: diffKeep, OldIndex: 1, NewIndex: 1, Value: 2},
				{Op: diffKeep, OldIndex: 2, NewIndex: 2, Value: 3},
				{Op: diffInsert, OldIndex: -1, NewIndex: 3, Value: 99},
			},
		},
		{
			name: "single delete at start",
			old:  []int{1, 2, 3, 4},
			new:  []int{2, 3, 4},
			want: []diffEntry[int]{
				{Op: diffDelete, OldIndex: 0, NewIndex: -1, Value: 1},
				{Op: diffKeep, OldIndex: 1, NewIndex: 0, Value: 2},
				{Op: diffKeep, OldIndex: 2, NewIndex: 1, Value: 3},
				{Op: diffKeep, OldIndex: 3, NewIndex: 2, Value: 4},
			},
		},
		{
			name: "single delete at middle",
			old:  []int{1, 2, 3, 4},
			new:  []int{1, 3, 4},
			want: []diffEntry[int]{
				{Op: diffKeep, OldIndex: 0, NewIndex: 0, Value: 1},
				{Op: diffDelete, OldIndex: 1, NewIndex: -1, Value: 2},
				{Op: diffKeep, OldIndex: 2, NewIndex: 1, Value: 3},
				{Op: diffKeep, OldIndex: 3, NewIndex: 2, Value: 4},
			},
		},
		{
			name: "single delete at end",
			old:  []int{1, 2, 3, 4},
			new:  []int{1, 2, 3},
			want: []diffEntry[int]{
				{Op: diffKeep, OldIndex: 0, NewIndex: 0, Value: 1},
				{Op: diffKeep, OldIndex: 1, NewIndex: 1, Value: 2},
				{Op: diffKeep, OldIndex: 2, NewIndex: 2, Value: 3},
				{Op: diffDelete, OldIndex: 3, NewIndex: -1, Value: 4},
			},
		},
		{
			name: "single element change produces delete then insert",
			old:  []int{1, 2, 3},
			new:  []int{1, 99, 3},
			want: []diffEntry[int]{
				{Op: diffKeep, OldIndex: 0, NewIndex: 0, Value: 1},
				{Op: diffDelete, OldIndex: 1, NewIndex: -1, Value: 2},
				{Op: diffInsert, OldIndex: -1, NewIndex: 1, Value: 99},
				{Op: diffKeep, OldIndex: 2, NewIndex: 2, Value: 3},
			},
		},
		{
			name: "mixed insert and delete",
			old:  []int{1, 2, 3, 4, 5},
			new:  []int{1, 3, 99, 5},
			want: []diffEntry[int]{
				{Op: diffKeep, OldIndex: 0, NewIndex: 0, Value: 1},
				{Op: diffDelete, OldIndex: 1, NewIndex: -1, Value: 2},
				{Op: diffKeep, OldIndex: 2, NewIndex: 1, Value: 3},
				{Op: diffDelete, OldIndex: 3, NewIndex: -1, Value: 4},
				{Op: diffInsert, OldIndex: -1, NewIndex: 2, Value: 99},
				{Op: diffKeep, OldIndex: 4, NewIndex: 3, Value: 5},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := diffIndexedRules(tt.old, tt.new, intEqual)

			require.Len(t, got, len(tt.want), "diff entry count mismatch")
			for i := range tt.want {
				assert.Equal(t, tt.want[i].Op, got[i].Op, "entry %d: op mismatch", i)
				assert.Equal(t, tt.want[i].OldIndex, got[i].OldIndex, "entry %d: old index mismatch", i)
				assert.Equal(t, tt.want[i].NewIndex, got[i].NewIndex, "entry %d: new index mismatch", i)
				assert.Equal(t, tt.want[i].Value, got[i].Value, "entry %d: value mismatch", i)
			}
		})
	}
}

func TestDiffIndexedRules_LargeSequenceSmallEditDistance(t *testing.T) {
	const size = 10000

	old := make([]int, size)
	for i := range old {
		old[i] = i
	}

	// Insert one element at position 5000
	dst := make([]int, 0, size+1)
	dst = append(dst, old[:5000]...)
	dst = append(dst, 999999)
	dst = append(dst, old[5000:]...)

	diffs := diffIndexedRules(old, dst, intEqual)

	var inserts, deletes, keeps int
	for _, d := range diffs {
		switch d.Op {
		case diffInsert:
			inserts++
		case diffDelete:
			deletes++
		case diffKeep:
			keeps++
		}
	}

	assert.Equal(t, 1, inserts, "should have exactly 1 insert")
	assert.Equal(t, 0, deletes, "should have 0 deletes")
	assert.Equal(t, size, keeps, "all original elements should be kept")
}

func TestCollapseEdits(t *testing.T) {
	tests := []struct {
		name  string
		diffs []diffEntry[int]
		want  []editEntry[int]
	}{
		{
			name:  "empty",
			diffs: nil,
			want:  nil,
		},
		{
			name: "keeps only produces no edits",
			diffs: []diffEntry[int]{
				{Op: diffKeep, OldIndex: 0, NewIndex: 0, Value: 1},
				{Op: diffKeep, OldIndex: 1, NewIndex: 1, Value: 2},
			},
			want: nil,
		},
		{
			name: "single insert",
			diffs: []diffEntry[int]{
				{Op: diffKeep, OldIndex: 0, NewIndex: 0, Value: 1},
				{Op: diffInsert, OldIndex: -1, NewIndex: 1, Value: 99},
				{Op: diffKeep, OldIndex: 1, NewIndex: 2, Value: 2},
			},
			want: []editEntry[int]{
				{Op: editInsert, NewIndex: 1, New: 99},
			},
		},
		{
			name: "single delete",
			diffs: []diffEntry[int]{
				{Op: diffKeep, OldIndex: 0, NewIndex: 0, Value: 1},
				{Op: diffDelete, OldIndex: 1, NewIndex: -1, Value: 2},
				{Op: diffKeep, OldIndex: 2, NewIndex: 1, Value: 3},
			},
			want: []editEntry[int]{
				{Op: editDelete, OldIndex: 1, Old: 2},
			},
		},
		{
			name: "adjacent delete+insert collapses to update",
			diffs: []diffEntry[int]{
				{Op: diffKeep, OldIndex: 0, NewIndex: 0, Value: 1},
				{Op: diffDelete, OldIndex: 1, NewIndex: -1, Value: 2},
				{Op: diffInsert, OldIndex: -1, NewIndex: 1, Value: 99},
				{Op: diffKeep, OldIndex: 2, NewIndex: 2, Value: 3},
			},
			want: []editEntry[int]{
				{Op: editUpdate, OldIndex: 1, NewIndex: 1, Old: 2, New: 99},
			},
		},
		{
			name: "multiple consecutive deletes+inserts collapse to updates",
			diffs: []diffEntry[int]{
				{Op: diffDelete, OldIndex: 0, NewIndex: -1, Value: 1},
				{Op: diffDelete, OldIndex: 1, NewIndex: -1, Value: 2},
				{Op: diffInsert, OldIndex: -1, NewIndex: 0, Value: 91},
				{Op: diffInsert, OldIndex: -1, NewIndex: 1, Value: 92},
				{Op: diffKeep, OldIndex: 2, NewIndex: 2, Value: 3},
			},
			want: []editEntry[int]{
				{Op: editUpdate, OldIndex: 0, NewIndex: 0, Old: 1, New: 91},
				{Op: editUpdate, OldIndex: 1, NewIndex: 1, Old: 2, New: 92},
			},
		},
		{
			name: "more deletes than inserts produces updates plus remaining deletes",
			diffs: []diffEntry[int]{
				{Op: diffDelete, OldIndex: 0, NewIndex: -1, Value: 1},
				{Op: diffDelete, OldIndex: 1, NewIndex: -1, Value: 2},
				{Op: diffDelete, OldIndex: 2, NewIndex: -1, Value: 3},
				{Op: diffInsert, OldIndex: -1, NewIndex: 0, Value: 91},
				{Op: diffKeep, OldIndex: 3, NewIndex: 1, Value: 4},
			},
			want: []editEntry[int]{
				{Op: editUpdate, OldIndex: 0, NewIndex: 0, Old: 1, New: 91},
				{Op: editDelete, OldIndex: 1, Old: 2},
				{Op: editDelete, OldIndex: 2, Old: 3},
			},
		},
		{
			name: "more inserts than deletes produces updates plus remaining inserts",
			diffs: []diffEntry[int]{
				{Op: diffDelete, OldIndex: 0, NewIndex: -1, Value: 1},
				{Op: diffInsert, OldIndex: -1, NewIndex: 0, Value: 91},
				{Op: diffInsert, OldIndex: -1, NewIndex: 1, Value: 92},
				{Op: diffKeep, OldIndex: 1, NewIndex: 2, Value: 2},
			},
			want: []editEntry[int]{
				{Op: editUpdate, OldIndex: 0, NewIndex: 0, Old: 1, New: 91},
				{Op: editInsert, NewIndex: 1, New: 92},
			},
		},
		{
			name: "separated delete and insert are not collapsed",
			diffs: []diffEntry[int]{
				{Op: diffDelete, OldIndex: 0, NewIndex: -1, Value: 1},
				{Op: diffKeep, OldIndex: 1, NewIndex: 0, Value: 2},
				{Op: diffInsert, OldIndex: -1, NewIndex: 1, Value: 99},
				{Op: diffKeep, OldIndex: 2, NewIndex: 2, Value: 3},
			},
			want: []editEntry[int]{
				{Op: editDelete, OldIndex: 0, Old: 1},
				{Op: editInsert, NewIndex: 1, New: 99},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := collapseEdits(tt.diffs)

			if tt.want == nil {
				assert.Empty(t, got)
				return
			}

			require.Len(t, got, len(tt.want), "edit entry count mismatch")
			for i := range tt.want {
				assert.Equal(t, tt.want[i].Op, got[i].Op, "entry %d: op mismatch", i)
				assert.Equal(t, tt.want[i].OldIndex, got[i].OldIndex, "entry %d: old index mismatch", i)
				assert.Equal(t, tt.want[i].NewIndex, got[i].NewIndex, "entry %d: new index mismatch", i)
				assert.Equal(t, tt.want[i].Old, got[i].Old, "entry %d: old value mismatch", i)
				assert.Equal(t, tt.want[i].New, got[i].New, "entry %d: new value mismatch", i)
			}
		})
	}
}

func TestDiffIndexedRules_CompletelyDisjointSequences(t *testing.T) {
	old := []int{1, 2, 3}
	dst := []int{4, 5, 6}

	diffs := diffIndexedRules(old, dst, intEqual)

	var inserts, deletes, keeps int
	for _, d := range diffs {
		switch d.Op {
		case diffInsert:
			inserts++
		case diffDelete:
			deletes++
		case diffKeep:
			keeps++
		}
	}

	assert.Equal(t, 3, inserts, "should insert all new elements")
	assert.Equal(t, 3, deletes, "should delete all old elements")
	assert.Equal(t, 0, keeps, "no common elements")
}

func TestDiffIndexedRules_DuplicateElements(t *testing.T) {
	// Duplicate elements test: the Myers algorithm must pick a valid LCS
	// even when elements repeat. [1,1,2] vs [1,2,1] share LCS [1,2] or [1,1].
	old := []int{1, 1, 2}
	dst := []int{1, 2, 1}

	diffs := diffIndexedRules(old, dst, intEqual)

	// Reconstruct old and new from diff entries to verify correctness
	var reconstructedOld, reconstructedNew []int
	for _, d := range diffs {
		switch d.Op {
		case diffKeep:
			reconstructedOld = append(reconstructedOld, d.Value)
			reconstructedNew = append(reconstructedNew, d.Value)
		case diffDelete:
			reconstructedOld = append(reconstructedOld, d.Value)
		case diffInsert:
			reconstructedNew = append(reconstructedNew, d.Value)
		}
	}

	assert.Equal(t, old, reconstructedOld, "reconstructed old should match input")
	assert.Equal(t, dst, reconstructedNew, "reconstructed new should match input")

	// The edit distance should be 2 (one delete + one insert)
	var edits int
	for _, d := range diffs {
		if d.Op != diffKeep {
			edits++
		}
	}
	assert.Equal(t, 2, edits, "edit distance should be 2")
}

func TestDiffIndexedRules_SingleElements(t *testing.T) {
	t.Run("single element replaced", func(t *testing.T) {
		diffs := diffIndexedRules([]int{1}, []int{2}, intEqual)

		var inserts, deletes int
		for _, d := range diffs {
			switch d.Op {
			case diffInsert:
				inserts++
			case diffDelete:
				deletes++
			}
		}
		assert.Equal(t, 1, inserts)
		assert.Equal(t, 1, deletes)
	})

	t.Run("single element to empty", func(t *testing.T) {
		diffs := diffIndexedRules([]int{1}, nil, intEqual)
		require.Len(t, diffs, 1)
		assert.Equal(t, diffDelete, diffs[0].Op)
		assert.Equal(t, 0, diffs[0].OldIndex)
	})

	t.Run("empty to single element", func(t *testing.T) {
		diffs := diffIndexedRules(nil, []int{1}, intEqual)
		require.Len(t, diffs, 1)
		assert.Equal(t, diffInsert, diffs[0].Op)
		assert.Equal(t, 0, diffs[0].NewIndex)
	})
}

func TestCollapseEdits_MultipleSeparatedBlocks(t *testing.T) {
	// Two disjoint change regions: update at position 1 and update at position 5.
	// Both should produce independent update operations.
	old := []int{10, 20, 30, 40, 50, 60, 70}
	dst := []int{10, 99, 30, 40, 50, 88, 70}

	diffs := diffIndexedRules(old, dst, intEqual)
	edits := collapseEdits(diffs)

	require.Len(t, edits, 2, "should produce 2 edit operations")

	assert.Equal(t, editUpdate, edits[0].Op, "first edit should be update")
	assert.Equal(t, 1, edits[0].OldIndex, "first update at old index 1")
	assert.Equal(t, 20, edits[0].Old, "first update old value")
	assert.Equal(t, 99, edits[0].New, "first update new value")

	assert.Equal(t, editUpdate, edits[1].Op, "second edit should be update")
	assert.Equal(t, 5, edits[1].OldIndex, "second update at old index 5")
	assert.Equal(t, 60, edits[1].Old, "second update old value")
	assert.Equal(t, 88, edits[1].New, "second update new value")
}

func TestCollapseEdits_CascadeElimination(t *testing.T) {
	// Simulate the cascade problem: 100 rules, one inserted at position 5.
	// Old index-based approach would produce 95 UPDATEs.
	// LCS approach should produce 1 INSERT and 0 UPDATEs.
	const size = 100

	old := make([]int, size)
	for i := range old {
		old[i] = i
	}

	dst := make([]int, 0, size+1)
	dst = append(dst, old[:5]...)
	dst = append(dst, 999)
	dst = append(dst, old[5:]...)

	diffs := diffIndexedRules(old, dst, intEqual)
	edits := collapseEdits(diffs)

	require.Len(t, edits, 1, "should produce exactly 1 edit operation")
	assert.Equal(t, editInsert, edits[0].Op, "should be an insert")
	assert.Equal(t, 5, edits[0].NewIndex, "insert should target new index 5")
	assert.Equal(t, 999, edits[0].New, "inserted value should be 999")
}
