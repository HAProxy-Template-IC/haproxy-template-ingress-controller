// Copyright 2025 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package dataplane

import (
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/auxiliaryfiles"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/comparator"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/comparator/sections"
)

// Operation type string constants used in AppliedOperation and PlannedOperation.
const (
	opCreate = "create"
	opUpdate = "update"
	opDelete = "delete"
)

// Helper functions to convert internal types to public API types

func convertOperationsToApplied(ops []comparator.Operation) []AppliedOperation {
	applied := make([]AppliedOperation, 0, len(ops))
	for _, op := range ops {
		applied = append(applied, AppliedOperation{
			Type:        operationTypeToString(op.Type()),
			Section:     op.Section(),
			Resource:    extractResourceName(op),
			Description: op.Describe(),
		})
	}
	return applied
}

func convertOperationsToPlanned(ops []comparator.Operation) []PlannedOperation {
	planned := make([]PlannedOperation, 0, len(ops))
	for _, op := range ops {
		planned = append(planned, PlannedOperation{
			Type:        operationTypeToString(op.Type()),
			Section:     op.Section(),
			Resource:    extractResourceName(op),
			Description: op.Describe(),
			Priority:    op.Priority(),
		})
	}
	return planned
}

func operationTypeToString(opType sections.OperationType) string {
	switch opType {
	case sections.OperationCreate:
		return opCreate
	case sections.OperationUpdate:
		return opUpdate
	case sections.OperationDelete:
		return opDelete
	default:
		return "unknown"
	}
}

func extractResourceName(op comparator.Operation) string {
	desc := op.Describe()
	// Extract resource name from description (format: "Action section 'name'")
	// This is a simple heuristic - we look for text between single quotes
	start := -1
	for i, ch := range desc {
		if ch == '\'' {
			if start == -1 {
				start = i + 1
			} else {
				return desc[start:i]
			}
		}
	}
	return "unknown"
}

func convertDiffSummary(summary *comparator.DiffSummary) DiffDetails {
	return DiffDetails{
		TotalOperations:   summary.TotalOperations(),
		Creates:           summary.TotalCreates,
		Updates:           summary.TotalUpdates,
		Deletes:           summary.TotalDeletes,
		GlobalChanged:     summary.GlobalChanged,
		DefaultsChanged:   summary.DefaultsChanged,
		FrontendsAdded:    summary.FrontendsAdded,
		FrontendsModified: summary.FrontendsModified,
		FrontendsDeleted:  summary.FrontendsDeleted,
		BackendsAdded:     summary.BackendsAdded,
		BackendsModified:  summary.BackendsModified,
		BackendsDeleted:   summary.BackendsDeleted,
		BackendDiffFields: summary.BackendDiffFields,
		ServersAdded:      summary.ServersAdded,
		ServersModified:   summary.ServersModified,
		ServersDeleted:    summary.ServersDeleted,
		ACLsAdded:         make(map[string][]string),
		ACLsModified:      make(map[string][]string),
		ACLsDeleted:       make(map[string][]string),
		HTTPRulesAdded:    make(map[string]int),
		HTTPRulesModified: make(map[string]int),
		HTTPRulesDeleted:  make(map[string]int),
	}
}

// addAuxiliaryFileCounts populates auxiliary file counts in DiffDetails from auxiliary file diffs.
func addAuxiliaryFileCounts(details *DiffDetails, auxDiffs *auxiliaryFileDiffs) {
	if auxDiffs == nil {
		return
	}

	// General files
	if auxDiffs.fileDiff != nil {
		details.GeneralFilesAdded = len(auxDiffs.fileDiff.ToCreate)
		details.GeneralFilesModified = len(auxDiffs.fileDiff.ToUpdate)
		details.GeneralFilesDeleted = len(auxDiffs.fileDiff.ToDelete)
	}

	// SSL certificates
	if auxDiffs.sslDiff != nil {
		details.SSLCertsAdded = len(auxDiffs.sslDiff.ToCreate)
		details.SSLCertsModified = len(auxDiffs.sslDiff.ToUpdate)
		details.SSLCertsDeleted = len(auxDiffs.sslDiff.ToDelete)
	}

	// SSL CA files
	if auxDiffs.caFileDiff != nil {
		details.SSLCaFilesAdded = len(auxDiffs.caFileDiff.ToCreate)
		details.SSLCaFilesModified = len(auxDiffs.caFileDiff.ToUpdate)
		details.SSLCaFilesDeleted = len(auxDiffs.caFileDiff.ToDelete)
	}

	// Map files
	if auxDiffs.mapDiff != nil {
		details.MapsAdded = len(auxDiffs.mapDiff.ToCreate)
		details.MapsModified = len(auxDiffs.mapDiff.ToUpdate)
		details.MapsDeleted = len(auxDiffs.mapDiff.ToDelete)
	}
}

// auxDiffsToOperations converts auxiliary file diffs to AppliedOperations.
// This provides a consistent view of all operations (config + aux files) in SyncResult.AppliedOperations.
func auxDiffsToOperations(auxDiffs *auxiliaryFileDiffs) []AppliedOperation {
	if auxDiffs == nil {
		return nil
	}

	var ops []AppliedOperation
	if d := auxDiffs.fileDiff; d != nil {
		ops = append(ops, auxFileDiffToOperations(d.ToCreate, d.ToUpdate, d.ToDelete, "file", "general file")...)
	}
	if d := auxDiffs.sslDiff; d != nil {
		ops = append(ops, auxFileDiffToOperations(d.ToCreate, d.ToUpdate, d.ToDelete, "ssl-cert", "SSL certificate")...)
	}
	if d := auxDiffs.caFileDiff; d != nil {
		ops = append(ops, auxFileDiffToOperations(d.ToCreate, d.ToUpdate, d.ToDelete, "ssl-ca", "SSL CA file")...)
	}
	if d := auxDiffs.mapDiff; d != nil {
		ops = append(ops, auxFileDiffToOperations(d.ToCreate, d.ToUpdate, d.ToDelete, "map", "map file")...)
	}
	if d := auxDiffs.crtlistDiff; d != nil {
		ops = append(ops, auxFileDiffToOperations(d.ToCreate, d.ToUpdate, d.ToDelete, "crt-list", "crt-list file")...)
	}
	return ops
}

// auxFileDiffToOperations converts any auxiliary file diff to AppliedOperations.
// The section and label parameters identify the file type (e.g., "file"/"general file",
// "ssl-cert"/"SSL certificate").
func auxFileDiffToOperations[T auxiliaryfiles.FileItem](toCreate, toUpdate []T, toDelete []string, section, label string) []AppliedOperation {
	ops := make([]AppliedOperation, 0, len(toCreate)+len(toUpdate)+len(toDelete))
	for _, f := range toCreate {
		id := f.GetIdentifier()
		ops = append(ops, AppliedOperation{
			Type:        opCreate,
			Section:     section,
			Resource:    id,
			Description: "Created " + label + " " + id,
		})
	}
	for _, f := range toUpdate {
		id := f.GetIdentifier()
		ops = append(ops, AppliedOperation{
			Type:        opUpdate,
			Section:     section,
			Resource:    id,
			Description: "Updated " + label + " " + id,
		})
	}
	for _, path := range toDelete {
		ops = append(ops, AppliedOperation{
			Type:        opDelete,
			Section:     section,
			Resource:    path,
			Description: "Deleted " + label + " " + path,
		})
	}
	return ops
}
