package orderedset_test

import (
	"fmt"
	"math/rand/v2"
	"slices"
	"strconv"
	"sync"
	"testing"

	"gitlab.com/haproxy-haptic/haptic/pkg/incremental/internal/orderedset"
)

var testScope = orderedset.Scope{Domain: 1, Key: "test"}

func TestRootRejectsZeroAndForeignAuthority(t *testing.T) {
	left := orderedset.NewAuthority()
	right := orderedset.NewAuthority()
	root, _, err := left.Empty().Add(left, testScope, "value")
	if err != nil {
		t.Fatalf("Add() error = %v", err)
	}
	if err := root.ValidateAuthentication(left); err != nil {
		t.Fatalf("ValidateAuthentication() error = %v", err)
	}
	if err := root.ValidateAuthentication(right); err == nil {
		t.Fatal("foreign authority authenticated a root")
	}
	foreign, _, err := right.Empty().Add(right, testScope, "value")
	if err != nil {
		t.Fatalf("foreign Add() error = %v", err)
	}
	if _, err := root.SameRoot(left, testScope, foreign); err == nil {
		t.Fatal("foreign root passed identity comparison")
	}
	if err := (orderedset.Root{}).ValidateAuthentication(left); err == nil {
		t.Fatal("zero root authenticated")
	}
}

func TestRootRejectsSameAuthorityScopeSubstitution(t *testing.T) {
	authority := orderedset.NewAuthority()
	root, _, err := authority.Empty().Add(authority, testScope, "value")
	if err != nil {
		t.Fatalf("Add() error = %v", err)
	}
	foreignScope := orderedset.Scope{Domain: 1, Key: "foreign"}
	if _, err := root.Len(authority, foreignScope); err == nil {
		t.Fatal("foreign scope authenticated a root")
	}
	foreign, _, err := authority.Empty().Add(authority, foreignScope, "value")
	if err != nil {
		t.Fatalf("foreign Add() error = %v", err)
	}
	if _, err := root.SameRoot(authority, testScope, foreign); err == nil {
		t.Fatal("foreign scope root passed identity comparison")
	}
}

func TestBuildSortedMatchesPersistentInsertionAndDetachesInput(t *testing.T) {
	authority := orderedset.NewAuthority()
	values := make([]string, 2_001)
	for index := range values {
		values[index] = fmt.Sprintf("query/%06d", index)
	}
	built, err := orderedset.BuildSorted(authority, testScope, values)
	if err != nil {
		t.Fatalf("BuildSorted() error = %v", err)
	}
	persistent := authority.Empty()
	for _, value := range values {
		persistent, _, err = persistent.Add(authority, testScope, value)
		if err != nil {
			t.Fatalf("Add(%q) error = %v", value, err)
		}
	}
	want := append([]string(nil), values...)
	values[0] = "poison"
	assertValues(t, built, authority, want)
	assertValues(t, persistent, authority, want)

	next, changed, err := built.Delete(authority, testScope, want[len(want)/2])
	if err != nil || !changed {
		t.Fatalf("Delete() = changed %t, error %v", changed, err)
	}
	next, changed, err = next.Add(authority, testScope, "query/new")
	if err != nil || !changed {
		t.Fatalf("Add() = changed %t, error %v", changed, err)
	}
	if _, err := next.Values(authority, testScope); err != nil {
		t.Fatalf("Values() after mutation error = %v", err)
	}
	assertValues(t, built, authority, want)
}

func TestBuildPackedSortedRetainsDetachedImmutableSnapshots(t *testing.T) {
	authority := orderedset.NewAuthority()
	values := []string{"alpha", "bravo", "delta"}
	root, err := orderedset.BuildPackedSorted(authority, testScope, values)
	if err != nil {
		t.Fatalf("BuildPackedSorted() error = %v", err)
	}
	values[0] = "poison"
	assertValues(t, root, authority, []string{"alpha", "bravo", "delta"})

	next, changed, err := root.Add(authority, testScope, "charlie")
	if err != nil || !changed {
		t.Fatalf("Add() = changed %t, error %v", changed, err)
	}
	next, changed, err = next.Delete(authority, testScope, "bravo")
	if err != nil || !changed {
		t.Fatalf("Delete() = changed %t, error %v", changed, err)
	}
	assertValues(t, root, authority, []string{"alpha", "bravo", "delta"})
	assertValues(t, next, authority, []string{"alpha", "charlie", "delta"})

	foreignScope := orderedset.Scope{Domain: testScope.Domain, Key: "foreign"}
	if _, err := root.Values(authority, foreignScope); err == nil {
		t.Fatal("foreign scope authenticated a packed root")
	}
}

func TestBuildSortedRejectsMalformedInputs(t *testing.T) {
	authority := orderedset.NewAuthority()
	tests := []struct {
		name      string
		authority *orderedset.Authority
		scope     orderedset.Scope
		values    []string
	}{
		{name: "nil authority", scope: testScope, values: []string{"value"}},
		{name: "zero scope", authority: authority, values: []string{"value"}},
		{name: "empty value", authority: authority, scope: testScope, values: []string{""}},
		{name: "duplicate", authority: authority, scope: testScope, values: []string{"a", "a"}},
		{name: "descending", authority: authority, scope: testScope, values: []string{"b", "a"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := orderedset.BuildSorted(test.authority, test.scope, test.values); err == nil {
				t.Fatal("BuildSorted() accepted malformed input")
			}
		})
	}
}

func TestBuildSortedEmptyUsesCanonicalRoot(t *testing.T) {
	authority := orderedset.NewAuthority()
	built, err := orderedset.BuildSorted(authority, testScope, nil)
	if err != nil {
		t.Fatalf("BuildSorted() error = %v", err)
	}
	same, err := built.SameRoot(authority, testScope, authority.Empty())
	if err != nil || !same {
		t.Fatalf("empty root identity = %t, error %v", same, err)
	}
}

