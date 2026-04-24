package comparator

import (
	"slices"

	"github.com/haproxytech/client-native/v6/models"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/comparator/sections"
)

// compareACLs compares ACL configurations within a frontend or backend.
// ACLs are identified by their name (ACLName field).
func (c *Comparator) compareACLs(parentType, parentName string, currentACLs, desiredACLs models.Acls, _ *DiffSummary) []Operation {
	// Build maps for easier comparison using ACL names
	currentACLMap := make(map[string]int) // name -> index
	for i, acl := range currentACLs {
		if acl.ACLName != "" {
			currentACLMap[acl.ACLName] = i
		}
	}

	desiredACLMap := make(map[string]int) // name -> index
	for i, acl := range desiredACLs {
		if acl.ACLName != "" {
			desiredACLMap[acl.ACLName] = i
		}
	}

	// Find added ACLs
	addedOps := c.compareAddedACLs(parentType, parentName, desiredACLMap, currentACLMap, desiredACLs)
	// Find deleted ACLs
	deletedOps := c.compareDeletedACLs(parentType, parentName, currentACLMap, desiredACLMap, currentACLs)
	// Find modified ACLs
	modifiedOps := c.compareModifiedACLs(parentType, parentName, desiredACLMap, currentACLMap, currentACLs, desiredACLs)

	operations := make([]Operation, 0, len(addedOps)+len(deletedOps)+len(modifiedOps))
	operations = append(operations, addedOps...)
	operations = append(operations, deletedOps...)
	operations = append(operations, modifiedOps...)

	return operations
}

// compareAddedACLs compares added ACLs and creates operations for them.
// Operations are sorted by index to ensure correct insertion order (index 0 before 1, etc.)
// since the DataPlane API requires indices to be valid at insertion time.
func (c *Comparator) compareAddedACLs(parentType, parentName string, desiredACLMap, currentACLMap map[string]int, desiredACLs models.Acls) []Operation {
	// Collect indices of ACLs to add, then sort them
	// This is necessary because map iteration order is not guaranteed,
	// but the DataPlane API requires ACLs to be created in index order
	// (can't create index 2 before index 0 exists)
	var indicesToAdd []int
	for name, idx := range desiredACLMap {
		if _, exists := currentACLMap[name]; !exists {
			indicesToAdd = append(indicesToAdd, idx)
		}
	}

	// Sort indices to ensure correct insertion order
	slices.Sort(indicesToAdd)

	// Create operations in sorted index order
	var operations []Operation
	for _, idx := range indicesToAdd {
		acl := desiredACLs[idx]
		if parentType == parentTypeFrontend {
			operations = append(operations, sections.NewACLFrontendCreate(parentName, acl, idx))
		} else {
			operations = append(operations, sections.NewACLBackendCreate(parentName, acl, idx))
		}
	}

	return operations
}

// compareDeletedACLs compares deleted ACLs and creates operations for them.
func (c *Comparator) compareDeletedACLs(parentType, parentName string, currentACLMap, desiredACLMap map[string]int, currentACLs models.Acls) []Operation {
	var operations []Operation

	for name, idx := range currentACLMap {
		if _, exists := desiredACLMap[name]; !exists {
			acl := currentACLs[idx]
			if parentType == parentTypeFrontend {
				operations = append(operations, sections.NewACLFrontendDelete(parentName, acl, idx))
			} else {
				operations = append(operations, sections.NewACLBackendDelete(parentName, acl, idx))
			}
		}
	}

	return operations
}

// compareModifiedACLs compares modified ACLs and creates operations for them.
func (c *Comparator) compareModifiedACLs(parentType, parentName string, desiredACLMap, currentACLMap map[string]int, currentACLs, desiredACLs models.Acls) []Operation {
	var operations []Operation

	for name, desiredIdx := range desiredACLMap {
		if currentIdx, exists := currentACLMap[name]; exists {
			currentACL := currentACLs[currentIdx]
			desiredACL := desiredACLs[desiredIdx]

			// Compare using built-in Equal() method
			if !currentACL.Equal(*desiredACL) {
				if parentType == parentTypeFrontend {
					operations = append(operations, sections.NewACLFrontendUpdate(parentName, desiredACL, desiredIdx))
				} else {
					operations = append(operations, sections.NewACLBackendUpdate(parentName, desiredACL, desiredIdx))
				}
			}
		}
	}

	return operations
}

