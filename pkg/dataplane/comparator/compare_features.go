package comparator

import (
	"github.com/haproxytech/client-native/v6/models"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/comparator/sections"
)

// compareFilters compares filter configurations within a frontend or backend.
// Filters are compared by position since they don't have unique identifiers.
func (c *Comparator) compareFilters(parentType, parentName string, currentFilters, desiredFilters models.Filters) []Operation {
	create, remove, update := sections.NewFilterBackendCreate, sections.NewFilterBackendDelete, sections.NewFilterBackendUpdate
	if parentType == parentTypeFrontend {
		create, remove, update = sections.NewFilterFrontendCreate, sections.NewFilterFrontendDelete, sections.NewFilterFrontendUpdate
	}
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
	var operations []Operation
	maxLen := max(len(desired), len(current))
	for i := 0; i < maxLen; i++ {
		hasCurrent := i < len(current)
		hasDesired := i < len(desired)
		switch {
		case !hasCurrent && hasDesired:
			operations = append(operations, createAt(desired[i], i))
		case hasCurrent && !hasDesired:
			operations = append(operations, deleteAt(current[i], i))
		case hasCurrent && hasDesired && !equal(current[i], desired[i]):
			operations = append(operations, updateAt(desired[i], i))
		}
	}
	return operations
}

// compareHTTPChecks compares HTTP check configurations within a backend.
// HTTP checks are compared by position since they don't have unique identifiers.
func (c *Comparator) compareHTTPChecks(backendName string, currentChecks, desiredChecks models.HTTPChecks) []Operation {
	return compareIndexedItems(
		currentChecks, desiredChecks,
		func(a, b *models.HTTPCheck) bool { return a.Equal(*b) },
		func(check *models.HTTPCheck, i int) Operation {
			return sections.NewHTTPCheckBackendCreate(backendName, check, i)
		},
		func(check *models.HTTPCheck, i int) Operation {
			return sections.NewHTTPCheckBackendDelete(backendName, check, i)
		},
		func(check *models.HTTPCheck, i int) Operation {
			return sections.NewHTTPCheckBackendUpdate(backendName, check, i)
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
			return sections.NewTCPCheckBackendCreate(backendName, check, i)
		},
		func(check *models.TCPCheck, i int) Operation {
			return sections.NewTCPCheckBackendDelete(backendName, check, i)
		},
		func(check *models.TCPCheck, i int) Operation {
			return sections.NewTCPCheckBackendUpdate(backendName, check, i)
		},
	)
}
