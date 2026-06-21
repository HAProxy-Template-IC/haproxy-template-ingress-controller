package comparator

import (
	"github.com/haproxytech/client-native/v6/models"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/comparator/sections"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/parser"
)

// compareLogTargets compares log target configurations within a frontend or backend.
// Log targets are compared by position since they don't have unique identifiers.
func (c *Comparator) compareLogTargets(parentType, parentName string, currentLogs, desiredLogs models.LogTargets) []Operation {
	create, remove, update := pickOps(parentType, sections.LogTargetFrontendOps, sections.LogTargetBackendOps)
	return compareIndexedItems(
		currentLogs, desiredLogs,
		func(a, b *models.LogTarget) bool { return a.Equal(*b) },
		func(log *models.LogTarget, i int) Operation { return create(parentName, log, i) },
		func(log *models.LogTarget, i int) Operation { return remove(parentName, log, i) },
		func(log *models.LogTarget, i int) Operation { return update(parentName, log, i) },
	)
}

// compareLogForwards compares log-forward sections between current and desired configurations.
func (c *Comparator) compareLogForwards(current, desired *parser.StructuredConfig) []Operation {
	return compareNamedSections(
		current.LogForwards,
		desired.LogForwards,
		func(lf *models.LogForward) string { return lf.Name },
		func(l1, l2 *models.LogForward) bool { return l1.Equal(*l2) },
		sections.LogForwardOps.Create,
		sections.LogForwardOps.Delete,
		sections.LogForwardOps.Update,
	)
}

// compareLogProfiles compares log-profile sections between current and desired configurations.
// Log profiles are only available in HAProxy DataPlane API v3.1+.
func (c *Comparator) compareLogProfiles(current, desired *parser.StructuredConfig) []Operation {
	return compareNamedSections(
		current.LogProfiles,
		desired.LogProfiles,
		func(lp *models.LogProfile) string { return lp.Name },
		func(l1, l2 *models.LogProfile) bool { return l1.Equal(*l2) },
		sections.LogProfileOps.Create,
		sections.LogProfileOps.Delete,
		sections.LogProfileOps.Update,
	)
}

// compareTraces compares the traces section between current and desired configurations.
// The traces section is a singleton - it can only be updated, not created or deleted separately.
// Traces configuration is only available in HAProxy DataPlane API v3.1+.
//
// When desired is nil we never emit a delete - traces is always present (or
// the API doesn't support it). When current is nil we still emit an update
// because the API treats it as a create/replace. Otherwise emit an update
// only when contents differ.
func (c *Comparator) compareTraces(current, desired *parser.StructuredConfig) []Operation {
	if desired.Traces == nil {
		return nil
	}
	if current.Traces == nil || !current.Traces.Equal(*desired.Traces) {
		return []Operation{sections.NewTracesUpdate(desired.Traces)}
	}
	return nil
}
