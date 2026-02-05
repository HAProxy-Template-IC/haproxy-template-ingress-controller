package comparator

import (
	"github.com/haproxytech/client-native/v6/models"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/comparator/sections"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/parser"
)

// compareFrontends compares frontend configurations between current and desired.
// Uses pointer indexes from StructuredConfig for zero-copy iteration over binds.
func (c *Comparator) compareFrontends(current, desired *parser.StructuredConfig, summary *DiffSummary) []Operation {
	// Pre-allocate with estimated capacity
	operations := make([]Operation, 0, len(desired.Frontends)*2)

	// Build maps for easier comparison
	currentFrontends := make(map[string]*models.Frontend, len(current.Frontends))
	for _, frontend := range current.Frontends {
		if frontend.Name != "" {
			currentFrontends[frontend.Name] = frontend
		}
	}

	desiredFrontends := make(map[string]*models.Frontend, len(desired.Frontends))
	for _, frontend := range desired.Frontends {
		if frontend.Name != "" {
			desiredFrontends[frontend.Name] = frontend
		}
	}

	// Find added frontends
	addedOps := c.compareAddedFrontendsWithIndexes(desiredFrontends, currentFrontends, current, desired, summary)
	operations = append(operations, addedOps...)

	// Find deleted frontends
	for name, frontend := range currentFrontends {
		if _, exists := desiredFrontends[name]; !exists {
			operations = append(operations, sections.NewFrontendDelete(frontend))
			summary.FrontendsDeleted = append(summary.FrontendsDeleted, name)
		}
	}

	// Find modified frontends
	modifiedOps := c.compareModifiedFrontendsWithIndexes(desiredFrontends, currentFrontends, current, desired, summary)
	operations = append(operations, modifiedOps...)

	return operations
}

// compareAddedFrontendsWithIndexes compares added frontends using pointer indexes.
func (c *Comparator) compareAddedFrontendsWithIndexes(desiredFrontends, currentFrontends map[string]*models.Frontend, _, desired *parser.StructuredConfig, summary *DiffSummary) []Operation {
	// Pre-allocate with estimated capacity (at least one operation per frontend)
	operations := make([]Operation, 0, len(desiredFrontends))

	for name, frontend := range desiredFrontends {
		if _, exists := currentFrontends[name]; exists {
			continue
		}
		operations = append(operations, sections.NewFrontendCreate(frontend))
		summary.FrontendsAdded = append(summary.FrontendsAdded, name)

		// Create operations for all nested elements in the new frontend
		nestedOps := c.createNestedFrontendOperationsWithIndexes(name, frontend, desired.BindIndex, summary)
		operations = append(operations, nestedOps...)
	}

	return operations
}

// createNestedFrontendOperationsWithIndexes creates operations for all nested elements of a new frontend.
// Uses pointer indexes for zero-copy iteration over binds.
func (c *Comparator) createNestedFrontendOperationsWithIndexes(name string, frontend *models.Frontend, bindIndex map[string]map[string]*models.Bind, summary *DiffSummary) []Operation {
	// Pre-allocate with estimated capacity based on nested element counts
	desiredBinds := bindIndex[name]
	estimatedCap := len(desiredBinds) + len(frontend.ACLList) +
		len(frontend.HTTPRequestRuleList) + len(frontend.HTTPResponseRuleList)
	operations := make([]Operation, 0, estimatedCap)

	// Compare ACLs
	aclOps := c.compareACLs(parentTypeFrontend, name, nil, frontend.ACLList, summary)
	operations = append(operations, aclOps...)

	// Compare HTTP request rules
	requestRuleOps := c.compareHTTPRequestRules(parentTypeFrontend, name, nil, frontend.HTTPRequestRuleList)
	operations = append(operations, requestRuleOps...)

	// Compare HTTP response rules
	responseRuleOps := c.compareHTTPResponseRules(parentTypeFrontend, name, nil, frontend.HTTPResponseRuleList)
	operations = append(operations, responseRuleOps...)

	// Compare TCP request rules
	tcpRequestRuleOps := c.compareTCPRequestRules(parentTypeFrontend, name, nil, frontend.TCPRequestRuleList)
	operations = append(operations, tcpRequestRuleOps...)

	// Compare backend switching rules
	backendSwitchingRuleOps := c.compareBackendSwitchingRules(name, nil, frontend.BackendSwitchingRuleList)
	operations = append(operations, backendSwitchingRuleOps...)

	// Compare filters
	filterOps := c.compareFilters(parentTypeFrontend, name, nil, frontend.FilterList)
	operations = append(operations, filterOps...)

	// Compare captures
	captureOps := c.compareCaptures(name, nil, frontend.CaptureList)
	operations = append(operations, captureOps...)

	// Compare log targets
	logTargetOps := c.compareLogTargets(parentTypeFrontend, name, nil, frontend.LogTargetList)
	operations = append(operations, logTargetOps...)

	// Compare QUIC initial rules (v3.1+ only)
	quicInitialRuleOps := c.compareQUICInitialRules(parentTypeFrontend, name, nil, frontend.QUICInitialRuleList)
	operations = append(operations, quicInitialRuleOps...)

	// Compare binds - use pointer index for zero-copy iteration
	emptyBinds := make(map[string]*models.Bind)
	bindOps := c.compareBindsWithIndex(name, emptyBinds, desiredBinds)
	operations = append(operations, bindOps...)

	return operations
}

