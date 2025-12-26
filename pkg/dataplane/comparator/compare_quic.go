package comparator

import (
	"github.com/haproxytech/client-native/v6/models"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/comparator/sections"
)

// compareQUICInitialRules compares QUIC initial rule configurations within a frontend or defaults.
// QUIC initial rules are compared by position since they don't have unique identifiers.
// QUIC initial rules are only available in HAProxy DataPlane API v3.1+.
func (c *Comparator) compareQUICInitialRules(parentType, parentName string, currentRules, desiredRules models.QUICInitialRules) []Operation {
	var operations []Operation

	// Compare QUIC initial rules by position
	maxLen := len(currentRules)
	if len(desiredRules) > maxLen {
		maxLen = len(desiredRules)
	}

	for i := 0; i < maxLen; i++ {
		hasCurrentRule := i < len(currentRules)
		hasDesiredRule := i < len(desiredRules)

		if !hasCurrentRule && hasDesiredRule {
			ops := c.createQUICInitialRuleOperation(parentType, parentName, desiredRules[i], i)
			operations = append(operations, ops...)
		} else if hasCurrentRule && !hasDesiredRule {
			ops := c.deleteQUICInitialRuleOperation(parentType, parentName, currentRules[i], i)
			operations = append(operations, ops...)
		} else if hasCurrentRule && hasDesiredRule {
			ops := c.updateQUICInitialRuleOperation(parentType, parentName, currentRules[i], desiredRules[i], i)
			operations = append(operations, ops...)
		}
	}

	return operations
}

func (c *Comparator) createQUICInitialRuleOperation(parentType, parentName string, rule *models.QUICInitialRule, index int) []Operation {
	if parentType == parentTypeFrontend {
		return []Operation{sections.NewQUICInitialRuleFrontendCreate(parentName, rule, index)}
	}
	return []Operation{sections.NewQUICInitialRuleDefaultsCreate(parentName, rule, index)}
}

func (c *Comparator) deleteQUICInitialRuleOperation(parentType, parentName string, rule *models.QUICInitialRule, index int) []Operation {
	if parentType == parentTypeFrontend {
		return []Operation{sections.NewQUICInitialRuleFrontendDelete(parentName, rule, index)}
	}
	return []Operation{sections.NewQUICInitialRuleDefaultsDelete(parentName, rule, index)}
}

func (c *Comparator) updateQUICInitialRuleOperation(parentType, parentName string, currentRule, desiredRule *models.QUICInitialRule, index int) []Operation {
	if !currentRule.Equal(*desiredRule) {
		if parentType == parentTypeFrontend {
			return []Operation{sections.NewQUICInitialRuleFrontendUpdate(parentName, desiredRule, index)}
		}
		return []Operation{sections.NewQUICInitialRuleDefaultsUpdate(parentName, desiredRule, index)}
	}
	return nil
}
