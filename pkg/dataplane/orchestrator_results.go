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
	"strings"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/auxiliaryfiles"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/comparator"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/comparator/sections"
)

// Operation type string constants used in AppliedOperation and PlannedOperation.
const (
	opCreate  = "create"
	opUpdate  = "update"
	opDelete  = "delete"
	opUnknown = "unknown"
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
		return opUnknown
	}
}

// extractResourceName parses the resource name out of the operation's
// description, which follows the convention "Action section 'name'".
// Returns "unknown" when the description doesn't contain a quoted name.
func extractResourceName(op comparator.Operation) string {
	_, after, found := strings.Cut(op.Describe(), "'")
	if !found {
		return opUnknown
	}
	name, _, found := strings.Cut(after, "'")
	if !found {
		return opUnknown
	}
	return name
}

func convertDiffSummary(summary *comparator.DiffSummary) DiffDetails {
	details := NewDiffDetails()
	details.TotalOperations = summary.TotalOperations()
	details.Creates = summary.TotalCreates
	details.Updates = summary.TotalUpdates
	details.Deletes = summary.TotalDeletes
	details.GlobalChanged = summary.GlobalChanged
	details.DefaultsChanged = summary.DefaultsChanged
	details.FrontendsAdded = summary.FrontendsAdded
	details.FrontendsModified = summary.FrontendsModified
	details.FrontendsDeleted = summary.FrontendsDeleted
	details.BackendsAdded = summary.BackendsAdded
	details.BackendsModified = summary.BackendsModified
	details.BackendsDeleted = summary.BackendsDeleted
	details.BackendDiffFields = summary.BackendDiffFields
	details.ServersAdded = summary.ServersAdded
	details.ServersModified = summary.ServersModified
	details.ServersDeleted = summary.ServersDeleted
	return details
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
