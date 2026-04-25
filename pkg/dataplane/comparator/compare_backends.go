package comparator

import (
	"github.com/haproxytech/client-native/v6/models"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/comparator/sections"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/parser"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/parser/parserconfig"
)

// compareBackends compares backend configurations between current and desired.
// Uses pointer indexes from StructuredConfig for zero-copy iteration over servers and server templates.
func (c *Comparator) compareBackends(current, desired *parser.StructuredConfig, summary *DiffSummary) []Operation {
	// Pre-allocate with estimated capacity (roughly 2 ops per backend for add/modify + servers)
	operations := make([]Operation, 0, len(desired.Backends)*2)

	getName := func(b *models.Backend) string { return b.Name }
	currentBackends := parserconfig.BuildPointerIndex(current.Backends, getName)
	desiredBackends := parserconfig.BuildPointerIndex(desired.Backends, getName)

	// Find added backends
	addedOps := c.compareAddedBackendsWithIndexes(desiredBackends, currentBackends, current, desired, summary)
	operations = append(operations, addedOps...)

	// Find deleted backends
	for name, backend := range currentBackends {
		if _, exists := desiredBackends[name]; !exists {
			operations = append(operations, sections.NewBackendDelete(backend))
			summary.BackendsDeleted = append(summary.BackendsDeleted, name)
		}
	}

	// Find modified backends
	modifiedOps := c.compareModifiedBackendsWithIndexes(desiredBackends, currentBackends, current, desired, summary)
	operations = append(operations, modifiedOps...)

	return operations
}

// compareAddedBackendsWithIndexes compares added backends and creates operations for them and their nested elements.
// Uses pointer indexes for zero-copy iteration over servers and server templates.
func (c *Comparator) compareAddedBackendsWithIndexes(desiredBackends, currentBackends map[string]*models.Backend, _, desired *parser.StructuredConfig, summary *DiffSummary) []Operation {
	// Pre-allocate with estimated capacity (at least one operation per backend)
	operations := make([]Operation, 0, len(desiredBackends))

	for name, backend := range desiredBackends {
		if _, exists := currentBackends[name]; exists {
			continue
		}
		operations = append(operations, sections.NewBackendCreate(backend))
		summary.BackendsAdded = append(summary.BackendsAdded, name)

		// Create operations for all nested elements in the new backend using pointer indexes
		nestedOps := c.createNestedBackendOperationsWithIndexes(name, backend, desired.ServerIndex, desired.ServerTemplateIndex, summary)
		operations = append(operations, nestedOps...)
	}

	return operations
}

// createNestedBackendOperationsWithIndexes creates operations for all nested elements of a new backend.
// Uses pointer indexes for zero-copy iteration over servers and server templates.
func (c *Comparator) createNestedBackendOperationsWithIndexes(name string, backend *models.Backend, serverIndex map[string]map[string]*models.Server, serverTemplateIndex map[string]map[string]*models.ServerTemplate, summary *DiffSummary) []Operation {
	// Pre-allocate with estimated capacity based on nested element counts from indexes
	desiredServers := serverIndex[name]
	desiredTemplates := serverTemplateIndex[name]
	estimatedCap := len(desiredServers) + len(desiredTemplates) + len(backend.ACLList) +
		len(backend.HTTPRequestRuleList) + len(backend.HTTPResponseRuleList)
	operations := make([]Operation, 0, estimatedCap)

	// Servers - use pointer index for zero-copy iteration
	operations = append(operations, c.compareServersWithIndex(name, nil, desiredServers, summary)...)

	// Server templates - use pointer index for zero-copy iteration
	operations = append(operations, c.compareServerTemplatesWithIndex(name, nil, desiredTemplates)...)

	// ACLs and rules (compare against nil for empty collections)
	operations = append(operations, c.compareACLs("backend", name, nil, backend.ACLList, summary)...)
	operations = append(operations, c.compareHTTPRequestRules("backend", name, nil, backend.HTTPRequestRuleList)...)
	operations = append(operations, c.compareHTTPResponseRules("backend", name, nil, backend.HTTPResponseRuleList)...)
	operations = append(operations, c.compareTCPRequestRules("backend", name, nil, backend.TCPRequestRuleList)...)
	operations = append(operations, c.compareTCPResponseRules(name, nil, backend.TCPResponseRuleList)...)
	operations = append(operations, c.compareLogTargets("backend", name, nil, backend.LogTargetList)...)
	operations = append(operations, c.compareStickRules(name, nil, backend.StickRuleList)...)
	operations = append(operations, c.compareHTTPAfterResponseRules(name, nil, backend.HTTPAfterResponseRuleList)...)
	operations = append(operations, c.compareServerSwitchingRules(name, nil, backend.ServerSwitchingRuleList)...)
	operations = append(operations, c.compareFilters("backend", name, nil, backend.FilterList)...)
	operations = append(operations, c.compareHTTPChecks(name, nil, backend.HTTPCheckList)...)
	operations = append(operations, c.compareTCPChecks(name, nil, backend.TCPCheckRuleList)...)

	return operations
}

