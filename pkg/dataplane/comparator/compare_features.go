package comparator

import (
	"github.com/haproxytech/client-native/v6/models"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/comparator/sections"
)

// compareFilters compares filter configurations within a frontend or backend.
// Filters are compared by position since they don't have unique identifiers.
func (c *Comparator) compareFilters(parentType, parentName string, currentFilters, desiredFilters models.Filters) []Operation {
	var operations []Operation

	// Compare filters by position
	maxLen := max(len(desiredFilters), len(currentFilters))

	for i := 0; i < maxLen; i++ {
		hasCurrentFilter := i < len(currentFilters)
		hasDesiredFilter := i < len(desiredFilters)

		if !hasCurrentFilter && hasDesiredFilter {
			ops := c.createFilterOperation(parentType, parentName, desiredFilters[i], i)
			operations = append(operations, ops...)
		} else if hasCurrentFilter && !hasDesiredFilter {
			ops := c.deleteFilterOperation(parentType, parentName, currentFilters[i], i)
			operations = append(operations, ops...)
		} else if hasCurrentFilter && hasDesiredFilter {
			ops := c.updateFilterOperation(parentType, parentName, currentFilters[i], desiredFilters[i], i)
			operations = append(operations, ops...)
		}
	}

	return operations
}

func (c *Comparator) createFilterOperation(parentType, parentName string, filter *models.Filter, index int) []Operation {
	if parentType == parentTypeFrontend {
		return []Operation{sections.NewFilterFrontendCreate(parentName, filter, index)}
	}
	return []Operation{sections.NewFilterBackendCreate(parentName, filter, index)}
}

func (c *Comparator) deleteFilterOperation(parentType, parentName string, filter *models.Filter, index int) []Operation {
	if parentType == parentTypeFrontend {
		return []Operation{sections.NewFilterFrontendDelete(parentName, filter, index)}
	}
	return []Operation{sections.NewFilterBackendDelete(parentName, filter, index)}
}

func (c *Comparator) updateFilterOperation(parentType, parentName string, currentFilter, desiredFilter *models.Filter, index int) []Operation {
	if !currentFilter.Equal(*desiredFilter) {
		if parentType == parentTypeFrontend {
			return []Operation{sections.NewFilterFrontendUpdate(parentName, desiredFilter, index)}
		}
		return []Operation{sections.NewFilterBackendUpdate(parentName, desiredFilter, index)}
	}
	return nil
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
