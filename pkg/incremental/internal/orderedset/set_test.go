package orderedset

import (
	"fmt"
	"strconv"
	"testing"
)

func TestSequentialInsertMaintainsTreeInvariants(t *testing.T) {
	authority := NewAuthority()
	scope := Scope{Domain: 1, Key: "test"}
	root := authority.Empty()
	for index := range 1_000 {
		var err error
		root, _, err = root.Add(authority, scope, strconv.Itoa(index))
		if err != nil {
			t.Fatalf("Add(%d) error = %v", index, err)
		}
		if err := validateTree(authority, root.state.node); err != nil {
			t.Fatalf("tree after Add(%d): %v", index, err)
		}
	}
}

func TestBuildSortedMaintainsTreeInvariants(t *testing.T) {
	authority := NewAuthority()
	scope := Scope{Domain: 1, Key: "test"}
	for _, size := range []int{1, 2, 3, 4, 31, 32, 33, 1_000} {
		values := make([]string, size)
		for index := range values {
			values[index] = fmt.Sprintf("%06d", index)
		}
		root, err := BuildSorted(authority, scope, values)
		if err != nil {
			t.Fatalf("BuildSorted(%d) error = %v", size, err)
		}
		if err := validateTree(authority, root.state.node); err != nil {
			t.Fatalf("tree after BuildSorted(%d): %v", size, err)
		}
	}
}

func validateTree(authority *Authority, current *node) error {
	if current == nil {
		return nil
	}
	if err := validateNode(authority, current); err != nil {
		return &treeValidationError{key: current.key, err: err}
	}
	if err := validateTree(authority, current.left); err != nil {
		return err
	}
	return validateTree(authority, current.right)
}

type treeValidationError struct {
	key string
	err error
}

func (e *treeValidationError) Error() string {
	return "node " + e.key + ": " + e.err.Error()
}
