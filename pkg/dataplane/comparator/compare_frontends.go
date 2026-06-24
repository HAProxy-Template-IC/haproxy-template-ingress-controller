package comparator

import (
	"strings"

	"github.com/haproxytech/client-native/v6/models"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/comparator/sections"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/parser"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/parser/parserconfig"
)

// compareFrontends compares frontend configurations between current and desired.
// Uses pointer indexes from StructuredConfig for zero-copy iteration over binds.
func (c *Comparator) compareFrontends(current, desired *parser.StructuredConfig, summary *DiffSummary) []Operation {
	// Pre-allocate with estimated capacity
	operations := make([]Operation, 0, len(desired.Frontends)*2)

	getName := func(f *models.Frontend) string { return f.Name }
	currentFrontends := parserconfig.BuildPointerIndex(current.Frontends, getName)
	desiredFrontends := parserconfig.BuildPointerIndex(desired.Frontends, getName)

	// Find added frontends
	addedOps := c.compareAddedFrontendsWithIndexes(desiredFrontends, currentFrontends, current, desired, summary)
	operations = append(operations, addedOps...)

	// Find deleted frontends
	for name, frontend := range currentFrontends {
		if _, exists := desiredFrontends[name]; !exists {
			operations = append(operations, sections.FrontendOps.Delete(frontend))
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
		operations = append(operations, sections.FrontendOps.Create(frontend))
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
		len(frontend.HTTPRequestRuleList) + len(frontend.HTTPResponseRuleList) +
		len(frontend.HTTPAfterResponseRuleList)
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

	// Compare HTTP after-response rules (frontend-side; required for chart's
	// SPOA-driven auth-failure header forwarding — see compareFrontendHTTPAfterResponseRules
	// for the architectural detail)
	afterResponseRuleOps := c.compareFrontendHTTPAfterResponseRules(name, nil, frontend.HTTPAfterResponseRuleList)
	operations = append(operations, afterResponseRuleOps...)

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
	bindOps := c.compareBindsWithIndex(name, nil, desiredBinds)
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

		// Compare HTTP after-response rules within this frontend
		afterResponseRuleOps := c.compareFrontendHTTPAfterResponseRules(name, currentFrontend.HTTPAfterResponseRuleList, desiredFrontend.HTTPAfterResponseRuleList)
		appendOperationsIfNotEmpty(&operations, afterResponseRuleOps, &frontendModified)

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
		bindOps := c.compareBindsWithIndex(name, current.BindIndex[name], desired.BindIndex[name])
		appendOperationsIfNotEmpty(&operations, bindOps, &frontendModified)

		// Compare frontend attributes (excluding ACLs, rules, and binds which we already compared)
		if !frontendsEqualWithoutNestedCollections(currentFrontend, desiredFrontend) {
			if op, ok := frontendMaxconnOnlyUpdate(name, currentFrontend, desiredFrontend); ok {
				// Only maxconn changed: apply via the runtime API (no reload).
				operations = append(operations, op)
			} else {
				operations = append(operations, sections.FrontendOps.Update(desiredFrontend))
			}
			frontendModified = true
		}

		if frontendModified {
			summary.FrontendsModified = append(summary.FrontendsModified, name)
		}
	}

	return operations
}

// frontendWithoutNestedCollections returns a copy of f with the nested
// collections (ACLs, HTTP/TCP rules, binds, filters, captures, log targets,
// switching rules) cleared, so attribute-level comparison ignores collections
// the comparator diffs separately.
func frontendWithoutNestedCollections(f *models.Frontend) models.Frontend {
	c := *f
	c.ACLList = nil
	c.HTTPRequestRuleList = nil
	c.HTTPResponseRuleList = nil
	c.HTTPAfterResponseRuleList = nil
	c.TCPRequestRuleList = nil
	c.BackendSwitchingRuleList = nil
	c.LogTargetList = nil
	c.Binds = nil
	c.FilterList = nil
	c.CaptureList = nil
	return c
}

// frontendsEqualWithoutNestedCollections checks if two frontends are equal, excluding ACLs, HTTP rules, and binds.
// Uses the HAProxy models' built-in Equal() method to compare ALL frontend attributes
// (mode, timeouts, etc.) automatically, excluding nested collections we compare separately.
func frontendsEqualWithoutNestedCollections(f1, f2 *models.Frontend) bool {
	a := frontendWithoutNestedCollections(f1)
	b := frontendWithoutNestedCollections(f2)
	return a.Equal(b)
}

// frontendMaxconnOnlyUpdate returns a runtime-eligible maxconn-only update op
// when the sole differing (non-nested) attribute between current and desired is
// Maxconn and the desired value is set. Otherwise ok is false and the caller
// emits a structural frontend update.
//
// Removing/unsetting maxconn (desired Maxconn nil) is NOT runtime-eligible —
// there is no `set maxconn frontend` command to clear it — so it stays
// structural. The desired value being set is what `SetFrontendMaxConn` applies
// to the live worker; the on-disk config the skip_reload push writes carries it
// across the next reload.
func frontendMaxconnOnlyUpdate(name string, current, desired *models.Frontend) (sections.Operation, bool) {
	if desired.Maxconn == nil {
		return nil, false
	}
	// The dataplane tokenizes the X-Runtime-Actions header on spaces and ';';
	// a frontend name containing either would split the `SetFrontendMaxConn
	// <name> <value>` command into malformed tokens. Fall back to a structural
	// (reload) update rather than emit a corrupt action — the same delimiter
	// guard the server delta path applies via safeRuntimeArg. HAPTIC-rendered
	// frontend names never contain these, so this only ever trips on
	// hand-written configs.
	if strings.ContainsAny(name, " ;") {
		return nil, false
	}
	a := frontendWithoutNestedCollections(current)
	b := frontendWithoutNestedCollections(desired)
	a.Maxconn = nil
	b.Maxconn = nil
	if !a.Equal(b) {
		return nil, false // something other than maxconn also differs
	}
	return sections.NewFrontendMaxconnUpdate(name, *desired.Maxconn), true
}

// compareBindsWithIndex compares bind configurations within a frontend using pointer indexes.
// The bind maps are keyed by bind.Name (see parser.BindIndex construction), so the
// factory closures can pull the bind name from the model itself.
func (c *Comparator) compareBindsWithIndex(frontendName string, currentBinds, desiredBinds map[string]*models.Bind) []Operation {
	return compareNamedMaps(
		currentBinds, desiredBinds,
		func(a, b *models.Bind) bool { return a.Equal(*b) },
		func(b *models.Bind) Operation { return sections.BindFrontendOps.Create(frontendName, b.Name, b) },
		func(b *models.Bind) Operation { return sections.BindFrontendOps.Delete(frontendName, b.Name, b) },
		func(b *models.Bind) Operation { return sections.BindFrontendOps.Update(frontendName, b.Name, b) },
	)
}

// compareCaptures compares capture configurations within a frontend.
// Captures are compared by position since they don't have unique identifiers.
func (c *Comparator) compareCaptures(frontendName string, currentCaptures, desiredCaptures models.Captures) []Operation {
	return compareIndexedItems(
		currentCaptures, desiredCaptures,
		func(a, b *models.Capture) bool { return a.Equal(*b) },
		func(capture *models.Capture, i int) Operation {
			return sections.CaptureFrontendOps.Create(frontendName, capture, i)
		},
		func(capture *models.Capture, i int) Operation {
			return sections.CaptureFrontendOps.Delete(frontendName, capture, i)
		},
		func(capture *models.Capture, i int) Operation {
			return sections.CaptureFrontendOps.Update(frontendName, capture, i)
		},
	)
}