// compareEditedItems runs an LCS-based diff (diffIndexedRules +
// collapseEdits) over current/desired and emits create/delete/update
// operations for the resulting edit script. Updates use the new value at the
// position of the old item, matching what collapseEdits already pairs up.
func compareEditedItems[T any](
	current, desired []T,
	equal func(T, T) bool,
	create func(item T, index int) Operation,
	remove func(item T, index int) Operation,
	update func(item T, index int) Operation,
) []Operation {
	diffs := diffIndexedRules(current, desired, equal)
	edits := collapseEdits(diffs)

	var operations []Operation
	for _, e := range edits {
		switch e.Op {
		case editInsert:
			operations = append(operations, create(e.New, e.NewIndex))
		case editDelete:
			operations = append(operations, remove(e.Old, e.OldIndex))
		case editUpdate:
			operations = append(operations, update(e.New, e.OldIndex))
		}
	}
	return operations
}

// compareHTTPRequestRules compares HTTP request rule configurations within a frontend or backend.
// Uses LCS-based content matching to produce minimal INSERT/DELETE/UPDATE operations
// instead of cascading UPDATEs caused by index shifts.
func (c *Comparator) compareHTTPRequestRules(parentType, parentName string, currentRules, desiredRules models.HTTPRequestRules) []Operation {
	create, remove, update := sections.NewHTTPRequestRuleBackendCreate, sections.NewHTTPRequestRuleBackendDelete, sections.NewHTTPRequestRuleBackendUpdate
	if parentType == parentTypeFrontend {
		create, remove, update = sections.NewHTTPRequestRuleFrontendCreate, sections.NewHTTPRequestRuleFrontendDelete, sections.NewHTTPRequestRuleFrontendUpdate
	}
	return compareEditedItems(
		currentRules, desiredRules,
		func(a, b *models.HTTPRequestRule) bool { return a.Equal(*b) },
		func(r *models.HTTPRequestRule, i int) Operation { return create(parentName, r, i) },
		func(r *models.HTTPRequestRule, i int) Operation { return remove(parentName, r, i) },
		func(r *models.HTTPRequestRule, i int) Operation { return update(parentName, r, i) },
	)
}

// compareHTTPResponseRules compares HTTP response rule configurations within a frontend or backend.
// Uses LCS-based content matching to produce minimal INSERT/DELETE/UPDATE operations.
func (c *Comparator) compareHTTPResponseRules(parentType, parentName string, currentRules, desiredRules models.HTTPResponseRules) []Operation {
	create, remove, update := sections.NewHTTPResponseRuleBackendCreate, sections.NewHTTPResponseRuleBackendDelete, sections.NewHTTPResponseRuleBackendUpdate
	if parentType == parentTypeFrontend {
		create, remove, update = sections.NewHTTPResponseRuleFrontendCreate, sections.NewHTTPResponseRuleFrontendDelete, sections.NewHTTPResponseRuleFrontendUpdate
	}
	return compareEditedItems(
		currentRules, desiredRules,
		func(a, b *models.HTTPResponseRule) bool { return a.Equal(*b) },
		func(r *models.HTTPResponseRule, i int) Operation { return create(parentName, r, i) },
		func(r *models.HTTPResponseRule, i int) Operation { return remove(parentName, r, i) },
		func(r *models.HTTPResponseRule, i int) Operation { return update(parentName, r, i) },
	)
}

// compareTCPRequestRules compares TCP request rule configurations within a frontend or backend.
// Uses LCS-based content matching to produce minimal INSERT/DELETE/UPDATE operations.
func (c *Comparator) compareTCPRequestRules(parentType, parentName string, currentRules, desiredRules models.TCPRequestRules) []Operation {
	create, remove, update := sections.NewTCPRequestRuleBackendCreate, sections.NewTCPRequestRuleBackendDelete, sections.NewTCPRequestRuleBackendUpdate
	if parentType == parentTypeFrontend {
		create, remove, update = sections.NewTCPRequestRuleFrontendCreate, sections.NewTCPRequestRuleFrontendDelete, sections.NewTCPRequestRuleFrontendUpdate
	}
	return compareEditedItems(
		currentRules, desiredRules,
		func(a, b *models.TCPRequestRule) bool { return a.Equal(*b) },
		func(r *models.TCPRequestRule, i int) Operation { return create(parentName, r, i) },
		func(r *models.TCPRequestRule, i int) Operation { return remove(parentName, r, i) },
		func(r *models.TCPRequestRule, i int) Operation { return update(parentName, r, i) },
	)
}

