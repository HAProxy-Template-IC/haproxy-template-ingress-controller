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
// Lifecycle Events.
// -----------------------------------------------------------------------------

// ControllerStartedEvent is published when the controller has completed startup.
// and all components are ready to process events.
type ControllerStartedEvent struct {
	ConfigVersion string
	SecretVersion string
	timestamp     time.Time
}

// NewControllerStartedEvent creates a new ControllerStartedEvent.
func NewControllerStartedEvent(configVersion, secretVersion string) *ControllerStartedEvent {
	return &ControllerStartedEvent{
		ConfigVersion: configVersion,
		SecretVersion: secretVersion,
		timestamp:     time.Now(),
	}
}

func (e *ControllerStartedEvent) EventType() string    { return EventTypeControllerStarted }
func (e *ControllerStartedEvent) Timestamp() time.Time { return e.timestamp }

// ControllerShutdownEvent is published when the controller is shutting down gracefully.
type ControllerShutdownEvent struct {
	Reason    string
	timestamp time.Time
}

// NewControllerShutdownEvent creates a new ControllerShutdownEvent.
func NewControllerShutdownEvent(reason string) *ControllerShutdownEvent {
	return &ControllerShutdownEvent{
		Reason:    reason,
		timestamp: time.Now(),
	}
}

func (e *ControllerShutdownEvent) EventType() string    { return EventTypeControllerShutdown }
func (e *ControllerShutdownEvent) Timestamp() time.Time { return e.timestamp }
