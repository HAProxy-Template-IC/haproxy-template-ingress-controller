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

import (
	"time"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane"
)

// ConfigPublishedEvent is published after runtime configuration resources are created/updated.
//
// This is a non-critical event - publishing failures do not affect controller operation.
type ConfigPublishedEvent struct {
	RuntimeConfigName      string
	RuntimeConfigNamespace string
	MapFileCount           int
	SecretCount            int
	timestamped
}

// NewConfigPublishedEvent creates a new ConfigPublishedEvent.
func NewConfigPublishedEvent(runtimeConfigName, runtimeConfigNamespace string, mapFileCount, secretCount int) *ConfigPublishedEvent {
	return &ConfigPublishedEvent{
		RuntimeConfigName:      runtimeConfigName,
		RuntimeConfigNamespace: runtimeConfigNamespace,
		MapFileCount:           mapFileCount,
		SecretCount:            secretCount,
		timestamped:            newTimestamped(),
	}
}

func (e *ConfigPublishedEvent) EventType() string { return EventTypeConfigPublished }

// ConfigAppliedToPodEvent is published after configuration is successfully applied to a HAProxy pod.
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

	timestamped
}

// SyncMetadata contains detailed information about a sync operation.
type SyncMetadata struct {
	// ReloadTriggered indicates whether HAProxy was reloaded during this sync.
	// Reload path triggers a reload; runtime path doesn't.
	ReloadTriggered bool

	// ReloadID is the reload identifier from HAProxy dataplane API.
	// Only populated when ReloadTriggered is true.
	ReloadID string

	// SyncDuration is how long the sync operation took.
	SyncDuration time.Duration

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
		timestamped:            newTimestamped(),
	}
}

func (e *ConfigAppliedToPodEvent) EventType() string { return EventTypeConfigAppliedToPod }

// DeployedConfigPublishRequest asks the config-publisher to publish, as the
// HAProxyCfg spec, the exact config bytes the deployer just applied to the
// pods. It closes a CR self-consistency gap: the deployer stamps
// status.deployedToPods[].Checksum from these bytes (via
// dataplane.ComputeContentChecksum), but the validation-driven spec publish for
// that same render can be throttled/coalesced away under churn — leaving a
// pod's recorded checksum that never appears in any published spec.Checksum.
// Emitting this on every successful, non-drift deploy guarantees the deployed
// checksum becomes an observable published spec.
//
// Config/AuxiliaryFiles are the deployer's immutable post-render bytes; like
// DeploymentScheduledEvent, AuxiliaryFiles is carried by pointer (never mutated
// after rendering).
type DeployedConfigPublishRequest struct {
	RuntimeConfigName      string
	RuntimeConfigNamespace string
	Config                 string
	AuxiliaryFiles         *dataplane.AuxiliaryFiles
	ContentChecksum        string

	timestamped
}

// NewDeployedConfigPublishRequest creates a new DeployedConfigPublishRequest.
func NewDeployedConfigPublishRequest(runtimeConfigName, runtimeConfigNamespace, config string, auxFiles *dataplane.AuxiliaryFiles, contentChecksum string) *DeployedConfigPublishRequest {
	return &DeployedConfigPublishRequest{
		RuntimeConfigName:      runtimeConfigName,
		RuntimeConfigNamespace: runtimeConfigNamespace,
		Config:                 config,
		AuxiliaryFiles:         auxFiles,
		ContentChecksum:        contentChecksum,
		timestamped:            newTimestamped(),
	}
}

func (e *DeployedConfigPublishRequest) EventType() string {
	return EventTypeDeployedConfigPublishRequest
}
