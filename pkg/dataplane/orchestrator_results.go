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
		return "create"
	case sections.OperationUpdate:
		return "update"
	case sections.OperationDelete:
		return "delete"
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
	ops = append(ops, fileDiffToOperations(auxDiffs.fileDiff)...)
	ops = append(ops, sslDiffToOperations(auxDiffs.sslDiff)...)
	ops = append(ops, caFileDiffToOperations(auxDiffs.caFileDiff)...)
	ops = append(ops, mapDiffToOperations(auxDiffs.mapDiff)...)
	ops = append(ops, crtlistDiffToOperations(auxDiffs.crtlistDiff)...)
	return ops
}

// fileDiffToOperations converts general file diffs to AppliedOperations.
func fileDiffToOperations(diff *auxiliaryfiles.FileDiff) []AppliedOperation {
	if diff == nil {
		return nil
	}
	ops := make([]AppliedOperation, 0, len(diff.ToCreate)+len(diff.ToUpdate)+len(diff.ToDelete))
	for _, f := range diff.ToCreate {
		ops = append(ops, AppliedOperation{
			Type:        "create",
			Section:     "file",
			Resource:    f.Filename,
			Description: "Created general file " + f.Filename,
		})
	}
	for _, f := range diff.ToUpdate {
		ops = append(ops, AppliedOperation{
			Type:        "update",
			Section:     "file",
			Resource:    f.Filename,
			Description: "Updated general file " + f.Filename,
		})
	}
	for _, path := range diff.ToDelete {
		ops = append(ops, AppliedOperation{
			Type:        "delete",
			Section:     "file",
			Resource:    path,
			Description: "Deleted general file " + path,
		})
	}
	return ops
}

// sslDiffToOperations converts SSL certificate diffs to AppliedOperations.
func sslDiffToOperations(diff *auxiliaryfiles.SSLCertificateDiff) []AppliedOperation {
	if diff == nil {
		return nil
	}
	ops := make([]AppliedOperation, 0, len(diff.ToCreate)+len(diff.ToUpdate)+len(diff.ToDelete))
	for _, c := range diff.ToCreate {
		ops = append(ops, AppliedOperation{
			Type:        "create",
			Section:     "ssl-cert",
			Resource:    c.Path,
			Description: "Created SSL certificate " + c.Path,
		})
	}
	for _, c := range diff.ToUpdate {
		ops = append(ops, AppliedOperation{
			Type:        "update",
			Section:     "ssl-cert",
			Resource:    c.Path,
			Description: "Updated SSL certificate " + c.Path,
		})
	}
	for _, path := range diff.ToDelete {
		ops = append(ops, AppliedOperation{
			Type:        "delete",
			Section:     "ssl-cert",
			Resource:    path,
			Description: "Deleted SSL certificate " + path,
		})
	}
	return ops
}

// caFileDiffToOperations converts SSL CA file diffs to AppliedOperations.
func caFileDiffToOperations(diff *auxiliaryfiles.SSLCaFileDiff) []AppliedOperation {
	if diff == nil {
		return nil
	}
	ops := make([]AppliedOperation, 0, len(diff.ToCreate)+len(diff.ToUpdate)+len(diff.ToDelete))
	for _, c := range diff.ToCreate {
		ops = append(ops, AppliedOperation{
			Type:        "create",
			Section:     "ssl-ca",
			Resource:    c.Path,
			Description: "Created SSL CA file " + c.Path,
		})
	}
	for _, c := range diff.ToUpdate {
		ops = append(ops, AppliedOperation{
			Type:        "update",
			Section:     "ssl-ca",
			Resource:    c.Path,
			Description: "Updated SSL CA file " + c.Path,
		})
	}
	for _, path := range diff.ToDelete {
		ops = append(ops, AppliedOperation{
			Type:        "delete",
			Section:     "ssl-ca",
			Resource:    path,
			Description: "Deleted SSL CA file " + path,
		})
	}
	return ops
}

// mapDiffToOperations converts map file diffs to AppliedOperations.
func mapDiffToOperations(diff *auxiliaryfiles.MapFileDiff) []AppliedOperation {
	if diff == nil {
		return nil
	}
	ops := make([]AppliedOperation, 0, len(diff.ToCreate)+len(diff.ToUpdate)+len(diff.ToDelete))
	for _, m := range diff.ToCreate {
		ops = append(ops, AppliedOperation{
			Type:        "create",
			Section:     "map",
			Resource:    m.Path,
			Description: "Created map file " + m.Path,
		})
	}
	for _, m := range diff.ToUpdate {
		ops = append(ops, AppliedOperation{
			Type:        "update",
			Section:     "map",
			Resource:    m.Path,
			Description: "Updated map file " + m.Path,
		})
	}
	for _, path := range diff.ToDelete {
		ops = append(ops, AppliedOperation{
			Type:        "delete",
			Section:     "map",
			Resource:    path,
			Description: "Deleted map file " + path,
		})
	}
	return ops
}

// crtlistDiffToOperations converts CRT-list file diffs to AppliedOperations.
func crtlistDiffToOperations(diff *auxiliaryfiles.CRTListDiff) []AppliedOperation {
	if diff == nil {
		return nil
	}
	ops := make([]AppliedOperation, 0, len(diff.ToCreate)+len(diff.ToUpdate)+len(diff.ToDelete))
	for _, c := range diff.ToCreate {
		ops = append(ops, AppliedOperation{
			Type:        "create",
			Section:     "crt-list",
			Resource:    c.Path,
			Description: "Created crt-list file " + c.Path,
		})
	}
	for _, c := range diff.ToUpdate {
		ops = append(ops, AppliedOperation{
			Type:        "update",
			Section:     "crt-list",
			Resource:    c.Path,
			Description: "Updated crt-list file " + c.Path,
		})
	}
	for _, path := range diff.ToDelete {
		ops = append(ops, AppliedOperation{
			Type:        "delete",
			Section:     "crt-list",
			Resource:    path,
			Description: "Deleted crt-list file " + path,
		})
	}
	return ops
}
