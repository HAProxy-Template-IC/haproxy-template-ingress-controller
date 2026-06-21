package comparator

import (
	"github.com/haproxytech/client-native/v6/models"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/comparator/sections"
)

// compareQUICInitialRules compares QUIC initial rule configurations within a frontend or defaults.
// QUIC initial rules are compared by position since they don't have unique identifiers.
// QUIC initial rules are only available in HAProxy DataPlane API v3.1+.
func (c *Comparator) compareQUICInitialRules(parentType, parentName string, currentRules, desiredRules models.QUICInitialRules) []Operation {
	create, remove, update := pickOps(parentType, sections.QuicInitialRuleFrontendOps, sections.QuicInitialRuleDefaultsOps)
	return compareIndexedItems(
		currentRules, desiredRules,
		func(a, b *models.QUICInitialRule) bool { return a.Equal(*b) },
		func(r *models.QUICInitialRule, i int) Operation { return create(parentName, r, i) },
		func(r *models.QUICInitialRule, i int) Operation { return remove(parentName, r, i) },
		func(r *models.QUICInitialRule, i int) Operation { return update(parentName, r, i) },
	)
}
