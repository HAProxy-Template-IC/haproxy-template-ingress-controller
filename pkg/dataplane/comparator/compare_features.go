package comparator

import (
	"github.com/haproxytech/client-native/v6/models"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/comparator/sections"
)

// compareFilters compares filter configurations within a frontend or backend.
// Filters are compared by position since they don't have unique identifiers.
func (c *Comparator) compareFilters(parentType, parentName string, currentFilters, desiredFilters models.Filters) []Operation {
	create, remove, update := pickOps(parentType, sections.FilterFrontendOps, sections.FilterBackendOps)
	return compareIndexedItems(
		currentFilters, desiredFilters,
		func(a, b *models.Filter) bool { return a.Equal(*b) },
		func(f *models.Filter, i int) Operation { return create(parentName, f, i) },
		func(f *models.Filter, i int) Operation { return remove(parentName, f, i) },
		func(f *models.Filter, i int) Operation { return update(parentName, f, i) },
	)
}

// compareIndexedItems compares two slices of items by position, emitting
// create/delete/update operations. Items at positions that exist in only one
// side become create/delete operations; items present in both are updated only
// when equal returns false.
func compareIndexedItems[T any](
	current, desired []*T,
	equal func(a, b *T) bool,
	createAt func(item *T, index int) Operation,
	deleteAt func(item *T, index int) Operation,
	updateAt func(item *T, index int) Operation,
) []Operation {
	// Emit updates and creates in ascending index order, but deletes in
	// DESCENDING index order. The dataplane API's underlying config-parser
	// implements Delete(idx) by shifting every later element down one slot
	// (see haproxytech/client-native config-parser/parsers/http/http-request_generated.go's
	// `(*Requests).Delete`: it `copy(p.data[index:], p.data[index+1:])` then
	// truncates). If a delete batch is applied sequentially under a single
	// parent, ascending-order deletes cascade: Delete(N) shifts what used to
	// be at N+1 down to N, so the subsequent Delete(N+1) removes a different
	// rule than the comparator intended, and eventually we run off the end of
	// the slice.
	//
	// Descending order avoids the shift entirely: Delete(highest) only
	// shifts indices that we no longer care about (we're about to delete
	// them too — or rather, we've already accounted for them earlier in
	// the descending sequence). Updates and creates don't shift the list
	// length, so their order is unchanged.
	//
	// Emitting deletes descending keeps the operation stream correct under
	// any execution model. The current production apply replaces the whole
	// config in one raw push (structural changes never reach the API as
	// individual Delete(idx) calls), so this ordering is belt-and-suspenders
	// today — but it keeps the comparator correct for any consumer that does
	// apply operations in order.
	var ops []Operation
	var deletes []Operation
	maxLen := max(len(desired), len(current))
	for i := 0; i < maxLen; i++ {
		hasCurrent := i < len(current)
		hasDesired := i < len(desired)
		switch {
		case !hasCurrent && hasDesired:
			ops = append(ops, createAt(desired[i], i))
		case hasCurrent && !hasDesired:
			deletes = append(deletes, deleteAt(current[i], i))
		case hasCurrent && hasDesired && !equal(current[i], desired[i]):
			ops = append(ops, updateAt(desired[i], i))
		}
	}
	// Append deletes in descending index order.
	for j := len(deletes) - 1; j >= 0; j-- {
		ops = append(ops, deletes[j])
	}
	return ops
}

// compareHTTPChecks compares HTTP check configurations within a backend.
// HTTP checks are compared by position since they don't have unique identifiers.
func (c *Comparator) compareHTTPChecks(backendName string, currentChecks, desiredChecks models.HTTPChecks) []Operation {
	return compareIndexedItems(
		currentChecks, desiredChecks,
		func(a, b *models.HTTPCheck) bool { return a.Equal(*b) },
		func(check *models.HTTPCheck, i int) Operation {
			return sections.HTTPCheckBackendOps.Create(backendName, check, i)
		},
		func(check *models.HTTPCheck, i int) Operation {
			return sections.HTTPCheckBackendOps.Delete(backendName, check, i)
		},
		func(check *models.HTTPCheck, i int) Operation {
			return sections.HTTPCheckBackendOps.Update(backendName, check, i)
		},
	)
}

// compareTCPChecks compares TCP check configurations within a backend.
// TCP checks are compared by position since they don't have unique identifiers.
func (c *Comparator) compareTCPChecks(backendName string, currentChecks, desiredChecks models.TCPChecks) []Operation {
	return compareIndexedItems(
		currentChecks, desiredChecks,
		func(a, b *models.TCPCheck) bool { return a.Equal(*b) },
		func(check *models.TCPCheck, i int) Operation {
			return sections.TCPCheckBackendOps.Create(backendName, check, i)
		},
		func(check *models.TCPCheck, i int) Operation {
			return sections.TCPCheckBackendOps.Delete(backendName, check, i)
		},
		func(check *models.TCPCheck, i int) Operation {
			return sections.TCPCheckBackendOps.Update(backendName, check, i)
		},
	)
}
