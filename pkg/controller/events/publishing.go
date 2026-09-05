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
	"fmt"
	"time"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/rendercycle"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/renderoutput"
	busevents "gitlab.com/haproxy-haptic/haptic/pkg/events"
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
	PodUID                 string
	PodRuntimeID           string
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

	// SyncDuration is how long the sync operation took.
	SyncDuration time.Duration

	// OperationCounts provides a breakdown of operations performed.
	OperationCounts OperationCounts

	// AppliedPlanID is the render plan the pod accepted in this sync.
	AppliedPlanID      string
	AppliedPlanProof   string
	AppliedRenderProof string

	// RunningPlanID is the render plan the pod's running HAProxy serves after
	// this sync. It trails AppliedPlanID while a reload is still pending.
	RunningPlanID    string
	RunningPlanProof string

	// Mode is how the plan was applied: runtime, file_only, reload, scheduled,
	// noop or rejected. Empty until the agent reports it.
	Mode string

	// Reasons explain Mode, most significant first.
	Reasons []string

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
func NewConfigAppliedToPodEvent(runtimeConfigName, runtimeConfigNamespace, podName, podNamespace, podUID, podRuntimeID, checksum string, isDriftCheck bool, syncMetadata *SyncMetadata) *ConfigAppliedToPodEvent {
	return &ConfigAppliedToPodEvent{
		RuntimeConfigName:      runtimeConfigName,
		RuntimeConfigNamespace: runtimeConfigNamespace,
		PodName:                podName,
		PodNamespace:           podNamespace,
		PodUID:                 podUID,
		PodRuntimeID:           podRuntimeID,
		Checksum:               checksum,
		IsDriftCheck:           isDriftCheck,
		SyncMetadata:           syncMetadata,
		timestamped:            newTimestamped(),
	}
}

func (e *ConfigAppliedToPodEvent) EventType() string { return EventTypeConfigAppliedToPod }

// DeployedConfigPublishRequest publishes the exact render occurrence acknowledged by a pod.
type DeployedConfigPublishRequest struct {
	renderOccurrenceCarrier

	CycleSnapshot          *rendercycle.Snapshot
	OutputSnapshot         *renderoutput.Snapshot
	RuntimeConfigName      string
	RuntimeConfigNamespace string
	Config                 string
	AuxiliaryFiles         *dataplane.AuxiliaryFiles
	ContentChecksum        string

	timestamped
}

// NewDeployedConfigPublishRequestWithCycle publishes one exact deployed occurrence.
func NewDeployedConfigPublishRequestWithCycle(
	runtimeConfigName, runtimeConfigNamespace string,
	occurrence *rendercycle.Occurrence,
) (*DeployedConfigPublishRequest, error) {
	carrier, identity, err := inspectRenderOccurrence(occurrence)
	if err != nil {
		return nil, fmt.Errorf("deployed config publish request: %w", err)
	}
	event := &DeployedConfigPublishRequest{
		renderOccurrenceCarrier: carrier,
		RuntimeConfigName:       runtimeConfigName,
		RuntimeConfigNamespace:  runtimeConfigNamespace,
		timestamped:             newTimestamped(),
	}
	owned := withDeployedConfigPublishIdentity(event, identity)
	return &owned, nil
}

func (e *DeployedConfigPublishRequest) EventType() string {
	return EventTypeDeployedConfigPublishRequest
}

// CloneForSubscriber restores authenticated shadows and isolates legacy files.
func (e *DeployedConfigPublishRequest) CloneForSubscriber() busevents.Event {
	if e == nil {
		panic("cannot clone nil deployed config publish request")
	}
	clone := *e
	clone.AuxiliaryFiles = dataplane.CloneAuxiliaryFiles(e.AuxiliaryFiles)
	if e.occurrence != nil {
		clone = withDeployedConfigPublishIdentity(&clone, mustInspectRenderOccurrence(e.renderOccurrenceCarrier))
	}
	return &clone
}

func withDeployedConfigPublishIdentity(
	source *DeployedConfigPublishRequest,
	identity *renderOccurrenceIdentity,
) DeployedConfigPublishRequest {
	event := *source
	event.CycleSnapshot = identity.cycle
	event.OutputSnapshot = identity.output
	event.Config = ""
	event.AuxiliaryFiles = nil
	event.ContentChecksum = identity.contentChecksum
	return event
}
