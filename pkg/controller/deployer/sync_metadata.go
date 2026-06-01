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

package deployer

import (
	"fmt"
	"math"
	"slices"
	"strings"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/events"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane"
)

// formatBackendDiffFields produces a compact summary of which BackendBase fields
// caused backend updates, grouped by field signature.
func formatBackendDiffFields(diffFields map[string][]string) string {
	if len(diffFields) == 0 {
		return ""
	}

	// Group by field signature for compact output.
	groups := make(map[string]int) // "Field1, Field2" -> count
	for _, fields := range diffFields {
		slices.Sort(fields)
		key := strings.Join(fields, ", ")
		groups[key]++
	}

	parts := make([]string, 0, len(groups))
	for fields, count := range groups {
		noun := "backends"
		if count == 1 {
			noun = "backend"
		}
		parts = append(parts, fmt.Sprintf("[%s] (%d %s)", fields, count, noun))
	}
	slices.Sort(parts)
	return strings.Join(parts, ", ")
}

// safeIntToInt32 converts int to int32 with bounds checking to prevent overflow.
func safeIntToInt32(n int) int32 {
	if n > math.MaxInt32 {
		return math.MaxInt32
	}
	if n < math.MinInt32 {
		return math.MinInt32
	}
	return int32(n)
}

// syncResultToMetadata converts dataplane.SyncResult to events.SyncMetadata.
// Package-level (no receiver state) so both the structural deploy path and the
// runtime-raw bypass can build the metadata for ConfigAppliedToPodEvent.
func syncResultToMetadata(result *dataplane.SyncResult) *events.SyncMetadata {
	if result == nil {
		return nil
	}

	// Count total servers added/removed/modified across all backends
	totalServersAdded := 0
	for _, servers := range result.Details.ServersAdded {
		totalServersAdded += len(servers)
	}
	totalServersRemoved := 0
	for _, servers := range result.Details.ServersDeleted {
		totalServersRemoved += len(servers)
	}
	totalServersModified := 0
	for _, servers := range result.Details.ServersModified {
		totalServersModified += len(servers)
	}

	return &events.SyncMetadata{
		ReloadTriggered: result.ReloadTriggered,
		ReloadID:        result.ReloadID,
		SyncDuration:    result.Duration,
		OperationCounts: events.OperationCounts{
			TotalAPIOperations: result.Details.TotalOperations,
			BackendsAdded:      len(result.Details.BackendsAdded),
			BackendsRemoved:    len(result.Details.BackendsDeleted),
			BackendsModified:   len(result.Details.BackendsModified),
			ServersAdded:       totalServersAdded,
			ServersRemoved:     totalServersRemoved,
			ServersModified:    totalServersModified,
			FrontendsAdded:     len(result.Details.FrontendsAdded),
			FrontendsRemoved:   len(result.Details.FrontendsDeleted),
			FrontendsModified:  len(result.Details.FrontendsModified),
		},
		Error: "", // Empty on success
	}
}
