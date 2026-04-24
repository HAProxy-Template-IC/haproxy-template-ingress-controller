package comparator

import (
	"github.com/haproxytech/client-native/v6/models"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/comparator/sections"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/parser"
)

// compareLogTargets compares log target configurations within a frontend or backend.
// Log targets are compared by position since they don't have unique identifiers.
func (c *Comparator) compareLogTargets(parentType, parentName string, currentLogs, desiredLogs models.LogTargets) []Operation {
	create, remove, update := sections.NewLogTargetBackendCreate, sections.NewLogTargetBackendDelete, sections.NewLogTargetBackendUpdate
	if parentType == parentTypeFrontend {
		create, remove, update = sections.NewLogTargetFrontendCreate, sections.NewLogTargetFrontendDelete, sections.NewLogTargetFrontendUpdate
	}
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
		sections.NewLogForwardCreate,
		sections.NewLogForwardDelete,
		sections.NewLogForwardUpdate,
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
		sections.NewLogProfileCreate,
		sections.NewLogProfileDelete,
		sections.NewLogProfileUpdate,
	)
}

// compareTraces compares the traces section between current and desired configurations.
// The traces section is a singleton - it can only be updated, not created or deleted separately.
// Traces configuration is only available in HAProxy DataPlane API v3.1+.
func (c *Comparator) compareTraces(current, desired *parser.StructuredConfig) []Operation {
	var operations []Operation

	// If desired has no traces but current does, we still don't generate a delete
	// because traces is a singleton that's always present (or not supported).
	// If neither has traces, nothing to do.
	if desired.Traces == nil {
		return operations
	}

	// If current is nil but desired has traces, treat as an update
	// (the API will create/replace the traces section)
	if current.Traces == nil {
		operations = append(operations, sections.NewTracesUpdate(desired.Traces))
		return operations
	}

	// Compare using built-in Equal() method
	if !current.Traces.Equal(*desired.Traces) {
		operations = append(operations, sections.NewTracesUpdate(desired.Traces))
	}

	return operations
}
