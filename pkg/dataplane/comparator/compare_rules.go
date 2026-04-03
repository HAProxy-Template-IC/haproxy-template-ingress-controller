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

// compareHTTPRequestRules compares HTTP request rule configurations within a frontend or backend.
// Uses LCS-based content matching to produce minimal INSERT/DELETE/UPDATE operations
// instead of cascading UPDATEs caused by index shifts.
func (c *Comparator) compareHTTPRequestRules(parentType, parentName string, currentRules, desiredRules models.HTTPRequestRules) []Operation {
	diffs := diffIndexedRules(currentRules, desiredRules, func(a, b *models.HTTPRequestRule) bool {
		return a.Equal(*b)
	})
	edits := collapseEdits(diffs)

	var operations []Operation
	for _, e := range edits {
		switch e.Op {
		case editInsert:
			ops := c.createHTTPRequestRuleOperation(parentType, parentName, e.New, e.NewIndex)
			operations = append(operations, ops...)
		case editDelete:
			ops := c.deleteHTTPRequestRuleOperation(parentType, parentName, e.Old, e.OldIndex)
			operations = append(operations, ops...)
		case editUpdate:
			ops := c.updateHTTPRequestRuleOperation(parentType, parentName, e.Old, e.New, e.OldIndex)
			operations = append(operations, ops...)
		}
	}

	return operations
}

func (c *Comparator) createHTTPRequestRuleOperation(parentType, parentName string, rule *models.HTTPRequestRule, index int) []Operation {
	if parentType == parentTypeFrontend {
		return []Operation{sections.NewHTTPRequestRuleFrontendCreate(parentName, rule, index)}
	}
	return []Operation{sections.NewHTTPRequestRuleBackendCreate(parentName, rule, index)}
}

func (c *Comparator) deleteHTTPRequestRuleOperation(parentType, parentName string, rule *models.HTTPRequestRule, index int) []Operation {
	if parentType == parentTypeFrontend {
		return []Operation{sections.NewHTTPRequestRuleFrontendDelete(parentName, rule, index)}
	}
	return []Operation{sections.NewHTTPRequestRuleBackendDelete(parentName, rule, index)}
}

func (c *Comparator) updateHTTPRequestRuleOperation(parentType, parentName string, currentRule, desiredRule *models.HTTPRequestRule, index int) []Operation {
	if !currentRule.Equal(*desiredRule) {
		if parentType == parentTypeFrontend {
			return []Operation{sections.NewHTTPRequestRuleFrontendUpdate(parentName, desiredRule, index)}
		}
		return []Operation{sections.NewHTTPRequestRuleBackendUpdate(parentName, desiredRule, index)}
	}
	return nil
}

// compareHTTPResponseRules compares HTTP response rule configurations within a frontend or backend.
// Uses LCS-based content matching to produce minimal INSERT/DELETE/UPDATE operations.
func (c *Comparator) compareHTTPResponseRules(parentType, parentName string, currentRules, desiredRules models.HTTPResponseRules) []Operation {
	diffs := diffIndexedRules(currentRules, desiredRules, func(a, b *models.HTTPResponseRule) bool {
		return a.Equal(*b)
	})
	edits := collapseEdits(diffs)

	var operations []Operation
	for _, e := range edits {
		switch e.Op {
		case editInsert:
			ops := c.createHTTPResponseRuleOperation(parentType, parentName, e.New, e.NewIndex)
			operations = append(operations, ops...)
		case editDelete:
			ops := c.deleteHTTPResponseRuleOperation(parentType, parentName, e.Old, e.OldIndex)
			operations = append(operations, ops...)
		case editUpdate:
			ops := c.updateHTTPResponseRuleOperation(parentType, parentName, e.Old, e.New, e.OldIndex)
			operations = append(operations, ops...)
		}
	}

	return operations
}

func (c *Comparator) createHTTPResponseRuleOperation(parentType, parentName string, rule *models.HTTPResponseRule, index int) []Operation {
	if parentType == parentTypeFrontend {
		return []Operation{sections.NewHTTPResponseRuleFrontendCreate(parentName, rule, index)}
	}
	return []Operation{sections.NewHTTPResponseRuleBackendCreate(parentName, rule, index)}
}

