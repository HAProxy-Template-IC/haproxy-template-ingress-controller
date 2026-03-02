package comparator

// diffOp represents the type of a diff entry produced by the Myers algorithm.
type diffOp int

const (
	diffKeep   diffOp = iota // Element present in both sequences
	diffInsert               // Element present only in the new sequence
	diffDelete               // Element present only in the old sequence
)

// diffEntry represents a single entry in the Myers diff output.
type diffEntry[T any] struct {
	Op       diffOp
	OldIndex int // Index in the old sequence (-1 for inserts)
	NewIndex int // Index in the new sequence (-1 for deletes)
	Value    T
}

// editOp represents a high-level edit operation after collapsing adjacent delete+insert pairs.
type editOp int

const (
	editInsert editOp = iota
	editDelete
	editUpdate
)

// editEntry represents a collapsed edit operation with old and new values.
type editEntry[T any] struct {
	Op       editOp
	OldIndex int // Index in old sequence (for delete and update)
	NewIndex int // Index in new sequence (for insert and update)
	Old      T   // Old value (for delete and update)
	New      T   // New value (for insert and update)
}

// diffIndexedRules computes the minimal diff between two ordered slices using
// the Myers diff algorithm (O(n*d) where d = edit distance). The equal function
// determines content equality between elements.
func diffIndexedRules[T any](src, dst []T, equal func(T, T) bool) []diffEntry[T] {
	n := len(src)
	m := len(dst)

	if n == 0 && m == 0 {
		return nil
	}

	if n == 0 {
		return allInserts(dst)
	}

	if m == 0 {
		return allDeletes(src)
	}

	trace := computeTrace(src, dst, equal)
	return buildEditScript(trace, src, dst)
}

func allInserts[T any](dst []T) []diffEntry[T] {
	entries := make([]diffEntry[T], len(dst))
	for i, v := range dst {
		entries[i] = diffEntry[T]{Op: diffInsert, OldIndex: -1, NewIndex: i, Value: v}
	}
	return entries
}

func allDeletes[T any](src []T) []diffEntry[T] {
	entries := make([]diffEntry[T], len(src))
	for i, v := range src {
		entries[i] = diffEntry[T]{Op: diffDelete, OldIndex: i, NewIndex: -1, Value: v}
	}
	return entries
}

// computeTrace runs the forward phase of the Myers diff algorithm,
// returning V snapshots at each edit distance step.
func computeTrace[T any](src, dst []T, equal func(T, T) bool) [][]int {
	n := len(src)
	m := len(dst)
	maxEdits := n + m
	size := 2*maxEdits + 1

	v := make([]int, size)
	trace := make([][]int, 0, maxEdits+1)

	for d := 0; d <= maxEdits; d++ {
		found := myersStep(v, d, maxEdits, n, m, src, dst, equal)

		snapshot := make([]int, size)
		copy(snapshot, v)
		trace = append(trace, snapshot)

		if found {
			break
		}
	}

	return trace
}

// myersStep processes a single edit distance step d, updating v in place.
// Returns true when the endpoint (n, m) is reached.
func myersStep[T any](v []int, d, maxEdits, n, m int, src, dst []T, equal func(T, T) bool) bool {
	for k := -d; k <= d; k += 2 {
		var x int
		if k == -d || (k != d && v[k-1+maxEdits] < v[k+1+maxEdits]) {
			x = v[k+1+maxEdits] // move down: insert from dst
		} else {
			x = v[k-1+maxEdits] + 1 // move right: delete from src
		}

		y := x - k

		for x < n && y < m && equal(src[x], dst[y]) {
			x++
			y++
		}

		v[k+maxEdits] = x

		if x >= n && y >= m {
			return true
		}
	}

	return false
}