// compareModifiedFrontendsWithIndexes compares modified frontends using pointer indexes.
func (c *Comparator) compareModifiedFrontendsWithIndexes(desiredFrontends, currentFrontends map[string]*models.Frontend, current, desired *parser.StructuredConfig, summary *DiffSummary) []Operation {
	// Pre-allocate with estimated capacity
	operations := make([]Operation, 0, len(desiredFrontends)*5)

	for name, desiredFrontend := range desiredFrontends {
		currentFrontend, exists := currentFrontends[name]
		if !exists {
			continue
		}
		frontendModified := false

		// Compare ACLs within this frontend
		aclOps := c.compareACLs(parentTypeFrontend, name, currentFrontend.ACLList, desiredFrontend.ACLList, summary)
		appendOperationsIfNotEmpty(&operations, aclOps, &frontendModified)

		// Compare HTTP request rules within this frontend
		requestRuleOps := c.compareHTTPRequestRules(parentTypeFrontend, name, currentFrontend.HTTPRequestRuleList, desiredFrontend.HTTPRequestRuleList)
		appendOperationsIfNotEmpty(&operations, requestRuleOps, &frontendModified)

		// Compare HTTP response rules within this frontend
		responseRuleOps := c.compareHTTPResponseRules(parentTypeFrontend, name, currentFrontend.HTTPResponseRuleList, desiredFrontend.HTTPResponseRuleList)
		appendOperationsIfNotEmpty(&operations, responseRuleOps, &frontendModified)

		// Compare TCP request rules within this frontend
		tcpRequestRuleOps := c.compareTCPRequestRules(parentTypeFrontend, name, currentFrontend.TCPRequestRuleList, desiredFrontend.TCPRequestRuleList)
		appendOperationsIfNotEmpty(&operations, tcpRequestRuleOps, &frontendModified)

		// Compare backend switching rules within this frontend
		backendSwitchingRuleOps := c.compareBackendSwitchingRules(name, currentFrontend.BackendSwitchingRuleList, desiredFrontend.BackendSwitchingRuleList)
		appendOperationsIfNotEmpty(&operations, backendSwitchingRuleOps, &frontendModified)

		// Compare filters within this frontend
		filterOps := c.compareFilters(parentTypeFrontend, name, currentFrontend.FilterList, desiredFrontend.FilterList)
		appendOperationsIfNotEmpty(&operations, filterOps, &frontendModified)

		// Compare captures within this frontend
		captureOps := c.compareCaptures(name, currentFrontend.CaptureList, desiredFrontend.CaptureList)
		appendOperationsIfNotEmpty(&operations, captureOps, &frontendModified)

		// Compare log targets within this frontend
		logTargetOps := c.compareLogTargets(parentTypeFrontend, name, currentFrontend.LogTargetList, desiredFrontend.LogTargetList)
		appendOperationsIfNotEmpty(&operations, logTargetOps, &frontendModified)

		// Compare QUIC initial rules within this frontend (v3.1+ only)
		quicInitialRuleOps := c.compareQUICInitialRules(parentTypeFrontend, name, currentFrontend.QUICInitialRuleList, desiredFrontend.QUICInitialRuleList)
		appendOperationsIfNotEmpty(&operations, quicInitialRuleOps, &frontendModified)

		// Compare binds within this frontend using pointer indexes
		currentBinds := current.BindIndex[name]
		desiredBinds := desired.BindIndex[name]
		if currentBinds == nil {
			currentBinds = make(map[string]*models.Bind)
		}
		if desiredBinds == nil {
			desiredBinds = make(map[string]*models.Bind)
		}
		bindOps := c.compareBindsWithIndex(name, currentBinds, desiredBinds)
		appendOperationsIfNotEmpty(&operations, bindOps, &frontendModified)

		// Compare frontend attributes (excluding ACLs, rules, and binds which we already compared)
		if !frontendsEqualWithoutNestedCollections(currentFrontend, desiredFrontend) {
			operations = append(operations, sections.NewFrontendUpdate(desiredFrontend))
			frontendModified = true
		}

		if frontendModified {
			summary.FrontendsModified = append(summary.FrontendsModified, name)
		}
	}

	return operations
}