func (c *Comparator) deleteHTTPResponseRuleOperation(parentType, parentName string, rule *models.HTTPResponseRule, index int) []Operation {
	if parentType == parentTypeFrontend {
		return []Operation{sections.NewHTTPResponseRuleFrontendDelete(parentName, rule, index)}
	}
	return []Operation{sections.NewHTTPResponseRuleBackendDelete(parentName, rule, index)}
}

func (c *Comparator) updateHTTPResponseRuleOperation(parentType, parentName string, currentRule, desiredRule *models.HTTPResponseRule, index int) []Operation {
	if !currentRule.Equal(*desiredRule) {
		if parentType == parentTypeFrontend {
			return []Operation{sections.NewHTTPResponseRuleFrontendUpdate(parentName, desiredRule, index)}
		}
		return []Operation{sections.NewHTTPResponseRuleBackendUpdate(parentName, desiredRule, index)}
	}
	return nil
}

// compareTCPRequestRules compares TCP request rule configurations within a frontend or backend.
// Uses LCS-based content matching to produce minimal INSERT/DELETE/UPDATE operations.
func (c *Comparator) compareTCPRequestRules(parentType, parentName string, currentRules, desiredRules models.TCPRequestRules) []Operation {
	diffs := diffIndexedRules(currentRules, desiredRules, func(a, b *models.TCPRequestRule) bool {
		return a.Equal(*b)
	})
	edits := collapseEdits(diffs)

	var operations []Operation
	for _, e := range edits {
		switch e.Op {
		case editInsert:
			ops := c.createTCPRequestRuleOperation(parentType, parentName, e.New, e.NewIndex)
			operations = append(operations, ops...)
		case editDelete:
			ops := c.deleteTCPRequestRuleOperation(parentType, parentName, e.Old, e.OldIndex)
			operations = append(operations, ops...)
		case editUpdate:
			ops := c.updateTCPRequestRuleOperation(parentType, parentName, e.Old, e.New, e.OldIndex)
			operations = append(operations, ops...)
		}
	}

	return operations
}

func (c *Comparator) createTCPRequestRuleOperation(parentType, parentName string, rule *models.TCPRequestRule, index int) []Operation {
	if parentType == parentTypeFrontend {
		return []Operation{sections.NewTCPRequestRuleFrontendCreate(parentName, rule, index)}
	}
	return []Operation{sections.NewTCPRequestRuleBackendCreate(parentName, rule, index)}
}

func (c *Comparator) deleteTCPRequestRuleOperation(parentType, parentName string, rule *models.TCPRequestRule, index int) []Operation {
	if parentType == parentTypeFrontend {
		return []Operation{sections.NewTCPRequestRuleFrontendDelete(parentName, rule, index)}
	}
	return []Operation{sections.NewTCPRequestRuleBackendDelete(parentName, rule, index)}
}

func (c *Comparator) updateTCPRequestRuleOperation(parentType, parentName string, currentRule, desiredRule *models.TCPRequestRule, index int) []Operation {
	if !currentRule.Equal(*desiredRule) {
		if parentType == parentTypeFrontend {
			return []Operation{sections.NewTCPRequestRuleFrontendUpdate(parentName, desiredRule, index)}
		}
		return []Operation{sections.NewTCPRequestRuleBackendUpdate(parentName, desiredRule, index)}
	}
	return nil
}

// compareTCPResponseRules compares TCP response rule configurations within a backend.
// Uses LCS-based content matching to produce minimal INSERT/DELETE/UPDATE operations.
func (c *Comparator) compareTCPResponseRules(parentName string, currentRules, desiredRules models.TCPResponseRules) []Operation {
	diffs := diffIndexedRules(currentRules, desiredRules, func(a, b *models.TCPResponseRule) bool {
		return a.Equal(*b)
	})
	edits := collapseEdits(diffs)

	var operations []Operation
	for _, e := range edits {
		switch e.Op {
		case editInsert:
			operations = append(operations, sections.NewTCPResponseRuleBackendCreate(parentName, e.New, e.NewIndex))
		case editDelete:
			operations = append(operations, sections.NewTCPResponseRuleBackendDelete(parentName, e.Old, e.OldIndex))
		case editUpdate:
			operations = append(operations, sections.NewTCPResponseRuleBackendUpdate(parentName, e.New, e.OldIndex))
		}
	}

	return operations
}