// compareModifiedBackendsWithIndexes compares modified backends and creates operations for changed nested elements.
// Uses pointer indexes for zero-copy iteration over servers and server templates.
func (c *Comparator) compareModifiedBackendsWithIndexes(desiredBackends, currentBackends map[string]*models.Backend, current, desired *parser.StructuredConfig, summary *DiffSummary) []Operation {
	// Pre-allocate with estimated capacity (assume ~5 ops per modified backend)
	operations := make([]Operation, 0, len(desiredBackends)*5)

	for name, desiredBackend := range desiredBackends {
		currentBackend, exists := currentBackends[name]
		if !exists {
			continue
		}
		backendModified := false

		// Compare servers within this backend using pointer indexes
		serverOps := c.compareServersWithIndex(name, current.ServerIndex[name], desired.ServerIndex[name], summary)
		appendOperationsIfNotEmpty(&operations, serverOps, &backendModified)

		// Compare ACLs within this backend
		aclOps := c.compareACLs("backend", name, currentBackend.ACLList, desiredBackend.ACLList, summary)
		appendOperationsIfNotEmpty(&operations, aclOps, &backendModified)

		// Compare HTTP request rules within this backend
		requestRuleOps := c.compareHTTPRequestRules("backend", name, currentBackend.HTTPRequestRuleList, desiredBackend.HTTPRequestRuleList)
		appendOperationsIfNotEmpty(&operations, requestRuleOps, &backendModified)

		// Compare HTTP response rules within this backend
		responseRuleOps := c.compareHTTPResponseRules("backend", name, currentBackend.HTTPResponseRuleList, desiredBackend.HTTPResponseRuleList)
		appendOperationsIfNotEmpty(&operations, responseRuleOps, &backendModified)

		// Compare TCP request rules within this backend
		tcpRequestRuleOps := c.compareTCPRequestRules("backend", name, currentBackend.TCPRequestRuleList, desiredBackend.TCPRequestRuleList)
		appendOperationsIfNotEmpty(&operations, tcpRequestRuleOps, &backendModified)

		// Compare TCP response rules within this backend
		tcpResponseRuleOps := c.compareTCPResponseRules(name, currentBackend.TCPResponseRuleList, desiredBackend.TCPResponseRuleList)
		appendOperationsIfNotEmpty(&operations, tcpResponseRuleOps, &backendModified)

		// Compare log targets within this backend
		logTargetOps := c.compareLogTargets("backend", name, currentBackend.LogTargetList, desiredBackend.LogTargetList)
		appendOperationsIfNotEmpty(&operations, logTargetOps, &backendModified)

		// Compare stick rules within this backend
		stickRuleOps := c.compareStickRules(name, currentBackend.StickRuleList, desiredBackend.StickRuleList)
		appendOperationsIfNotEmpty(&operations, stickRuleOps, &backendModified)

		// Compare HTTP after response rules within this backend
		httpAfterRuleOps := c.compareHTTPAfterResponseRules(name, currentBackend.HTTPAfterResponseRuleList, desiredBackend.HTTPAfterResponseRuleList)
		appendOperationsIfNotEmpty(&operations, httpAfterRuleOps, &backendModified)

		// Compare server switching rules within this backend
		serverSwitchingRuleOps := c.compareServerSwitchingRules(name, currentBackend.ServerSwitchingRuleList, desiredBackend.ServerSwitchingRuleList)
		appendOperationsIfNotEmpty(&operations, serverSwitchingRuleOps, &backendModified)

		// Compare filters within this backend
		filterOps := c.compareFilters("backend", name, currentBackend.FilterList, desiredBackend.FilterList)
		appendOperationsIfNotEmpty(&operations, filterOps, &backendModified)

		// Compare HTTP checks within this backend
		httpCheckOps := c.compareHTTPChecks(name, currentBackend.HTTPCheckList, desiredBackend.HTTPCheckList)
		appendOperationsIfNotEmpty(&operations, httpCheckOps, &backendModified)

		// Compare TCP checks within this backend
		tcpCheckOps := c.compareTCPChecks(name, currentBackend.TCPCheckRuleList, desiredBackend.TCPCheckRuleList)
		appendOperationsIfNotEmpty(&operations, tcpCheckOps, &backendModified)

		// Compare server templates within this backend using pointer indexes
		serverTemplateOps := c.compareServerTemplatesWithIndex(name, current.ServerTemplateIndex[name], desired.ServerTemplateIndex[name])
		appendOperationsIfNotEmpty(&operations, serverTemplateOps, &backendModified)

		// Compare backend attributes (excluding servers, ACLs, and rules which we already compared)
		if diffFields := backendBaseDiffFields(currentBackend, desiredBackend); len(diffFields) > 0 {
			operations = append(operations, sections.NewBackendUpdate(desiredBackend))
			backendModified = true
			summary.BackendDiffFields[name] = diffFields
		}

		if backendModified {
			summary.BackendsModified = append(summary.BackendsModified, name)
		}
	}

	return operations
}

