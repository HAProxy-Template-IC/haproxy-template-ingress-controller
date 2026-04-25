package comparator

import (
	"slices"

	"github.com/haproxytech/client-native/v6/models"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/comparator/sections"
)

// compareACLs compares ACL configurations within a frontend or backend.
// ACLs are identified by their name (ACLName field). Adds are emitted in
// ascending index order so the DataPlane API sees a valid insertion sequence
// (it requires lower indices to exist before higher ones).
func (c *Comparator) compareACLs(parentType, parentName string, currentACLs, desiredACLs models.Acls, _ *DiffSummary) []Operation {
	create, remove, update := sections.NewACLBackendCreate, sections.NewACLBackendDelete, sections.NewACLBackendUpdate
	if parentType == parentTypeFrontend {
		create, remove, update = sections.NewACLFrontendCreate, sections.NewACLFrontendDelete, sections.NewACLFrontendUpdate
	}

	currentByName := indexACLsByName(currentACLs)
	desiredByName := indexACLsByName(desiredACLs)

	// Adds: collect indices first, then sort, then emit in index order.
	var addIndices []int
	for name, idx := range desiredByName {
		if _, exists := currentByName[name]; !exists {
			addIndices = append(addIndices, idx)
		}
	}
	slices.Sort(addIndices)

	operations := make([]Operation, 0, len(addIndices)+len(currentByName))
	for _, idx := range addIndices {
		operations = append(operations, create(parentName, desiredACLs[idx], idx))
	}

	// Deletes: in current but not in desired.
	for name, idx := range currentByName {
		if _, exists := desiredByName[name]; !exists {
			operations = append(operations, remove(parentName, currentACLs[idx], idx))
		}
	}

	// Updates: in both, content differs.
	for name, desiredIdx := range desiredByName {
		currentIdx, exists := currentByName[name]
		if !exists {
			continue
		}
		if !currentACLs[currentIdx].Equal(*desiredACLs[desiredIdx]) {
			operations = append(operations, update(parentName, desiredACLs[desiredIdx], desiredIdx))
		}
	}

	return operations
}

// indexACLsByName builds a name → slice-index lookup, skipping ACLs without
// names (which can't be addressed by the DataPlane API anyway).
func indexACLsByName(acls models.Acls) map[string]int {
	index := make(map[string]int, len(acls))
	for i, acl := range acls {
		if acl.ACLName != "" {
			index[acl.ACLName] = i
		}
	}
	return index
}

// compareEditedItems runs an LCS-based diff (diffIndexedRules +
// collapseEdits) over current/desired and emits create/delete/update
// operations for the resulting edit script. Updates use the new value at the
// position of the old item, matching what collapseEdits already pairs up.
func compareEditedItems[T any](
	current, desired []T,
	equal func(T, T) bool,
	create func(item T, index int) Operation,
	remove func(item T, index int) Operation,
	update func(item T, index int) Operation,
) []Operation {
	diffs := diffIndexedRules(current, desired, equal)
	edits := collapseEdits(diffs)

	var operations []Operation
	for _, e := range edits {
		switch e.Op {
		case editInsert:
			operations = append(operations, create(e.New, e.NewIndex))
		case editDelete:
			operations = append(operations, remove(e.Old, e.OldIndex))
		case editUpdate:
			operations = append(operations, update(e.New, e.OldIndex))
		}
	}
	return operations
}

// compareHTTPRequestRules compares HTTP request rule configurations within a frontend or backend.
// Uses LCS-based content matching to produce minimal INSERT/DELETE/UPDATE operations
// instead of cascading UPDATEs caused by index shifts.
func (c *Comparator) compareHTTPRequestRules(parentType, parentName string, currentRules, desiredRules models.HTTPRequestRules) []Operation {
	create, remove, update := sections.NewHTTPRequestRuleBackendCreate, sections.NewHTTPRequestRuleBackendDelete, sections.NewHTTPRequestRuleBackendUpdate
	if parentType == parentTypeFrontend {
		create, remove, update = sections.NewHTTPRequestRuleFrontendCreate, sections.NewHTTPRequestRuleFrontendDelete, sections.NewHTTPRequestRuleFrontendUpdate
	}
	return compareEditedItems(
		currentRules, desiredRules,
		func(a, b *models.HTTPRequestRule) bool { return a.Equal(*b) },
		func(r *models.HTTPRequestRule, i int) Operation { return create(parentName, r, i) },
		func(r *models.HTTPRequestRule, i int) Operation { return remove(parentName, r, i) },
		func(r *models.HTTPRequestRule, i int) Operation { return update(parentName, r, i) },
	)
}

// compareHTTPResponseRules compares HTTP response rule configurations within a frontend or backend.
// Uses LCS-based content matching to produce minimal INSERT/DELETE/UPDATE operations.
func (c *Comparator) compareHTTPResponseRules(parentType, parentName string, currentRules, desiredRules models.HTTPResponseRules) []Operation {
	create, remove, update := sections.NewHTTPResponseRuleBackendCreate, sections.NewHTTPResponseRuleBackendDelete, sections.NewHTTPResponseRuleBackendUpdate
	if parentType == parentTypeFrontend {
		create, remove, update = sections.NewHTTPResponseRuleFrontendCreate, sections.NewHTTPResponseRuleFrontendDelete, sections.NewHTTPResponseRuleFrontendUpdate
	}
	return compareEditedItems(
		currentRules, desiredRules,
		func(a, b *models.HTTPResponseRule) bool { return a.Equal(*b) },
		func(r *models.HTTPResponseRule, i int) Operation { return create(parentName, r, i) },
		func(r *models.HTTPResponseRule, i int) Operation { return remove(parentName, r, i) },
		func(r *models.HTTPResponseRule, i int) Operation { return update(parentName, r, i) },
	)
}

