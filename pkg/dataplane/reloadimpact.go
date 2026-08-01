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
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/parser"
)

// ReloadImpact is the offline verdict on deploying one rendered configuration
// (config + auxiliary files) over another: whether it would reload HAProxy or
// apply over the runtime socket, and the changes behind that verdict.
type ReloadImpact struct {
	// ConfigChanged is true when the haproxy.cfg itself differs.
	ConfigChanged bool
	// WouldReload is true when deploying requires an HAProxy reload.
	WouldReload bool
	// StructuralOps is the number of config operations that force a reload
	// (everything except runtime-eligible server field updates).
	StructuralOps int
	// ServerFieldUpdates counts runtime-eligible server field updates
	// (address / port / weight / maintenance / agent-check) — applied over the
	// runtime socket with no reload.
	ServerFieldUpdates int
	// MapUpdates / CertUpdates name the map files and certificates whose content
	// changed and can be pushed over the runtime socket without a reload.
	MapUpdates  []string
	CertUpdates []string
	// ReloadFreeFileUpdates names the general files written or rewritten without
	// a reload because they carry reloadOnPush=false — a sidecar owns them and
	// HAProxy never reads them.
	ReloadFreeFileUpdates []string
	// AuxForcesReload is true when an auxiliary change (map/cert create or delete,
	// a general file, a crt-list, or a cert content update below v3.2) forces the
	// reload rather than a runtime push.
	AuxForcesReload bool
	// Summary is the full per-section config diff (frontends/backends/servers/…).
	Summary comparator.DiffSummary
}

// ComputeReloadImpact reports whether deploying `desired` over `baseline` would
// reload HAProxy, using the SAME decision the deployer makes: the config
// comparator's structural operations OR any auxiliary change that isn't a
// runtime-eligible map/cert content update (auxiliaryFileDiffs.runtimeEligibleAuxUpdates).
//
// It is a pure, client-free function — no DataPlane API connection — so the
// playground and offline validation can preview the deploy cost of a config
// change without a live HAProxy. `caps` selects version-dependent runtime
// support (e.g. runtime SSL-cert updates on v3.2+).
//
// `desiredConfigText` is the rendered configuration `desired` was parsed from.
// A deleted auxiliary file's name is searched for in it (see fileReferences),
// so passing "" makes every delete report a reload.
func ComputeReloadImpact(baseline, desired *parser.StructuredConfig, baselineAux, desiredAux *AuxiliaryFiles, desiredConfigText string, caps Capabilities) (*ReloadImpact, error) {
	diff, err := comparator.New().Compare(baseline, desired)
	if err != nil {
		return nil, err
	}
	structural := diff.Summary.StructuralOperations()
	serverUpdates := 0
	for _, servers := range diff.Summary.ServersModified {
		serverUpdates += len(servers)
	}

	aux := buildAuxiliaryFileDiffs(baselineAux, desiredAux)
	aux.references = newFileReferences(desiredConfigText, desiredAux)
	mapUpd, certUpd, _, auxNeedsReload := aux.runtimeEligibleAuxUpdates(caps)

	return &ReloadImpact{
		ConfigChanged:         diff.Summary.HasChanges(),
		WouldReload:           structural > 0 || auxNeedsReload,
		StructuralOps:         structural,
		ServerFieldUpdates:    serverUpdates,
		MapUpdates:            fileIdentifiers(mapUpd),
		CertUpdates:           fileIdentifiers(certUpd),
		ReloadFreeFileUpdates: fileIdentifiers(aux.reloadFreeGeneralFiles()),
		AuxForcesReload:       auxNeedsReload,
		Summary:               diff.Summary,
	}, nil
}

// buildAuxiliaryFileDiffs diffs two rendered auxiliary-file sets in memory (no
// DataPlane client), the pure-data equivalent of the per-type Compare*
// functions that otherwise diff a desired set against the live worker.
func buildAuxiliaryFileDiffs(baseline, desired *AuxiliaryFiles) *auxiliaryFileDiffs {
	if baseline == nil {
		baseline = &AuxiliaryFiles{}
	}
	if desired == nil {
		desired = &AuxiliaryFiles{}
	}
	return &auxiliaryFileDiffs{
		fileDiff:    diffAuxFiles(baseline.GeneralFiles, desired.GeneralFiles),
		sslDiff:     diffAuxFiles(baseline.SSLCertificates, desired.SSLCertificates),
		caFileDiff:  diffAuxFiles(baseline.SSLCaFiles, desired.SSLCaFiles),
		mapDiff:     diffAuxFiles(baseline.MapFiles, desired.MapFiles),
		crtlistDiff: diffAuxFiles(baseline.CRTListFiles, desired.CRTListFiles),
	}
}

// diffAuxFiles computes the create/update/delete diff between two in-memory file
// sets, keyed by identifier: absent in current -> create, present with different
// content -> update, present in current but not desired -> delete.
func diffAuxFiles[T auxiliaryfiles.FileItem](current, desired []T) *auxiliaryfiles.FileDiffGeneric[T] {
	diff := &auxiliaryfiles.FileDiffGeneric[T]{}
	cur := make(map[string]T, len(current))
	for _, f := range current {
		cur[f.GetIdentifier()] = f
	}
	seen := make(map[string]bool, len(desired))
	for _, d := range desired {
		id := d.GetIdentifier()
		seen[id] = true
		if c, ok := cur[id]; !ok {
			diff.ToCreate = append(diff.ToCreate, d)
		} else if c.GetContent() != d.GetContent() {
			diff.ToUpdate = append(diff.ToUpdate, d)
		}
	}
	for _, c := range current {
		if !seen[c.GetIdentifier()] {
			diff.ToDelete = append(diff.ToDelete, c.GetIdentifier())
		}
	}
	return diff
}

func fileIdentifiers[T auxiliaryfiles.FileItem](files []T) []string {
	ids := make([]string, len(files))
	for i, f := range files {
		ids[i] = f.GetIdentifier()
	}
	return ids
}