func TestRootRetainsImmutableSnapshotsAndDetachedValues(t *testing.T) {
	authority := orderedset.NewAuthority()
	root := authority.Empty()
	for _, value := range []string{"delta", "alpha", "charlie", "bravo"} {
		var err error
		root, _, err = root.Add(authority, testScope, value)
		if err != nil {
			t.Fatalf("Add(%q) error = %v", value, err)
		}
	}
	retained := root
	values, err := root.Values(authority, testScope)
	if err != nil {
		t.Fatalf("Values() error = %v", err)
	}
	values[0] = "poison"

	next, changed, err := root.Delete(authority, testScope, "charlie")
	if err != nil || !changed {
		t.Fatalf("Delete() = changed %t, error %v", changed, err)
	}
	next, changed, err = next.Add(authority, testScope, "echo")
	if err != nil || !changed {
		t.Fatalf("Add() = changed %t, error %v", changed, err)
	}
	assertValues(t, retained, authority, []string{"alpha", "bravo", "charlie", "delta"})
	assertValues(t, next, authority, []string{"alpha", "bravo", "delta", "echo"})

	unchanged, changed, err := retained.Add(authority, testScope, "alpha")
	if err != nil || changed {
		t.Fatalf("idempotent Add() = changed %t, error %v", changed, err)
	}
	same, err := retained.SameRoot(authority, testScope, unchanged)
	if err != nil || !same {
		t.Fatalf("idempotent Add() root identity = %t, error %v", same, err)
	}
	unchanged, changed, err = retained.Delete(authority, testScope, "absent")
	if err != nil || changed {
		t.Fatalf("idempotent Delete() = changed %t, error %v", changed, err)
	}
	same, err = retained.SameRoot(authority, testScope, unchanged)
	if err != nil || !same {
		t.Fatalf("idempotent Delete() root identity = %t, error %v", same, err)
	}
}

func TestRootRandomizedDifferential(t *testing.T) {
	authority := orderedset.NewAuthority()
	root := authority.Empty()
	oracle := map[string]struct{}{}
	type snapshot struct {
		root   orderedset.Root
		values []string
	}
	retained := make([]snapshot, 0, 32)
	random := rand.New(rand.NewPCG(0x187, 0x5eed))
	for operation := range 20_000 {
		value := "query/" + strconv.Itoa(random.IntN(2_000))
		var changed bool
		var err error
		if random.IntN(2) == 0 {
			_, existed := oracle[value]
			root, changed, err = root.Add(authority, testScope, value)
			if changed == existed {
				t.Fatalf("Add(%q) changed = %t, existed = %t", value, changed, existed)
			}
			oracle[value] = struct{}{}
		} else {
			_, existed := oracle[value]
			root, changed, err = root.Delete(authority, testScope, value)
			if changed != existed {
				t.Fatalf("Delete(%q) changed = %t, existed = %t", value, changed, existed)
			}
			delete(oracle, value)
		}
		if err != nil {
			t.Fatalf("operation %d error = %v", operation, err)
		}
		if operation%641 == 0 {
			retained = append(retained, snapshot{root: root, values: sortedOracle(oracle)})
		}
		if operation%97 == 0 {
			assertValues(t, root, authority, sortedOracle(oracle))
		}
	}
	assertValues(t, root, authority, sortedOracle(oracle))
	for _, saved := range retained {
		assertValues(t, saved.root, authority, saved.values)
	}
}

func TestRootSupportsConcurrentRetainedReaders(t *testing.T) {
	authority := orderedset.NewAuthority()
	root := authority.Empty()
	for index := range 1_000 {
		var err error
		root, _, err = root.Add(authority, testScope, strconv.Itoa(index))
		if err != nil {
			t.Fatalf("Add(%d) error = %v", index, err)
		}
	}
	retained := root
	var wait sync.WaitGroup
	for range 8 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			for range 100 {
				if _, err := retained.Contains(authority, testScope, "500"); err != nil {
					t.Errorf("Contains() error = %v", err)
					return
				}
				if _, err := retained.Values(authority, testScope); err != nil {
					t.Errorf("Values() error = %v", err)
					return
				}
			}
		}()
	}
	for index := 1_000; index < 2_000; index++ {
		var err error
		root, _, err = root.Add(authority, testScope, strconv.Itoa(index))
		if err != nil {
			t.Fatalf("Add() error = %v", err)
		}
	}
	wait.Wait()
	assertValues(t, retained, authority, sortedRange(1_000))
}

func assertValues(t *testing.T, root orderedset.Root, authority *orderedset.Authority, want []string) {
	t.Helper()
	got, err := root.Values(authority, testScope)
	if err != nil {
		t.Fatalf("Values() error = %v", err)
	}
	if !slices.Equal(got, want) {
		t.Fatalf("Values() = %#v, want %#v", got, want)
	}
	length, err := root.Len(authority, testScope)
	if err != nil {
		t.Fatalf("Len() error = %v", err)
	}
	if length != len(want) {
		t.Fatalf("Len() = %d, want %d", length, len(want))
	}
	for _, value := range want {
		found, err := root.Contains(authority, testScope, value)
		if err != nil || !found {
			t.Fatalf("Contains(%q) = %t, error %v", value, found, err)
		}
	}
}

func sortedOracle(oracle map[string]struct{}) []string {
	values := make([]string, 0, len(oracle))
	for value := range oracle {
		values = append(values, value)
	}
	slices.Sort(values)
	return values
}

func sortedRange(size int) []string {
	values := make([]string, size)
	for index := range size {
		values[index] = strconv.Itoa(index)
	}
	slices.Sort(values)
	return values
}