// compareStickRules compares stick rule configurations within a backend.
// Uses LCS-based content matching to produce minimal INSERT/DELETE/UPDATE operations.
func (c *Comparator) compareStickRules(backendName string, currentRules, desiredRules models.StickRules) []Operation {
	diffs := diffIndexedRules(currentRules, desiredRules, func(a, b *models.StickRule) bool {
		return a.Equal(*b)
	})
	edits := collapseEdits(diffs)

	var operations []Operation
	for _, e := range edits {
		switch e.Op {
		case editInsert:
			operations = append(operations, sections.NewStickRuleBackendCreate(backendName, e.New, e.NewIndex))
		case editDelete:
			operations = append(operations, sections.NewStickRuleBackendDelete(backendName, e.Old, e.OldIndex))
		case editUpdate:
			operations = append(operations, sections.NewStickRuleBackendUpdate(backendName, e.New, e.OldIndex))
		}
	}

	return operations
}

// compareHTTPAfterResponseRules compares HTTP after response rule configurations within a backend.
// Uses LCS-based content matching to produce minimal INSERT/DELETE/UPDATE operations.
func (c *Comparator) compareHTTPAfterResponseRules(backendName string, currentRules, desiredRules models.HTTPAfterResponseRules) []Operation {
	diffs := diffIndexedRules(currentRules, desiredRules, func(a, b *models.HTTPAfterResponseRule) bool {
		return a.Equal(*b)
	})
	edits := collapseEdits(diffs)

	var operations []Operation
	for _, e := range edits {
		switch e.Op {
		case editInsert:
			operations = append(operations, sections.NewHTTPAfterResponseRuleBackendCreate(backendName, e.New, e.NewIndex))
		case editDelete:
			operations = append(operations, sections.NewHTTPAfterResponseRuleBackendDelete(backendName, e.Old, e.OldIndex))
		case editUpdate:
			operations = append(operations, sections.NewHTTPAfterResponseRuleBackendUpdate(backendName, e.New, e.OldIndex))
		}
	}

	return operations
}

// compareBackendSwitchingRules compares backend switching rule configurations within a frontend.
// Uses LCS-based content matching to produce minimal INSERT/DELETE/UPDATE operations.
func (c *Comparator) compareBackendSwitchingRules(frontendName string, currentRules, desiredRules models.BackendSwitchingRules) []Operation {
	diffs := diffIndexedRules(currentRules, desiredRules, func(a, b *models.BackendSwitchingRule) bool {
		return a.Equal(*b)
	})
	edits := collapseEdits(diffs)

	var operations []Operation
	for _, e := range edits {
		switch e.Op {
		case editInsert:
			operations = append(operations, sections.NewBackendSwitchingRuleFrontendCreate(frontendName, e.New, e.NewIndex))
		case editDelete:
			operations = append(operations, sections.NewBackendSwitchingRuleFrontendDelete(frontendName, e.Old, e.OldIndex))
		case editUpdate:
			operations = append(operations, sections.NewBackendSwitchingRuleFrontendUpdate(frontendName, e.New, e.OldIndex))
		}
	}

	return operations
}

// compareServerSwitchingRules compares server switching rule configurations within a backend.
// Uses LCS-based content matching to produce minimal INSERT/DELETE/UPDATE operations.
func (c *Comparator) compareServerSwitchingRules(backendName string, currentRules, desiredRules models.ServerSwitchingRules) []Operation {
	diffs := diffIndexedRules(currentRules, desiredRules, func(a, b *models.ServerSwitchingRule) bool {
		return a.Equal(*b)
	})
	edits := collapseEdits(diffs)

	var operations []Operation
	for _, e := range edits {
		switch e.Op {
		case editInsert:
			operations = append(operations, sections.NewServerSwitchingRuleBackendCreate(backendName, e.New, e.NewIndex))
		case editDelete:
			operations = append(operations, sections.NewServerSwitchingRuleBackendDelete(backendName, e.Old, e.OldIndex))
		case editUpdate:
			operations = append(operations, sections.NewServerSwitchingRuleBackendUpdate(backendName, e.New, e.OldIndex))
		}
	}

	return operations
}