// compareTCPRequestRules compares TCP request rule configurations within a frontend or backend.
// Uses LCS-based content matching to produce minimal INSERT/DELETE/UPDATE operations.
func (c *Comparator) compareTCPRequestRules(parentType, parentName string, currentRules, desiredRules models.TCPRequestRules) []Operation {
	create, remove, update := sections.NewTCPRequestRuleBackendCreate, sections.NewTCPRequestRuleBackendDelete, sections.NewTCPRequestRuleBackendUpdate
	if parentType == parentTypeFrontend {
		create, remove, update = sections.NewTCPRequestRuleFrontendCreate, sections.NewTCPRequestRuleFrontendDelete, sections.NewTCPRequestRuleFrontendUpdate
	}
	return compareEditedItems(
		currentRules, desiredRules,
		func(a, b *models.TCPRequestRule) bool { return a.Equal(*b) },
		func(r *models.TCPRequestRule, i int) Operation { return create(parentName, r, i) },
		func(r *models.TCPRequestRule, i int) Operation { return remove(parentName, r, i) },
		func(r *models.TCPRequestRule, i int) Operation { return update(parentName, r, i) },
	)
}

// compareTCPResponseRules compares TCP response rule configurations within a backend.
// Uses LCS-based content matching to produce minimal INSERT/DELETE/UPDATE operations.
func (c *Comparator) compareTCPResponseRules(parentName string, currentRules, desiredRules models.TCPResponseRules) []Operation {
	return compareEditedItems(
		currentRules, desiredRules,
		func(a, b *models.TCPResponseRule) bool { return a.Equal(*b) },
		func(r *models.TCPResponseRule, i int) Operation {
			return sections.NewTCPResponseRuleBackendCreate(parentName, r, i)
		},
		func(r *models.TCPResponseRule, i int) Operation {
			return sections.NewTCPResponseRuleBackendDelete(parentName, r, i)
		},
		func(r *models.TCPResponseRule, i int) Operation {
			return sections.NewTCPResponseRuleBackendUpdate(parentName, r, i)
		},
	)
}

// compareStickRules compares stick rule configurations within a backend.
// Uses LCS-based content matching to produce minimal INSERT/DELETE/UPDATE operations.
func (c *Comparator) compareStickRules(backendName string, currentRules, desiredRules models.StickRules) []Operation {
	return compareEditedItems(
		currentRules, desiredRules,
		func(a, b *models.StickRule) bool { return a.Equal(*b) },
		func(r *models.StickRule, i int) Operation {
			return sections.NewStickRuleBackendCreate(backendName, r, i)
		},
		func(r *models.StickRule, i int) Operation {
			return sections.NewStickRuleBackendDelete(backendName, r, i)
		},
		func(r *models.StickRule, i int) Operation {
			return sections.NewStickRuleBackendUpdate(backendName, r, i)
		},
	)
}

// compareHTTPAfterResponseRules compares HTTP after response rule configurations within a backend.
// Uses LCS-based content matching to produce minimal INSERT/DELETE/UPDATE operations.
func (c *Comparator) compareHTTPAfterResponseRules(backendName string, currentRules, desiredRules models.HTTPAfterResponseRules) []Operation {
	return compareEditedItems(
		currentRules, desiredRules,
		func(a, b *models.HTTPAfterResponseRule) bool { return a.Equal(*b) },
		func(r *models.HTTPAfterResponseRule, i int) Operation {
			return sections.NewHTTPAfterResponseRuleBackendCreate(backendName, r, i)
		},
		func(r *models.HTTPAfterResponseRule, i int) Operation {
			return sections.NewHTTPAfterResponseRuleBackendDelete(backendName, r, i)
		},
		func(r *models.HTTPAfterResponseRule, i int) Operation {
			return sections.NewHTTPAfterResponseRuleBackendUpdate(backendName, r, i)
		},
	)
}

// compareBackendSwitchingRules compares backend switching rule configurations within a frontend.
// Uses LCS-based content matching to produce minimal INSERT/DELETE/UPDATE operations.
func (c *Comparator) compareBackendSwitchingRules(frontendName string, currentRules, desiredRules models.BackendSwitchingRules) []Operation {
	return compareEditedItems(
		currentRules, desiredRules,
		func(a, b *models.BackendSwitchingRule) bool { return a.Equal(*b) },
		func(r *models.BackendSwitchingRule, i int) Operation {
			return sections.NewBackendSwitchingRuleFrontendCreate(frontendName, r, i)
		},
		func(r *models.BackendSwitchingRule, i int) Operation {
			return sections.NewBackendSwitchingRuleFrontendDelete(frontendName, r, i)
		},
		func(r *models.BackendSwitchingRule, i int) Operation {
			return sections.NewBackendSwitchingRuleFrontendUpdate(frontendName, r, i)
		},
	)
}

// compareServerSwitchingRules compares server switching rule configurations within a backend.
// Uses LCS-based content matching to produce minimal INSERT/DELETE/UPDATE operations.
func (c *Comparator) compareServerSwitchingRules(backendName string, currentRules, desiredRules models.ServerSwitchingRules) []Operation {
	return compareEditedItems(
		currentRules, desiredRules,
		func(a, b *models.ServerSwitchingRule) bool { return a.Equal(*b) },
		func(r *models.ServerSwitchingRule, i int) Operation {
			return sections.NewServerSwitchingRuleBackendCreate(backendName, r, i)
		},
		func(r *models.ServerSwitchingRule, i int) Operation {
			return sections.NewServerSwitchingRuleBackendDelete(backendName, r, i)
		},
		func(r *models.ServerSwitchingRule, i int) Operation {
			return sections.NewServerSwitchingRuleBackendUpdate(backendName, r, i)
		},
	)
}
