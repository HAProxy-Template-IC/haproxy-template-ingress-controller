package incremental

import (
	"cmp"
	"fmt"
	"slices"
	"testing"

	"gitlab.com/haproxy-haptic/haptic/pkg/incremental/internal/orderedset"
)

var benchmarkReverseChanges map[dependencyKey]reverseSetChange
var benchmarkLegacyReverse map[QueryKey]struct{}
var benchmarkReplacementReverse map[dependencyKey]orderedset.Root

func BenchmarkColdReplacementReverseBuild(b *testing.B) {
	const queryCount = 42_012
	const dependentsPerInput = 14
	nodes := benchmarkReplacementNodes(queryCount, dependentsPerInput)
	removed := map[QueryKey]struct{}{}
	authority := orderedset.NewAuthority()
	b.Run("legacy-two-persistent-builds", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			roots, err := benchmarkLegacyColdReverseBuild(authority, nodes)
			if err != nil {
				b.Fatal(err)
			}
			benchmarkReplacementReverse = roots
		}
	})
	b.Run("flat-authenticated-builder", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			roots, err := buildReplacementReverseRoots(authority, nodes, removed)
			if err != nil {
				b.Fatal(err)
			}
			benchmarkReplacementReverse = roots
		}
	})
	b.Run("sorted-edge-authenticated-builder", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			roots, err := benchmarkSortedEdgeReplacementReverseBuild(authority, nodes, removed)
			if err != nil {
				b.Fatal(err)
			}
			benchmarkReplacementReverse = roots
		}
	})
}

type benchmarkReplacementReverseEdge struct {
	dependency dependencyKey
	dependent  QueryKey
}

func benchmarkSortedEdgeReplacementReverseBuild(
	authority *orderedset.Authority,
	nodes map[QueryKey]nodeEntry,
	removed map[QueryKey]struct{},
) (map[dependencyKey]orderedset.Root, error) {
	edges := make([]benchmarkReplacementReverseEdge, 0, len(nodes))
	for key, entry := range nodes {
		if _, isRemoved := removed[key]; isRemoved {
			continue
		}
		for _, dependency := range entry.deps {
			edges = append(edges, benchmarkReplacementReverseEdge{
				dependency: dependency.key,
				dependent:  key,
			})
		}
	}
	slices.SortFunc(edges, func(left, right benchmarkReplacementReverseEdge) int {
		if comparison := compareDependencyKeys(left.dependency, right.dependency); comparison != 0 {
			return comparison
		}
		return cmp.Compare(left.dependent.value, right.dependent.value)
	})
	roots := make(map[dependencyKey]orderedset.Root)
	values := make([]string, 0)
	for start := 0; start < len(edges); {
		end := start + 1
		for end < len(edges) && edges[end].dependency == edges[start].dependency {
			end++
		}
		values = values[:0]
		for index := start; index < end; index++ {
			values = append(values, edges[index].dependent.value)
		}
		root, err := orderedset.BuildSorted(authority, reverseScope(edges[start].dependency), values)
		if err != nil {
			return nil, err
		}
		roots[edges[start].dependency] = root
		start = end
	}
	return roots, nil
}

func benchmarkLegacyColdReverseBuild(
	authority *orderedset.Authority,
	nodes map[QueryKey]nodeEntry,
) (map[dependencyKey]orderedset.Root, error) {
	var result map[dependencyKey]orderedset.Root
	for range 2 {
		reverse := map[dependencyKey]orderedset.Root{}
		for _, key := range sortedNodeEntryKeys(nodes) {
			if err := addReverseEdges(authority, reverse, key, nodes[key].deps); err != nil {
				return nil, err
			}
		}
		if len(reverse) == 0 {
			return nil, fmt.Errorf("legacy reverse build is empty")
		}
		result = reverse
	}
	return result, nil
}

func benchmarkReplacementNodes(queryCount, dependentsPerInput int) map[QueryKey]nodeEntry {
	nodes := make(map[QueryKey]nodeEntry, queryCount)
	for index := range queryCount {
		key := NewQueryKey(fmt.Sprintf("query/%06d", index))
		dependencyKey := inputDep(NewInputKey(fmt.Sprintf("input/%06d", index/dependentsPerInput)))
		nodes[key] = nodeEntry{deps: []dependency{{key: dependencyKey}}}
	}
	return nodes
}

func BenchmarkSharedFanoutReverseTransition(b *testing.B) {
	for _, size := range []int{1, 1_000, 100_000} {
		graph, shared, alternate, dependent := benchmarkReverseGraph(b, size)
		previous := []dependency{{key: shared}}
		next := []dependency{{key: alternate}}
		b.Run(fmt.Sprintf("%d/persistent", size), func(b *testing.B) {
			benchmarkPersistentFanoutTransition(b, graph, dependent, previous, next)
		})
		legacy := make(map[QueryKey]struct{}, size)
		for index := range size {
			legacy[NewQueryKey(fmt.Sprintf("query/%06d", index))] = struct{}{}
		}
		b.Run(fmt.Sprintf("%d/legacy-full-clone", size), func(b *testing.B) {
			benchmarkLegacyFanoutTransition(b, legacy, dependent)
		})
	}
}

func benchmarkPersistentFanoutTransition(
	b *testing.B,
	graph *Graph,
	dependent QueryKey,
	previous []dependency,
	next []dependency,
) {
	b.Helper()
	b.ReportAllocs()
	for b.Loop() {
		editor := reverseSetEditor{graph: graph, roots: map[dependencyKey]orderedset.Root{}}
		if err := editor.replace(dependent, previous, next); err != nil {
			b.Fatal(err)
		}
		changes, err := editor.changes()
		if err != nil || len(changes) != 2 {
			b.Fatalf("changes = %d, error %v", len(changes), err)
		}
		benchmarkReverseChanges = changes
	}
}

func benchmarkLegacyFanoutTransition(
	b *testing.B,
	legacy map[QueryKey]struct{},
	dependent QueryKey,
) {
	b.Helper()
	b.ReportAllocs()
	for b.Loop() {
		cloned := make(map[QueryKey]struct{}, len(legacy))
		for key := range legacy {
			cloned[key] = struct{}{}
		}
		delete(cloned, dependent)
		benchmarkLegacyReverse = cloned
	}
}

func benchmarkReverseGraph(
	b *testing.B,
	size int,
) (graph *Graph, shared, alternate dependencyKey, dependent QueryKey) {
	b.Helper()
	graph, err := New()
	if err != nil {
		b.Fatalf("New() error = %v", err)
	}
	shared = inputDep(NewInputKey("shared"))
	alternate = inputDep(NewInputKey("alternate"))
	root := graph.reverseAuthority.Empty()
	for index := range size {
		var changed bool
		root, changed, err = root.Add(
			graph.reverseAuthority,
			reverseScope(shared),
			fmt.Sprintf("query/%06d", index),
		)
		if err != nil || !changed {
			b.Fatalf("Add(%d) = changed %t, error %v", index, changed, err)
		}
	}
	reverse, _, _ := graph.current.reverse.Insert([]byte(dependencyTreeKey(shared)), root)
	next, err := newGraphGenerationFromTrees(
		graph,
		graph.current.number,
		graph.current.inputs,
		graph.current.nodes,
		reverse,
		graph.current.dirty,
		graph.current.counters,
	)
	if err != nil {
		b.Fatalf("newGraphGenerationFromTrees() error = %v", err)
	}
	graph.installGenerationLocked(next)
	return graph, shared, alternate, NewQueryKey(fmt.Sprintf("query/%06d", 0))
}
