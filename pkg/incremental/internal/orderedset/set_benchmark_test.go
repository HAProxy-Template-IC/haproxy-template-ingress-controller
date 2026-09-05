package orderedset_test

import (
	"fmt"
	"strconv"
	"testing"

	"gitlab.com/haproxy-haptic/haptic/pkg/incremental/internal/orderedset"
)

var benchmarkRootSink orderedset.Root
var benchmarkLengthSink int
var benchmarkScope = orderedset.Scope{Domain: 1, Key: "benchmark"}

func BenchmarkRootCardinality(b *testing.B) {
	for _, size := range []int{1, 1_000, 100_000} {
		b.Run(strconv.Itoa(size), func(b *testing.B) {
			authority, root := benchmarkRoot(b, size)
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				length, err := root.Len(authority, benchmarkScope)
				if err != nil {
					b.Fatal(err)
				}
				benchmarkLengthSink = length
			}
		})
	}
}

func BenchmarkRootOneEdgeTransition(b *testing.B) {
	for _, size := range []int{1, 1_000, 100_000} {
		authority, root := benchmarkRoot(b, size)
		b.Run(fmt.Sprintf("%d/add", size), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				next, changed, err := root.Add(authority, benchmarkScope, "query/new")
				if err != nil || !changed {
					b.Fatalf("Add() = changed %t, error %v", changed, err)
				}
				benchmarkRootSink = next
			}
		})
		b.Run(fmt.Sprintf("%d/delete", size), func(b *testing.B) {
			value := fmt.Sprintf("query/%06d", size/2)
			b.ReportAllocs()
			for b.Loop() {
				next, changed, err := root.Delete(authority, benchmarkScope, value)
				if err != nil || !changed {
					b.Fatalf("Delete() = changed %t, error %v", changed, err)
				}
				benchmarkRootSink = next
			}
		})
	}
}

func benchmarkPersistentRootConstruction(b *testing.B, values []string) {
	b.Helper()
	authority := orderedset.NewAuthority()
	b.ReportAllocs()
	for b.Loop() {
		root := authority.Empty()
		for _, value := range values {
			var err error
			root, _, err = root.Add(authority, benchmarkScope, value)
			if err != nil {
				b.Fatal(err)
			}
		}
		benchmarkRootSink = root
	}
}

func BenchmarkRootConstruction(b *testing.B) {
	for _, size := range []int{14, 42_012} {
		values := make([]string, size)
		for index := range values {
			values[index] = fmt.Sprintf("query/%06d", index)
		}
		b.Run(fmt.Sprintf("%d/persistent", size), func(b *testing.B) {
			benchmarkPersistentRootConstruction(b, values)
		})
		b.Run(fmt.Sprintf("%d/bulk-sorted", size), func(b *testing.B) {
			authority := orderedset.NewAuthority()
			b.ReportAllocs()
			for b.Loop() {
				root, err := orderedset.BuildSorted(authority, benchmarkScope, values)
				if err != nil {
					b.Fatal(err)
				}
				benchmarkRootSink = root
			}
		})
	}
}

func benchmarkRoot(b *testing.B, size int) (*orderedset.Authority, orderedset.Root) {
	b.Helper()
	authority := orderedset.NewAuthority()
	root := authority.Empty()
	for index := range size {
		var err error
		root, _, err = root.Add(authority, benchmarkScope, fmt.Sprintf("query/%06d", index))
		if err != nil {
			b.Fatalf("Add() error = %v", err)
		}
	}
	return authority, root
}
