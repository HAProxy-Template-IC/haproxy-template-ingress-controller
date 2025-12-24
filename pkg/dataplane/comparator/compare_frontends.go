package comparator

import (
	"github.com/haproxytech/client-native/v6/models"

	"gitlab.com/haproxy-template-ic/haproxy-template-ingress-controller/pkg/dataplane/comparator/sections"
	"gitlab.com/haproxy-template-ic/haproxy-template-ingress-controller/pkg/dataplane/parser"
)

// compareFrontends compares frontend configurations between current and desired.
func (c *Comparator) compareFrontends(current, desired *parser.StructuredConfig, summary *DiffSummary) []Operation {
	var operations []Operation

	// Build maps for easier comparison
	currentFrontends := make(map[string]*models.Frontend)
	for _, frontend := range current.Frontends {
		if frontend.Name != "" {
			currentFrontends[frontend.Name] = frontend
		}
	}

	desiredFrontends := make(map[string]*models.Frontend)
	for _, frontend := range desired.Frontends {
		if frontend.Name != "" {
			desiredFrontends[frontend.Name] = frontend
		}
	}

	// Find added frontends
	addedOps := c.compareAddedFrontends(desiredFrontends, currentFrontends, summary)
	operations = append(operations, addedOps...)

	// Find deleted frontends
	for name, frontend := range currentFrontends {
		if _, exists := desiredFrontends[name]; !exists {
			operations = append(operations, sections.NewFrontendDelete(frontend))
			summary.FrontendsDeleted = append(summary.FrontendsDeleted, name)
		}
	}

	// Find modified frontends
	modifiedOps := c.compareModifiedFrontends(desiredFrontends, currentFrontends, summary)
	operations = append(operations, modifiedOps...)

	return operations
}

// compareAddedFrontends compares added frontends and creates operations for them and their nested elements.
func (c *Comparator) compareAddedFrontends(desiredFrontends, currentFrontends map[string]*models.Frontend, summary *DiffSummary) []Operation {
	// Pre-allocate with estimated capacity (at least one operation per frontend)
	operations := make([]Operation, 0, len(desiredFrontends))

	for name, frontend := range desiredFrontends {
		if _, exists := currentFrontends[name]; exists {
			continue
		}
		operations = append(operations, sections.NewFrontendCreate(frontend))
		summary.FrontendsAdded = append(summary.FrontendsAdded, name)

		// Also create operations for nested elements in this new frontend
		// Compare against an empty frontend to get all nested element create operations
		emptyFrontend := &models.Frontend{}
		emptyFrontend.Name = name

		// Compare ACLs
		aclOps := c.compareACLs(parentTypeFrontend, name, emptyFrontend.ACLList, frontend.ACLList, summary)
		operations = append(operations, aclOps...)

		// Compare HTTP request rules
		requestRuleOps := c.compareHTTPRequestRules(parentTypeFrontend, name, emptyFrontend.HTTPRequestRuleList, frontend.HTTPRequestRuleList)
		operations = append(operations, requestRuleOps...)

		// Compare HTTP response rules
		responseRuleOps := c.compareHTTPResponseRules(parentTypeFrontend, name, emptyFrontend.HTTPResponseRuleList, frontend.HTTPResponseRuleList)
		operations = append(operations, responseRuleOps...)

		// Compare TCP request rules
		tcpRequestRuleOps := c.compareTCPRequestRules(parentTypeFrontend, name, emptyFrontend.TCPRequestRuleList, frontend.TCPRequestRuleList)
		operations = append(operations, tcpRequestRuleOps...)

		// Compare backend switching rules
		backendSwitchingRuleOps := c.compareBackendSwitchingRules(name, emptyFrontend.BackendSwitchingRuleList, frontend.BackendSwitchingRuleList)
		operations = append(operations, backendSwitchingRuleOps...)

		// Compare filters
		filterOps := c.compareFilters(parentTypeFrontend, name, emptyFrontend.FilterList, frontend.FilterList)
		operations = append(operations, filterOps...)

		// Compare captures
		captureOps := c.compareCaptures(name, emptyFrontend.CaptureList, frontend.CaptureList)
		operations = append(operations, captureOps...)

		// Compare log targets
		logTargetOps := c.compareLogTargets(parentTypeFrontend, name, emptyFrontend.LogTargetList, frontend.LogTargetList)
		operations = append(operations, logTargetOps...)

		// Compare QUIC initial rules (v3.1+ only)
		quicInitialRuleOps := c.compareQUICInitialRules(parentTypeFrontend, name, emptyFrontend.QUICInitialRuleList, frontend.QUICInitialRuleList)
		operations = append(operations, quicInitialRuleOps...)

		// Compare binds
		emptyBinds := make(map[string]models.Bind)
		bindOps := c.compareBinds(name, emptyBinds, frontend.Binds)
		operations = append(operations, bindOps...)
	}

	return operations
}

// compareModifiedFrontends compares modified frontends and creates operations for changed nested elements.
func (c *Comparator) compareModifiedFrontends(desiredFrontends, currentFrontends map[string]*models.Frontend, summary *DiffSummary) []Operation {
	var operations []Operation

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

		// Compare binds within this frontend
		bindOps := c.compareBinds(name, currentFrontend.Binds, desiredFrontend.Binds)
		appendOperationsIfNotEmpty(&operations, bindOps, &frontendModified)

		// Compare frontend attributes (excluding ACLs, rules, and binds which we already compared)
		// Create copies to compare without nested collections
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

// compareBinds compares bind configurations within a frontend.
// Binds are identified by their name (Name field in the map key).
func (c *Comparator) compareBinds(frontendName string, currentBinds, desiredBinds map[string]models.Bind) []Operation {
	var operations []Operation

	// Find added binds
	for name := range desiredBinds {
		if _, exists := currentBinds[name]; !exists {
			bind := desiredBinds[name]
			operations = append(operations, sections.NewBindFrontendCreate(frontendName, name, &bind))
		}
	}

	// Find deleted binds
	for name := range currentBinds {
		if _, exists := desiredBinds[name]; !exists {
			bind := currentBinds[name]
			operations = append(operations, sections.NewBindFrontendDelete(frontendName, name, &bind))
		}
	}

	// Find modified binds
	for name := range desiredBinds {
		currentBind, exists := currentBinds[name]
		if !exists {
			continue
		}
		desiredBind := desiredBinds[name]
		// Compare using built-in Equal() method
		if !currentBind.Equal(desiredBind) {
			operations = append(operations, sections.NewBindFrontendUpdate(frontendName, name, &desiredBind))
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
