package comparator

import (
	"github.com/haproxytech/client-native/v6/models"

	"haproxy-template-ic/pkg/dataplane/comparator/sections"
)

// safeGetHTTPRequestRule safely retrieves a rule at the given index, returning nil if out of bounds.
func safeGetHTTPRequestRule(rules models.HTTPRequestRules, i int) *models.HTTPRequestRule {
	if i >= 0 && i < len(rules) {
		return rules[i]
	}
	return nil
}

// safeGetHTTPResponseRule safely retrieves a rule at the given index, returning nil if out of bounds.
func safeGetHTTPResponseRule(rules models.HTTPResponseRules, i int) *models.HTTPResponseRule {
	if i >= 0 && i < len(rules) {
		return rules[i]
	}
	return nil
}

// safeGetBackendSwitchingRule safely retrieves a rule at the given index, returning nil if out of bounds.
func safeGetBackendSwitchingRule(rules models.BackendSwitchingRules, i int) *models.BackendSwitchingRule {
	if i >= 0 && i < len(rules) {
		return rules[i]
	}
	return nil
}

// compareACLs compares ACL configurations within a frontend or backend.
// ACLs are identified by their name (ACLName field).
func (c *Comparator) compareACLs(parentType, parentName string, currentACLs, desiredACLs models.Acls, _ *DiffSummary) []Operation {
	var operations []Operation

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
	operations = append(operations, addedOps...)

	// Find deleted ACLs
	deletedOps := c.compareDeletedACLs(parentType, parentName, currentACLMap, desiredACLMap, currentACLs)
	operations = append(operations, deletedOps...)

	// Find modified ACLs
	modifiedOps := c.compareModifiedACLs(parentType, parentName, desiredACLMap, currentACLMap, currentACLs, desiredACLs)
	operations = append(operations, modifiedOps...)

	return operations
}