// frontendsEqualWithoutNestedCollections checks if two frontends are equal, excluding ACLs, HTTP rules, and binds.
// Uses the HAProxy models' built-in Equal() method to compare ALL frontend attributes
// (mode, timeouts, etc.) automatically, excluding nested collections we compare separately.
func frontendsEqualWithoutNestedCollections(f1, f2 *models.Frontend) bool {
	// Create copies to avoid modifying originals
	f1Copy := *f1
	f2Copy := *f2

	// Clear nested collections so they don't affect comparison
	f1Copy.ACLList = nil
	f2Copy.ACLList = nil
	f1Copy.HTTPRequestRuleList = nil
	f2Copy.HTTPRequestRuleList = nil
	f1Copy.HTTPResponseRuleList = nil
	f2Copy.HTTPResponseRuleList = nil
	f1Copy.TCPRequestRuleList = nil
	f2Copy.TCPRequestRuleList = nil
	f1Copy.BackendSwitchingRuleList = nil
	f2Copy.BackendSwitchingRuleList = nil
	f1Copy.LogTargetList = nil
	f2Copy.LogTargetList = nil
	f1Copy.Binds = nil
	f2Copy.Binds = nil
	f1Copy.FilterList = nil
	f2Copy.FilterList = nil
	f1Copy.CaptureList = nil
	f2Copy.CaptureList = nil

	return f1Copy.Equal(f2Copy)
}

// compareBindsWithIndex compares bind configurations within a frontend using pointer indexes.
func (c *Comparator) compareBindsWithIndex(frontendName string, currentBinds, desiredBinds map[string]*models.Bind) []Operation {
	// Pre-allocate with capacity for potential changes
	maxOps := len(currentBinds) + len(desiredBinds)
	operations := make([]Operation, 0, maxOps)

	// Find added binds
	for name, bind := range desiredBinds {
		if _, exists := currentBinds[name]; !exists {
			operations = append(operations, sections.NewBindFrontendCreate(frontendName, name, bind))
		}
	}

	// Find deleted binds
	for name, bind := range currentBinds {
		if _, exists := desiredBinds[name]; !exists {
			operations = append(operations, sections.NewBindFrontendDelete(frontendName, name, bind))
		}
	}

	// Find modified binds
	for name, desiredBind := range desiredBinds {
		currentBind, exists := currentBinds[name]
		if !exists {
			continue
		}
		// Compare using built-in Equal() method
		if !currentBind.Equal(*desiredBind) {
			operations = append(operations, sections.NewBindFrontendUpdate(frontendName, name, desiredBind))
		}
	}

	return operations
}

// compareCaptures compares capture configurations within a frontend.
// Captures are compared by position since they don't have unique identifiers.
func (c *Comparator) compareCaptures(frontendName string, currentCaptures, desiredCaptures models.Captures) []Operation {
	var operations []Operation

	// Compare captures by position
	maxLen := len(currentCaptures)
	if len(desiredCaptures) > maxLen {
		maxLen = len(desiredCaptures)
	}

	for i := 0; i < maxLen; i++ {
		hasCurrentCapture := i < len(currentCaptures)
		hasDesiredCapture := i < len(desiredCaptures)

		if !hasCurrentCapture && hasDesiredCapture {
			// Capture added at this position
			capture := desiredCaptures[i]
			operations = append(operations, sections.NewCaptureFrontendCreate(frontendName, capture, i))
		} else if hasCurrentCapture && !hasDesiredCapture {
			// Capture removed at this position
			capture := currentCaptures[i]
			operations = append(operations, sections.NewCaptureFrontendDelete(frontendName, capture, i))
		} else if hasCurrentCapture && hasDesiredCapture {
			// Both exist - check if modified
			currentCapture := currentCaptures[i]
			desiredCapture := desiredCaptures[i]

			if !currentCapture.Equal(*desiredCapture) {
				operations = append(operations, sections.NewCaptureFrontendUpdate(frontendName, desiredCapture, i))
			}
		}
	}

	return operations
}
