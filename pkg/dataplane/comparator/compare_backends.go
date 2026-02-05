package comparator

import (
	"github.com/haproxytech/client-native/v6/models"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/comparator/sections"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/parser"
)

// compareBackends compares backend configurations between current and desired.
// Uses pointer indexes from StructuredConfig for zero-copy iteration over servers and server templates.
func (c *Comparator) compareBackends(current, desired *parser.StructuredConfig, summary *DiffSummary) []Operation {
	// Pre-allocate with estimated capacity (roughly 2 ops per backend for add/modify + servers)
	operations := make([]Operation, 0, len(desired.Backends)*2)

	// Build maps for easier comparison
	currentBackends := make(map[string]*models.Backend, len(current.Backends))
	for _, backend := range current.Backends {
		if backend.Name != "" {
			currentBackends[backend.Name] = backend
		}
	}

	desiredBackends := make(map[string]*models.Backend)
	for _, backend := range desired.Backends {
		if backend.Name != "" {
			desiredBackends[backend.Name] = backend
		}
	}

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
	emptyServers := make(map[string]*models.Server)
	operations = append(operations, c.compareServersWithIndex(name, emptyServers, desiredServers, summary)...)

	// Server templates - use pointer index for zero-copy iteration
	emptyTemplates := make(map[string]*models.ServerTemplate)
	operations = append(operations, c.compareServerTemplatesWithIndex(name, emptyTemplates, desiredTemplates)...)

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
		currentServers, desiredServers := getServerIndexes(name, current.ServerIndex, desired.ServerIndex)
		serverOps := c.compareServersWithIndex(name, currentServers, desiredServers, summary)
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
		currentTemplates, desiredTemplates := getServerTemplateIndexes(name, current.ServerTemplateIndex, desired.ServerTemplateIndex)
		serverTemplateOps := c.compareServerTemplatesWithIndex(name, currentTemplates, desiredTemplates)
		appendOperationsIfNotEmpty(&operations, serverTemplateOps, &backendModified)

		// Compare backend attributes (excluding servers, ACLs, and rules which we already compared)
		if !backendsEqualWithoutNestedCollections(currentBackend, desiredBackend) {
			operations = append(operations, sections.NewBackendUpdate(desiredBackend))
			backendModified = true
		}

		if backendModified {
			summary.BackendsModified = append(summary.BackendsModified, name)
		}
	}

	return operations
}

// compareServersWithIndex compares server configurations within a backend using pointer indexes.
func (c *Comparator) compareServersWithIndex(backendName string, currentServers, desiredServers map[string]*models.Server, summary *DiffSummary) []Operation {
	// Pre-allocate with capacity for potential changes (add + delete + modify)
	maxOps := len(currentServers) + len(desiredServers)
	operations := make([]Operation, 0, maxOps)

	// Find added servers
	addedOps := c.compareAddedServersWithIndex(backendName, currentServers, desiredServers, summary)
	operations = append(operations, addedOps...)

	// Find deleted servers
	deletedOps := c.compareDeletedServersWithIndex(backendName, currentServers, desiredServers, summary)
	operations = append(operations, deletedOps...)

	// Find modified servers
	modifiedOps := c.compareModifiedServersWithIndex(backendName, currentServers, desiredServers, summary)
	operations = append(operations, modifiedOps...)

	return operations
}

// compareAddedServersWithIndex compares added servers using pointer indexes for zero-copy iteration.
func (c *Comparator) compareAddedServersWithIndex(backendName string, currentServers, desiredServers map[string]*models.Server, summary *DiffSummary) []Operation {
	// Pre-allocate with capacity for potential additions
	operations := make([]Operation, 0, len(desiredServers))

	for name, server := range desiredServers {
		if _, exists := currentServers[name]; !exists {
			operations = append(operations, sections.NewServerCreate(backendName, server))
			if summary.ServersAdded[backendName] == nil {
				summary.ServersAdded[backendName] = []string{}
			}
			summary.ServersAdded[backendName] = append(summary.ServersAdded[backendName], name)
		}
	}

	return operations
}

// compareDeletedServersWithIndex compares deleted servers using pointer indexes for zero-copy iteration.
func (c *Comparator) compareDeletedServersWithIndex(backendName string, currentServers, desiredServers map[string]*models.Server, summary *DiffSummary) []Operation {
	// Pre-allocate with capacity for potential deletions
	operations := make([]Operation, 0, len(currentServers))

	for name, server := range currentServers {
		if _, exists := desiredServers[name]; !exists {
			operations = append(operations, sections.NewServerDelete(backendName, server))
			if summary.ServersDeleted[backendName] == nil {
				summary.ServersDeleted[backendName] = []string{}
			}
			summary.ServersDeleted[backendName] = append(summary.ServersDeleted[backendName], name)
		}
	}

	return operations
}