// compareAddedACLs compares added ACLs and creates operations for them.
func (c *Comparator) compareAddedACLs(parentType, parentName string, desiredACLMap, currentACLMap map[string]int, desiredACLs models.Acls) []Operation {
	var operations []Operation

	for name, idx := range desiredACLMap {
		if _, exists := currentACLMap[name]; !exists {
			acl := desiredACLs[idx]
			if parentType == parentTypeFrontend {
				operations = append(operations, sections.NewACLFrontendCreate(parentName, acl, idx))
			} else {
				operations = append(operations, sections.NewACLBackendCreate(parentName, acl, idx))
			}
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
// Rules are compared by position since they don't have unique identifiers.
func (c *Comparator) compareHTTPRequestRules(parentType, parentName string, currentRules, desiredRules models.HTTPRequestRules) []Operation {
	var operations []Operation

	// Compare rules by position
	maxLen := len(currentRules)
	if len(desiredRules) > maxLen {
		maxLen = len(desiredRules)
	}

	for i := 0; i < maxLen; i++ {
		currentRule := safeGetHTTPRequestRule(currentRules, i)
		desiredRule := safeGetHTTPRequestRule(desiredRules, i)

		switch {
		case currentRule == nil && desiredRule != nil:
			ops := c.createHTTPRequestRuleOperation(parentType, parentName, desiredRule, i)
			operations = append(operations, ops...)
		case currentRule != nil && desiredRule == nil:
			ops := c.deleteHTTPRequestRuleOperation(parentType, parentName, currentRule, i)
			operations = append(operations, ops...)
		case currentRule != nil && desiredRule != nil:
			ops := c.updateHTTPRequestRuleOperation(parentType, parentName, currentRule, desiredRule, i)
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
// Rules are compared by position since they don't have unique identifiers.
func (c *Comparator) compareHTTPResponseRules(parentType, parentName string, currentRules, desiredRules models.HTTPResponseRules) []Operation {
	var operations []Operation

	// Compare rules by position
	maxLen := len(currentRules)
	if len(desiredRules) > maxLen {
		maxLen = len(desiredRules)
	}

	for i := 0; i < maxLen; i++ {
		currentRule := safeGetHTTPResponseRule(currentRules, i)
		desiredRule := safeGetHTTPResponseRule(desiredRules, i)

		switch {
		case currentRule == nil && desiredRule != nil:
			ops := c.createHTTPResponseRuleOperation(parentType, parentName, desiredRule, i)
			operations = append(operations, ops...)
		case currentRule != nil && desiredRule == nil:
			ops := c.deleteHTTPResponseRuleOperation(parentType, parentName, currentRule, i)
			operations = append(operations, ops...)
		case currentRule != nil && desiredRule != nil:
			ops := c.updateHTTPResponseRuleOperation(parentType, parentName, currentRule, desiredRule, i)
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
// Rules are compared by position since they don't have unique identifiers.
func (c *Comparator) compareTCPRequestRules(parentType, parentName string, currentRules, desiredRules models.TCPRequestRules) []Operation {
	var operations []Operation

	// Compare rules by position
	maxLen := len(currentRules)
	if len(desiredRules) > maxLen {
		maxLen = len(desiredRules)
	}

	for i := 0; i < maxLen; i++ {
		hasCurrentRule := i < len(currentRules)
		hasDesiredRule := i < len(desiredRules)

		if !hasCurrentRule && hasDesiredRule {
			ops := c.createTCPRequestRuleOperation(parentType, parentName, desiredRules[i], i)
			operations = append(operations, ops...)
		} else if hasCurrentRule && !hasDesiredRule {
			ops := c.deleteTCPRequestRuleOperation(parentType, parentName, currentRules[i], i)
			operations = append(operations, ops...)
		} else if hasCurrentRule && hasDesiredRule {
			ops := c.updateTCPRequestRuleOperation(parentType, parentName, currentRules[i], desiredRules[i], i)
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
// Rules are compared by position since they don't have unique identifiers.
func (c *Comparator) compareTCPResponseRules(parentName string, currentRules, desiredRules models.TCPResponseRules) []Operation {
	var operations []Operation

	// Compare rules by position
	maxLen := len(currentRules)
	if len(desiredRules) > maxLen {
		maxLen = len(desiredRules)
	}

	for i := 0; i < maxLen; i++ {
		hasCurrentRule := i < len(currentRules)
		hasDesiredRule := i < len(desiredRules)

		if !hasCurrentRule && hasDesiredRule {
			// Rule added at this position
			rule := desiredRules[i]
			operations = append(operations, sections.NewTCPResponseRuleBackendCreate(parentName, rule, i))
		} else if hasCurrentRule && !hasDesiredRule {
			// Rule removed at this position
			rule := currentRules[i]
			operations = append(operations, sections.NewTCPResponseRuleBackendDelete(parentName, rule, i))
		} else if hasCurrentRule && hasDesiredRule {
			// Both exist - check if modified
			currentRule := currentRules[i]
			desiredRule := desiredRules[i]

			if !currentRule.Equal(*desiredRule) {
				operations = append(operations, sections.NewTCPResponseRuleBackendUpdate(parentName, desiredRule, i))
			}
		}
	}

	return operations
}

// compareStickRules compares stick rule configurations within a backend.
// Stick rules are compared by position since they don't have unique identifiers.
// Backend-only (frontends do not support stick rules).
func (c *Comparator) compareStickRules(backendName string, currentRules, desiredRules models.StickRules) []Operation {
	var operations []Operation

	// Compare stick rules by position
	maxLen := len(currentRules)
	if len(desiredRules) > maxLen {
		maxLen = len(desiredRules)
	}

	for i := 0; i < maxLen; i++ {
		hasCurrentRule := i < len(currentRules)
		hasDesiredRule := i < len(desiredRules)

		if !hasCurrentRule && hasDesiredRule {
			// Stick rule added at this position
			rule := desiredRules[i]
			operations = append(operations, sections.NewStickRuleBackendCreate(backendName, rule, i))
		} else if hasCurrentRule && !hasDesiredRule {
			// Stick rule removed at this position
			rule := currentRules[i]
			operations = append(operations, sections.NewStickRuleBackendDelete(backendName, rule, i))
		} else if hasCurrentRule && hasDesiredRule {
			// Both exist - check if modified
			currentRule := currentRules[i]
			desiredRule := desiredRules[i]

			if !currentRule.Equal(*desiredRule) {
				operations = append(operations, sections.NewStickRuleBackendUpdate(backendName, desiredRule, i))
			}
		}
	}

	return operations
}

// compareHTTPAfterResponseRules compares HTTP after response rule configurations within a backend.
// Rules are compared by position since they don't have unique identifiers.
// Backend-only (frontends do not support HTTP after response rules).
func (c *Comparator) compareHTTPAfterResponseRules(backendName string, currentRules, desiredRules models.HTTPAfterResponseRules) []Operation {
	var operations []Operation

	// Compare rules by position
	maxLen := len(currentRules)
	if len(desiredRules) > maxLen {
		maxLen = len(desiredRules)
	}

	for i := 0; i < maxLen; i++ {
		hasCurrentRule := i < len(currentRules)
		hasDesiredRule := i < len(desiredRules)

		if !hasCurrentRule && hasDesiredRule {
			// Rule added at this position
			rule := desiredRules[i]
			operations = append(operations, sections.NewHTTPAfterResponseRuleBackendCreate(backendName, rule, i))
		} else if hasCurrentRule && !hasDesiredRule {
			// Rule removed at this position
			rule := currentRules[i]
			operations = append(operations, sections.NewHTTPAfterResponseRuleBackendDelete(backendName, rule, i))
		} else if hasCurrentRule && hasDesiredRule {
			// Both exist - check if modified
			currentRule := currentRules[i]
			desiredRule := desiredRules[i]

			if !currentRule.Equal(*desiredRule) {
				operations = append(operations, sections.NewHTTPAfterResponseRuleBackendUpdate(backendName, desiredRule, i))
			}
		}
	}

	return operations
}

// compareBackendSwitchingRules compares backend switching rule configurations within a frontend.
// Rules are compared by position since they don't have unique identifiers.
func (c *Comparator) compareBackendSwitchingRules(frontendName string, currentRules, desiredRules models.BackendSwitchingRules) []Operation {
	var operations []Operation

	// Compare rules by position
	maxLen := len(currentRules)
	if len(desiredRules) > maxLen {
		maxLen = len(desiredRules)
	}

	for i := 0; i < maxLen; i++ {
		currentRule := safeGetBackendSwitchingRule(currentRules, i)
		desiredRule := safeGetBackendSwitchingRule(desiredRules, i)

		switch {
		case currentRule == nil && desiredRule != nil:
			// Rule added at this position
			operations = append(operations, sections.NewBackendSwitchingRuleFrontendCreate(frontendName, desiredRule, i))
		case currentRule != nil && desiredRule == nil:
			// Rule removed at this position
			operations = append(operations, sections.NewBackendSwitchingRuleFrontendDelete(frontendName, currentRule, i))
		case currentRule != nil && desiredRule != nil:
			// Both exist - check if modified
			if !currentRule.Equal(*desiredRule) {
				operations = append(operations, sections.NewBackendSwitchingRuleFrontendUpdate(frontendName, desiredRule, i))
			}
		}
	}

	return operations
}

// compareServerSwitchingRules compares server switching rule configurations within a backend.
// Rules are compared by position since they don't have unique identifiers.
func (c *Comparator) compareServerSwitchingRules(backendName string, currentRules, desiredRules models.ServerSwitchingRules) []Operation {
	var operations []Operation

	// Compare rules by position
	maxLen := len(currentRules)
	if len(desiredRules) > maxLen {
		maxLen = len(desiredRules)
	}

	for i := 0; i < maxLen; i++ {
		hasCurrentRule := i < len(currentRules)
		hasDesiredRule := i < len(desiredRules)

		if !hasCurrentRule && hasDesiredRule {
			// Rule added at this position
			rule := desiredRules[i]
			operations = append(operations, sections.NewServerSwitchingRuleBackendCreate(backendName, rule, i))
		} else if hasCurrentRule && !hasDesiredRule {
			// Rule removed at this position
			rule := currentRules[i]
			operations = append(operations, sections.NewServerSwitchingRuleBackendDelete(backendName, rule, i))
		} else if hasCurrentRule && hasDesiredRule {
			// Both exist - check if modified
			currentRule := currentRules[i]
			desiredRule := desiredRules[i]

			if !currentRule.Equal(*desiredRule) {
				operations = append(operations, sections.NewServerSwitchingRuleBackendUpdate(backendName, desiredRule, i))
			}
		}
	}

	return operations
}