// compareTCPResponseRules compares TCP response rule configurations within a backend.
// Uses LCS-based content matching to produce minimal INSERT/DELETE/UPDATE operations.
func (c *Comparator) compareTCPResponseRules(parentName string, currentRules, desiredRules models.TCPResponseRules) []Operation {
	return compareEditedItems(
		currentRules, desiredRules,
		func(a, b *models.TCPResponseRule) bool { return a.Equal(*b) },
		func(r *models.TCPResponseRule, i int) Operation {
			return sections.NewTCPResponseRuleBackendCreate(parentName, r, i)
		},
		func(r *models.TCPResponseRule, i int) Operation {
			return sections.NewTCPResponseRuleBackendDelete(parentName, r, i)
		},
		func(r *models.TCPResponseRule, i int) Operation {
			return sections.NewTCPResponseRuleBackendUpdate(parentName, r, i)
		},
	)
}

// compareStickRules compares stick rule configurations within a backend.
// Uses LCS-based content matching to produce minimal INSERT/DELETE/UPDATE operations.
func (c *Comparator) compareStickRules(backendName string, currentRules, desiredRules models.StickRules) []Operation {
	return compareEditedItems(
		currentRules, desiredRules,
		func(a, b *models.StickRule) bool { return a.Equal(*b) },
		func(r *models.StickRule, i int) Operation {
			return sections.NewStickRuleBackendCreate(backendName, r, i)
		},
		func(r *models.StickRule, i int) Operation {
			return sections.NewStickRuleBackendDelete(backendName, r, i)
		},
		func(r *models.StickRule, i int) Operation {
			return sections.NewStickRuleBackendUpdate(backendName, r, i)
		},
	)
}

// compareHTTPAfterResponseRules compares HTTP after response rule configurations within a backend.
// Uses LCS-based content matching to produce minimal INSERT/DELETE/UPDATE operations.
func (c *Comparator) compareHTTPAfterResponseRules(backendName string, currentRules, desiredRules models.HTTPAfterResponseRules) []Operation {
	return compareEditedItems(
		currentRules, desiredRules,
		func(a, b *models.HTTPAfterResponseRule) bool { return a.Equal(*b) },
		func(r *models.HTTPAfterResponseRule, i int) Operation {
			return sections.NewHTTPAfterResponseRuleBackendCreate(backendName, r, i)
		},
		func(r *models.HTTPAfterResponseRule, i int) Operation {
			return sections.NewHTTPAfterResponseRuleBackendDelete(backendName, r, i)
		},
		func(r *models.HTTPAfterResponseRule, i int) Operation {
			return sections.NewHTTPAfterResponseRuleBackendUpdate(backendName, r, i)
		},
	)
}

// compareBackendSwitchingRules compares backend switching rule configurations within a frontend.
// Uses LCS-based content matching to produce minimal INSERT/DELETE/UPDATE operations.
func (c *Comparator) compareBackendSwitchingRules(frontendName string, currentRules, desiredRules models.BackendSwitchingRules) []Operation {
	return compareEditedItems(
		currentRules, desiredRules,
		func(a, b *models.BackendSwitchingRule) bool { return a.Equal(*b) },
		func(r *models.BackendSwitchingRule, i int) Operation {
			return sections.NewBackendSwitchingRuleFrontendCreate(frontendName, r, i)
		},
		func(r *models.BackendSwitchingRule, i int) Operation {
			return sections.NewBackendSwitchingRuleFrontendDelete(frontendName, r, i)
		},
		func(r *models.BackendSwitchingRule, i int) Operation {
			return sections.NewBackendSwitchingRuleFrontendUpdate(frontendName, r, i)
		},
	)
}

// compareServerSwitchingRules compares server switching rule configurations within a backend.
// Uses LCS-based content matching to produce minimal INSERT/DELETE/UPDATE operations.
func (c *Comparator) compareServerSwitchingRules(backendName string, currentRules, desiredRules models.ServerSwitchingRules) []Operation {
	return compareEditedItems(
		currentRules, desiredRules,
		func(a, b *models.ServerSwitchingRule) bool { return a.Equal(*b) },
		func(r *models.ServerSwitchingRule, i int) Operation {
			return sections.NewServerSwitchingRuleBackendCreate(backendName, r, i)
		},
		func(r *models.ServerSwitchingRule, i int) Operation {
			return sections.NewServerSwitchingRuleBackendDelete(backendName, r, i)
		},
		func(r *models.ServerSwitchingRule, i int) Operation {
			return sections.NewServerSwitchingRuleBackendUpdate(backendName, r, i)
		},
	)
}
