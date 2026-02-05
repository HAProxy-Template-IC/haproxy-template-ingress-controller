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
	maxLen := len(currentFilters)
	if len(desiredFilters) > maxLen {
		maxLen = len(desiredFilters)
	}

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

// compareHTTPChecks compares HTTP check configurations within a backend.
// HTTP checks are compared by position since they don't have unique identifiers.
func (c *Comparator) compareHTTPChecks(backendName string, currentChecks, desiredChecks models.HTTPChecks) []Operation {
	var operations []Operation

	// Compare checks by position
	maxLen := len(currentChecks)
	if len(desiredChecks) > maxLen {
		maxLen = len(desiredChecks)
	}

	for i := 0; i < maxLen; i++ {
		hasCurrentCheck := i < len(currentChecks)
		hasDesiredCheck := i < len(desiredChecks)

		if !hasCurrentCheck && hasDesiredCheck {
			// Check added at this position
			check := desiredChecks[i]
			operations = append(operations, sections.NewHTTPCheckBackendCreate(backendName, check, i))
		} else if hasCurrentCheck && !hasDesiredCheck {
			// Check removed at this position
			check := currentChecks[i]
			operations = append(operations, sections.NewHTTPCheckBackendDelete(backendName, check, i))
		} else if hasCurrentCheck && hasDesiredCheck {
			// Both exist - check if modified
			currentCheck := currentChecks[i]
			desiredCheck := desiredChecks[i]

			if !currentCheck.Equal(*desiredCheck) {
				operations = append(operations, sections.NewHTTPCheckBackendUpdate(backendName, desiredCheck, i))
			}
		}
	}

	return operations
}

// compareTCPChecks compares TCP check configurations within a backend.
// TCP checks are compared by position since they don't have unique identifiers.
func (c *Comparator) compareTCPChecks(backendName string, currentChecks, desiredChecks models.TCPChecks) []Operation {
	var operations []Operation

	// Compare checks by position
	maxLen := len(currentChecks)
	if len(desiredChecks) > maxLen {
		maxLen = len(desiredChecks)
	}

	for i := 0; i < maxLen; i++ {
		hasCurrentCheck := i < len(currentChecks)
		hasDesiredCheck := i < len(desiredChecks)

		if !hasCurrentCheck && hasDesiredCheck {
			// Check added at this position
			check := desiredChecks[i]
			operations = append(operations, sections.NewTCPCheckBackendCreate(backendName, check, i))
		} else if hasCurrentCheck && !hasDesiredCheck {
			// Check removed at this position
			check := currentChecks[i]
			operations = append(operations, sections.NewTCPCheckBackendDelete(backendName, check, i))
		} else if hasCurrentCheck && hasDesiredCheck {
			// Both exist - check if modified
			currentCheck := currentChecks[i]
			desiredCheck := desiredChecks[i]

			if !currentCheck.Equal(*desiredCheck) {
				operations = append(operations, sections.NewTCPCheckBackendUpdate(backendName, desiredCheck, i))
			}
		}
	}

	return operations
}

// serverTemplatesEqual checks if two server templates are equal.
// Uses the HAProxy models' built-in Equal() method to compare ALL attributes.
func serverTemplatesEqual(t1, t2 *models.ServerTemplate) bool {
	return t1.Equal(*t2)
}
