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

package events

import "time"

// -----------------------------------------------------------------------------
// Config Publishing Events.
// -----------------------------------------------------------------------------

// ConfigPublishedEvent is published after runtime configuration resources are created/updated.
//
// This is a non-critical event - publishing failures do not affect controller operation.
type ConfigPublishedEvent struct {
	RuntimeConfigName      string
	RuntimeConfigNamespace string
	MapFileCount           int
	SecretCount            int
	timestamp              time.Time
}

// NewConfigPublishedEvent creates a new ConfigPublishedEvent.
func NewConfigPublishedEvent(runtimeConfigName, runtimeConfigNamespace string, mapFileCount, secretCount int) *ConfigPublishedEvent {
	return &ConfigPublishedEvent{
		RuntimeConfigName:      runtimeConfigName,
		RuntimeConfigNamespace: runtimeConfigNamespace,
		MapFileCount:           mapFileCount,
		SecretCount:            secretCount,
		timestamp:              time.Now(),
	}
}

func (e *ConfigPublishedEvent) EventType() string    { return EventTypeConfigPublished }
func (e *ConfigPublishedEvent) Timestamp() time.Time { return e.timestamp }

// ConfigPublishFailedEvent is published when runtime configuration publishing fails.
//
// This is logged but does not affect controller operation.
type ConfigPublishFailedEvent struct {
	Error     string
	timestamp time.Time
}

// NewConfigPublishFailedEvent creates a new ConfigPublishFailedEvent.
func NewConfigPublishFailedEvent(err error) *ConfigPublishFailedEvent {
	errStr := ""
	if err != nil {
		errStr = err.Error()
	}

	return &ConfigPublishFailedEvent{
		Error:     errStr,
		timestamp: time.Now(),
	}
}

func (e *ConfigPublishFailedEvent) EventType() string    { return EventTypeConfigPublishFailed }
func (e *ConfigPublishFailedEvent) Timestamp() time.Time { return e.timestamp }

// ConfigAppliedToPodEvent is published after configuration is successfully applied to an HAProxy pod.
//
// This triggers updating the deployment status in runtime config resources.
type ConfigAppliedToPodEvent struct {
	RuntimeConfigName      string
	RuntimeConfigNamespace string
	PodName                string
	PodNamespace           string
	Checksum               string

	// IsDriftCheck indicates whether this was a drift prevention check (GET-only)
	// or an actual sync operation (POST/PUT/DELETE).
	//
	// True:  Drift check - no actual changes were made, just verified config is current
	// False: Actual sync - configuration was written to HAProxy
	IsDriftCheck bool

	// SyncMetadata contains detailed information about the sync operation.
	// Only populated for actual syncs (IsDriftCheck=false).
	SyncMetadata *SyncMetadata

	timestamp time.Time
}

// SyncMetadata contains detailed information about a sync operation.
type SyncMetadata struct {
	// ReloadTriggered indicates whether HAProxy was reloaded during this sync.
	// Reloads occur for structural changes via transaction API (status 202).
	// Runtime-only changes don't trigger reloads (status 200).
	ReloadTriggered bool

	// ReloadID is the reload identifier from HAProxy dataplane API.
	// Only populated when ReloadTriggered is true.
	ReloadID string

	// SyncDuration is how long the sync operation took.
	SyncDuration time.Duration

	// VersionConflictRetries is the number of retries due to version conflicts.
	// HAProxy's dataplane API uses optimistic concurrency control.
	VersionConflictRetries int

	// FallbackUsed indicates whether incremental sync failed and a full
	// raw configuration push was used instead.
	FallbackUsed bool

	// OperationCounts provides a breakdown of operations performed.
	OperationCounts OperationCounts

	// Error contains the error message if sync failed.
	// Empty string indicates success.
	Error string
}

// OperationCounts provides statistics about sync operations.
type OperationCounts struct {
	// Config operations
	TotalAPIOperations int
	BackendsAdded      int
	BackendsRemoved    int
	BackendsModified   int
	ServersAdded       int
	ServersRemoved     int
	ServersModified    int
	FrontendsAdded     int
	FrontendsRemoved   int
	FrontendsModified  int

	// Auxiliary file operations
	MapsAdded            int
	MapsRemoved          int
	MapsModified         int
	SSLCertsAdded        int
	SSLCertsRemoved      int
	SSLCertsModified     int
	GeneralFilesAdded    int
	GeneralFilesRemoved  int
	GeneralFilesModified int
}

// NewConfigAppliedToPodEvent creates a new ConfigAppliedToPodEvent.
func NewConfigAppliedToPodEvent(runtimeConfigName, runtimeConfigNamespace, podName, podNamespace, checksum string, isDriftCheck bool, syncMetadata *SyncMetadata) *ConfigAppliedToPodEvent {
	return &ConfigAppliedToPodEvent{
		RuntimeConfigName:      runtimeConfigName,
		RuntimeConfigNamespace: runtimeConfigNamespace,
		PodName:                podName,
		PodNamespace:           podNamespace,
		Checksum:               checksum,
		IsDriftCheck:           isDriftCheck,
		SyncMetadata:           syncMetadata,
		timestamp:              time.Now(),
	}
}

func (e *ConfigAppliedToPodEvent) EventType() string    { return EventTypeConfigAppliedToPod }
func (e *ConfigAppliedToPodEvent) Timestamp() time.Time { return e.timestamp }
