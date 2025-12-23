package comparator

import (
	"github.com/haproxytech/client-native/v6/models"

	"haptic/pkg/dataplane/comparator/sections"
	"haptic/pkg/dataplane/parser"
)

// compareBackends compares backend configurations between current and desired.
//
// This is a focused implementation that handles the most common use case:
// backend and server management. It demonstrates the pattern for other sections.
func (c *Comparator) compareBackends(current, desired *parser.StructuredConfig, summary *DiffSummary) []Operation {
	var operations []Operation

	// Build maps for easier comparison
	currentBackends := make(map[string]*models.Backend)
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
	addedOps := c.compareAddedBackends(desiredBackends, currentBackends, summary)
	operations = append(operations, addedOps...)

	// Find deleted backends
	for name, backend := range currentBackends {
		if _, exists := desiredBackends[name]; !exists {
			operations = append(operations, sections.NewBackendDelete(backend))
			summary.BackendsDeleted = append(summary.BackendsDeleted, name)
		}
	}

	// Find modified backends
	modifiedOps := c.compareModifiedBackends(desiredBackends, currentBackends, summary)
	operations = append(operations, modifiedOps...)

	return operations
}

// compareAddedBackends compares added backends and creates operations for them and their nested elements.
func (c *Comparator) compareAddedBackends(desiredBackends, currentBackends map[string]*models.Backend, summary *DiffSummary) []Operation {
	// Pre-allocate with estimated capacity (at least one operation per backend)
	operations := make([]Operation, 0, len(desiredBackends))

	for name, backend := range desiredBackends {
		if _, exists := currentBackends[name]; exists {
			continue
		}
		operations = append(operations, sections.NewBackendCreate(backend))
		summary.BackendsAdded = append(summary.BackendsAdded, name)

		// Create operations for all nested elements in the new backend
		nestedOps := c.createNestedBackendOperations(name, backend, summary)
		operations = append(operations, nestedOps...)
	}

	return operations
}

// createNestedBackendOperations creates operations for all nested elements of a new backend.
// Compares against empty collections to generate create operations for each element.
func (c *Comparator) createNestedBackendOperations(name string, backend *models.Backend, summary *DiffSummary) []Operation {
	var operations []Operation

	// Create an empty backend to compare against for servers and server templates
	emptyBackend := &models.Backend{}
	emptyBackend.Name = name
	emptyBackend.Servers = make(map[string]models.Server)
	emptyBackend.ServerTemplates = make(map[string]models.ServerTemplate)

	// Servers and server templates
	operations = append(operations, c.compareServers(name, emptyBackend, backend, summary)...)
	operations = append(operations, c.compareServerTemplates(name, emptyBackend, backend)...)

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

// compareModifiedBackends compares modified backends and creates operations for changed nested elements.
func (c *Comparator) compareModifiedBackends(desiredBackends, currentBackends map[string]*models.Backend, summary *DiffSummary) []Operation {
	var operations []Operation

	for name, desiredBackend := range desiredBackends {
		currentBackend, exists := currentBackends[name]
		if !exists {
			continue
		}
		backendModified := false

		// Compare servers within this backend
		serverOps := c.compareServers(name, currentBackend, desiredBackend, summary)
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

		// Compare server templates within this backend
		serverTemplateOps := c.compareServerTemplates(name, currentBackend, desiredBackend)
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

// compareServers compares server configurations within a backend.
func (c *Comparator) compareServers(backendName string, currentBackend, desiredBackend *models.Backend, summary *DiffSummary) []Operation {
	var operations []Operation

	// Backend.Servers is already a map[string]models.Server
	currentServers := currentBackend.Servers
	desiredServers := desiredBackend.Servers

	// Find added servers
	addedOps := c.compareAddedServers(backendName, currentServers, desiredServers, summary)
	operations = append(operations, addedOps...)

	// Find deleted servers
	deletedOps := c.compareDeletedServers(backendName, currentServers, desiredServers, summary)
	operations = append(operations, deletedOps...)

	// Find modified servers
	modifiedOps := c.compareModifiedServers(backendName, currentServers, desiredServers, summary)
	operations = append(operations, modifiedOps...)

	return operations
}

// compareAddedServers compares added servers and creates operations for them.
func (c *Comparator) compareAddedServers(backendName string, currentServers, desiredServers map[string]models.Server, summary *DiffSummary) []Operation {
	var operations []Operation

	for name := range desiredServers {
		if _, exists := currentServers[name]; !exists {
			server := desiredServers[name]
			operations = append(operations, sections.NewServerCreate(backendName, &server))
			if summary.ServersAdded[backendName] == nil {
				summary.ServersAdded[backendName] = []string{}
			}
			summary.ServersAdded[backendName] = append(summary.ServersAdded[backendName], name)
		}
	}

	return operations
}

// compareDeletedServers compares deleted servers and creates operations for them.
func (c *Comparator) compareDeletedServers(backendName string, currentServers, desiredServers map[string]models.Server, summary *DiffSummary) []Operation {
	var operations []Operation

	for name := range currentServers {
		if _, exists := desiredServers[name]; !exists {
			server := currentServers[name]
			operations = append(operations, sections.NewServerDelete(backendName, &server))
			if summary.ServersDeleted[backendName] == nil {
				summary.ServersDeleted[backendName] = []string{}
			}
			summary.ServersDeleted[backendName] = append(summary.ServersDeleted[backendName], name)
		}
	}

	return operations
}

// compareModifiedServers compares modified servers and creates operations for them.
func (c *Comparator) compareModifiedServers(backendName string, currentServers, desiredServers map[string]models.Server, summary *DiffSummary) []Operation {
	var operations []Operation

	for name := range desiredServers {
		currentServer, exists := currentServers[name]
		if !exists {
			continue
		}
		desiredServer := desiredServers[name]

		// Compare server attributes
		// For now, we check if anything changed - future implementation
		// will do fine-grained attribute comparison
		if !serversEqual(&currentServer, &desiredServer) {
			operations = append(operations, sections.NewServerUpdate(backendName, &desiredServer))
			if summary.ServersModified[backendName] == nil {
				summary.ServersModified[backendName] = []string{}
			}
			summary.ServersModified[backendName] = append(summary.ServersModified[backendName], name)
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
