package comparator

import (
	"github.com/haproxytech/client-native/v6/models"

	"haptic/pkg/dataplane/comparator/sections"
	"haptic/pkg/dataplane/parser"
)

// compareLogTargets compares log target configurations within a frontend or backend.
// Log targets are compared by position since they don't have unique identifiers.
func (c *Comparator) compareLogTargets(parentType, parentName string, currentLogs, desiredLogs models.LogTargets) []Operation {
	var operations []Operation

	// Compare log targets by position
	maxLen := len(currentLogs)
	if len(desiredLogs) > maxLen {
		maxLen = len(desiredLogs)
	}

	for i := 0; i < maxLen; i++ {
		hasCurrentLog := i < len(currentLogs)
		hasDesiredLog := i < len(desiredLogs)

		if !hasCurrentLog && hasDesiredLog {
			ops := c.createLogTargetOperation(parentType, parentName, desiredLogs[i], i)
			operations = append(operations, ops...)
		} else if hasCurrentLog && !hasDesiredLog {
			ops := c.deleteLogTargetOperation(parentType, parentName, currentLogs[i], i)
			operations = append(operations, ops...)
		} else if hasCurrentLog && hasDesiredLog {
			ops := c.updateLogTargetOperation(parentType, parentName, currentLogs[i], desiredLogs[i], i)
			operations = append(operations, ops...)
		}
	}

	return operations
}

func (c *Comparator) createLogTargetOperation(parentType, parentName string, logTarget *models.LogTarget, index int) []Operation {
	if parentType == parentTypeFrontend {
		return []Operation{sections.NewLogTargetFrontendCreate(parentName, logTarget, index)}
	}
	return []Operation{sections.NewLogTargetBackendCreate(parentName, logTarget, index)}
}

func (c *Comparator) deleteLogTargetOperation(parentType, parentName string, logTarget *models.LogTarget, index int) []Operation {
	if parentType == parentTypeFrontend {
		return []Operation{sections.NewLogTargetFrontendDelete(parentName, logTarget, index)}
	}
	return []Operation{sections.NewLogTargetBackendDelete(parentName, logTarget, index)}
}

func (c *Comparator) updateLogTargetOperation(parentType, parentName string, currentLog, desiredLog *models.LogTarget, index int) []Operation {
	if !currentLog.Equal(*desiredLog) {
		if parentType == parentTypeFrontend {
			return []Operation{sections.NewLogTargetFrontendUpdate(parentName, desiredLog, index)}
		}
		return []Operation{sections.NewLogTargetBackendUpdate(parentName, desiredLog, index)}
	}
	return nil
}

// compareLogForwards compares log-forward sections between current and desired configurations.
func (c *Comparator) compareLogForwards(current, desired *parser.StructuredConfig) []Operation {
	return compareNamedSections(
		current.LogForwards,
		desired.LogForwards,
		func(lf *models.LogForward) string { return lf.Name },
		func(l1, l2 *models.LogForward) bool { return l1.Equal(*l2) },
		func(lf *models.LogForward) Operation { return sections.NewLogForwardCreate(lf) },
		func(lf *models.LogForward) Operation { return sections.NewLogForwardDelete(lf) },
		func(lf *models.LogForward) Operation { return sections.NewLogForwardUpdate(lf) },
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
		func(lp *models.LogProfile) Operation { return sections.NewLogProfileCreate(lp) },
		func(lp *models.LogProfile) Operation { return sections.NewLogProfileDelete(lp) },
		func(lp *models.LogProfile) Operation { return sections.NewLogProfileUpdate(lp) },
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