// compareModifiedServersWithIndex compares modified servers using pointer indexes for zero-copy iteration.
func (c *Comparator) compareModifiedServersWithIndex(backendName string, currentServers, desiredServers map[string]*models.Server, summary *DiffSummary) []Operation {
	// Pre-allocate with capacity for potential modifications
	operations := make([]Operation, 0, len(desiredServers))

	for name, desiredServer := range desiredServers {
		currentServer, exists := currentServers[name]
		if !exists {
			continue
		}

		// Compare server attributes using built-in Equal() method
		if !serversEqual(currentServer, desiredServer) {
			operations = append(operations, sections.NewServerUpdate(backendName, desiredServer))
			if summary.ServersModified[backendName] == nil {
				summary.ServersModified[backendName] = []string{}
			}
			summary.ServersModified[backendName] = append(summary.ServersModified[backendName], name)
		}
	}

	return operations
}

// compareServerTemplatesWithIndex compares server template configurations using pointer indexes for zero-copy iteration.
func (c *Comparator) compareServerTemplatesWithIndex(backendName string, currentTemplates, desiredTemplates map[string]*models.ServerTemplate) []Operation {
	// Pre-allocate with capacity for potential changes (add + delete + modify)
	maxOps := len(currentTemplates) + len(desiredTemplates)
	operations := make([]Operation, 0, maxOps)

	// Find added server templates
	for prefix, template := range desiredTemplates {
		if _, exists := currentTemplates[prefix]; !exists {
			operations = append(operations, sections.NewServerTemplateCreate(backendName, template))
		}
	}

	// Find deleted server templates
	for prefix, template := range currentTemplates {
		if _, exists := desiredTemplates[prefix]; !exists {
			operations = append(operations, sections.NewServerTemplateDelete(backendName, template))
		}
	}

	// Find modified server templates
	for prefix, desiredTemplate := range desiredTemplates {
		currentTemplate, exists := currentTemplates[prefix]
		if !exists {
			continue
		}
		// Compare server template attributes using Equal() method
		if !serverTemplatesEqual(currentTemplate, desiredTemplate) {
			operations = append(operations, sections.NewServerTemplateUpdate(backendName, desiredTemplate))
		}
	}

	return operations
}

// serversEqual checks if two servers are equal.
// Uses the HAProxy models' built-in Equal() method to compare ALL attributes.
// This approach automatically handles current and future server parameters without
// maintenance burden, since we sync the entire server line anyway.
func serversEqual(s1, s2 *models.Server) bool {
	return s1.Equal(*s2)
}

// backendsEqualWithoutNestedCollections checks if two backends are equal, excluding servers, ACLs, and HTTP rules.
// Uses the HAProxy models' built-in Equal() method to compare ALL backend attributes
// (mode, balance algorithm, timeouts, health checks, etc.) automatically, excluding nested collections we compare separately.
func backendsEqualWithoutNestedCollections(b1, b2 *models.Backend) bool {
	// Create copies to avoid modifying originals
	b1Copy := *b1
	b2Copy := *b2

	// Clear nested collections so they don't affect comparison
	b1Copy.Servers = nil
	b2Copy.Servers = nil
	b1Copy.ACLList = nil
	b2Copy.ACLList = nil
	b1Copy.HTTPRequestRuleList = nil
	b2Copy.HTTPRequestRuleList = nil
	b1Copy.HTTPResponseRuleList = nil
	b2Copy.HTTPResponseRuleList = nil
	b1Copy.HTTPAfterResponseRuleList = nil
	b2Copy.HTTPAfterResponseRuleList = nil
	b1Copy.TCPRequestRuleList = nil
	b2Copy.TCPRequestRuleList = nil
	b1Copy.TCPResponseRuleList = nil
	b2Copy.TCPResponseRuleList = nil
	b1Copy.ServerSwitchingRuleList = nil
	b2Copy.ServerSwitchingRuleList = nil
	b1Copy.LogTargetList = nil
	b2Copy.LogTargetList = nil
	b1Copy.StickRuleList = nil
	b2Copy.StickRuleList = nil
	b1Copy.FilterList = nil
	b2Copy.FilterList = nil
	b1Copy.HTTPCheckList = nil
	b2Copy.HTTPCheckList = nil
	b1Copy.TCPCheckRuleList = nil
	b2Copy.TCPCheckRuleList = nil
	b1Copy.ServerTemplates = nil
	b2Copy.ServerTemplates = nil

	return b1Copy.Equal(b2Copy)
}

// getServerIndexes retrieves server indexes for a backend, returning empty maps if not found.
func getServerIndexes(backendName string, currentIndex, desiredIndex map[string]map[string]*models.Server) (currentServers, desiredServers map[string]*models.Server) {
	currentServers = currentIndex[backendName]
	if currentServers == nil {
		currentServers = make(map[string]*models.Server)
	}
	desiredServers = desiredIndex[backendName]
	if desiredServers == nil {
		desiredServers = make(map[string]*models.Server)
	}
	return currentServers, desiredServers
}

// getServerTemplateIndexes retrieves server template indexes for a backend, returning empty maps if not found.
func getServerTemplateIndexes(backendName string, currentIndex, desiredIndex map[string]map[string]*models.ServerTemplate) (currentTemplates, desiredTemplates map[string]*models.ServerTemplate) {
	currentTemplates = currentIndex[backendName]
	if currentTemplates == nil {
		currentTemplates = make(map[string]*models.ServerTemplate)
	}
	desiredTemplates = desiredIndex[backendName]
	if desiredTemplates == nil {
		desiredTemplates = make(map[string]*models.ServerTemplate)
	}
	return currentTemplates, desiredTemplates
}