// buildEditScript reconstructs the edit script by backtracking through the trace.
func buildEditScript[T any](trace [][]int, src, dst []T) []diffEntry[T] {
	n := len(src)
	m := len(dst)
	maxEdits := n + m
	x := n
	y := m

	var edits []diffEntry[T]

	for d := len(trace) - 1; d > 0; d-- {
		prevV := trace[d-1]
		k := x - y

		var prevK int
		if k == -d || (k != d && prevV[k-1+maxEdits] < prevV[k+1+maxEdits]) {
			prevK = k + 1
		} else {
			prevK = k - 1
		}

		prevX := prevV[prevK+maxEdits]
		prevY := prevX - prevK

		for x > prevX && y > prevY {
			x--
			y--
			edits = append(edits, diffEntry[T]{Op: diffKeep, OldIndex: x, NewIndex: y, Value: src[x]})
		}

		if x == prevX {
			y--
			edits = append(edits, diffEntry[T]{Op: diffInsert, OldIndex: -1, NewIndex: y, Value: dst[y]})
		} else {
			x--
			edits = append(edits, diffEntry[T]{Op: diffDelete, OldIndex: x, NewIndex: -1, Value: src[x]})
		}
	}

	// At d=0, remaining moves are all diagonal (common prefix matches)
	for x > 0 && y > 0 {
		x--
		y--
		edits = append(edits, diffEntry[T]{Op: diffKeep, OldIndex: x, NewIndex: y, Value: src[x]})
	}

	// Reverse to forward order
	for i, j := 0, len(edits)-1; i < j; i, j = i+1, j-1 {
		edits[i], edits[j] = edits[j], edits[i]
	}

	return edits
}

// collapseEdits converts raw diff entries into high-level edit operations,
// collapsing adjacent DELETE+INSERT blocks into UPDATE operations.
// This detects in-place rule modifications that the Myers algorithm represents
// as a delete of the old rule followed by an insert of the new rule.
func collapseEdits[T any](diffs []diffEntry[T]) []editEntry[T] {
	var edits []editEntry[T]

	i := 0
	for i < len(diffs) {
		switch diffs[i].Op {
		case diffKeep:
			i++

		case diffInsert:
			edits = append(edits, editEntry[T]{
				Op:       editInsert,
				NewIndex: diffs[i].NewIndex,
				New:      diffs[i].Value,
			})
			i++

		case diffDelete:
			edits = collapseDeleteInsertBlock(diffs, &i, edits)
		}
	}

	return edits
}

// collapseDeleteInsertBlock handles a block of consecutive DELETEs optionally
// followed by consecutive INSERTs, pairing them into UPDATEs where possible.
func collapseDeleteInsertBlock[T any](diffs []diffEntry[T], pos *int, edits []editEntry[T]) []editEntry[T] {
	i := *pos

	// Collect consecutive DELETEs
	delStart := i
	for i < len(diffs) && diffs[i].Op == diffDelete {
		i++
	}
	deletes := diffs[delStart:i]

	// Collect consecutive INSERTs that follow
	insStart := i
	for i < len(diffs) && diffs[i].Op == diffInsert {
		i++
	}
	inserts := diffs[insStart:i]

	*pos = i

	// Pair up as UPDATEs (min of delete/insert counts)
	pairs := min(len(deletes), len(inserts))
	for j := range pairs {
		edits = append(edits, editEntry[T]{
			Op:       editUpdate,
			OldIndex: deletes[j].OldIndex,
			NewIndex: inserts[j].NewIndex,
			Old:      deletes[j].Value,
			New:      inserts[j].Value,
		})
	}

	// Remaining unpaired DELETEs
	for j := pairs; j < len(deletes); j++ {
		edits = append(edits, editEntry[T]{
			Op:       editDelete,
			OldIndex: deletes[j].OldIndex,
			Old:      deletes[j].Value,
		})
	}

	// Remaining unpaired INSERTs
	for j := pairs; j < len(inserts); j++ {
		edits = append(edits, editEntry[T]{
			Op:       editInsert,
			NewIndex: inserts[j].NewIndex,
			New:      inserts[j].Value,
		})
	}

	return edits
}