// compareServersWithIndex compares server configurations within a backend using
// pointer indexes. Add/delete/modify side effects on summary live in the
// factory closures so the per-server walk happens exactly once.
func (c *Comparator) compareServersWithIndex(backendName string, currentServers, desiredServers map[string]*models.Server, summary *DiffSummary) []Operation {
	return compareNamedMaps(
		currentServers, desiredServers,
		func(a, b *models.Server) bool { return a.Equal(*b) },
		func(s *models.Server) Operation {
			summary.ServersAdded[backendName] = append(summary.ServersAdded[backendName], s.Name)
			return sections.NewServerCreate(backendName, s)
		},
		func(s *models.Server) Operation {
			summary.ServersDeleted[backendName] = append(summary.ServersDeleted[backendName], s.Name)
			return sections.NewServerDelete(backendName, s)
		},
		func(s *models.Server) Operation {
			summary.ServersModified[backendName] = append(summary.ServersModified[backendName], s.Name)
			return sections.NewServerUpdate(backendName, currentServers[s.Name], s)
		},
	)
}

// compareServerTemplatesWithIndex compares server template configurations using pointer indexes for zero-copy iteration.
func (c *Comparator) compareServerTemplatesWithIndex(backendName string, currentTemplates, desiredTemplates map[string]*models.ServerTemplate) []Operation {
	return compareNamedMaps(
		currentTemplates, desiredTemplates,
		func(a, b *models.ServerTemplate) bool { return a.Equal(*b) },
		func(t *models.ServerTemplate) Operation { return sections.NewServerTemplateCreate(backendName, t) },
		func(t *models.ServerTemplate) Operation { return sections.NewServerTemplateDelete(backendName, t) },
		func(t *models.ServerTemplate) Operation { return sections.NewServerTemplateUpdate(backendName, t) },
	)
}

// clearNestedCollections zeroes all nested collection fields on a Backend copy
// so they don't affect attribute-level comparison.
func clearNestedCollections(b *models.Backend) {
	b.Servers = nil
	b.ACLList = nil
	b.HTTPRequestRuleList = nil
	b.HTTPResponseRuleList = nil
	b.HTTPAfterResponseRuleList = nil
	b.TCPRequestRuleList = nil
	b.TCPResponseRuleList = nil
	b.ServerSwitchingRuleList = nil
	b.LogTargetList = nil
	b.StickRuleList = nil
	b.FilterList = nil
	b.HTTPCheckList = nil
	b.TCPCheckRuleList = nil
	b.ServerTemplates = nil
}

// backendBaseDiffFields returns the list of BackendBase field names that differ
// between two backends (excluding nested collections which are compared separately).
// Returns nil if the backends are equal.
func backendBaseDiffFields(b1, b2 *models.Backend) []string {
	b1Copy := *b1
	b2Copy := *b2
	clearNestedCollections(&b1Copy)
	clearNestedCollections(&b2Copy)

	if b1Copy.Equal(b2Copy) {
		return nil
	}

	// Use client-native's Diff to identify which BackendBase fields differ.
	diff := b1Copy.BackendBase.Diff(b2Copy.BackendBase)
	fields := make([]string, 0, len(diff))
	for field := range diff {
		fields = append(fields, field)
	}
	return fields
}
