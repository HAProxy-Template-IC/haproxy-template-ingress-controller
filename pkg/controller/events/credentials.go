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

// SecretResourceChangedEvent is published when the Secret resource is added, updated, or deleted.
//
// This is a low-level event published directly by the SingleWatcher callback in the controller package.
// The CredentialsLoaderComponent subscribes to this event and handles parsing.
type SecretResourceChangedEvent struct {
	// Resource contains the raw Secret resource.
	// Type: interface{} to avoid circular dependencies.
	// Consumers should type-assert to *unstructured.Unstructured or *corev1.Secret.
	Resource interface{}

	timestamp time.Time
}

// NewSecretResourceChangedEvent creates a new SecretResourceChangedEvent.
func NewSecretResourceChangedEvent(resource interface{}) *SecretResourceChangedEvent {
	return &SecretResourceChangedEvent{
		Resource:  resource,
		timestamp: time.Now(),
	}
}

func (e *SecretResourceChangedEvent) EventType() string    { return EventTypeSecretResourceChanged }
func (e *SecretResourceChangedEvent) Timestamp() time.Time { return e.timestamp }

// CredentialsUpdatedEvent is published when credentials have been successfully.
// loaded and validated from the Secret.
type CredentialsUpdatedEvent struct {
	// Credentials contains the validated credentials.
	// Type: interface{} to avoid circular dependencies.
	// Consumers should type-assert to their expected credentials type.
	Credentials interface{}

	// SecretVersion is the resourceVersion of the Secret.
	SecretVersion string

	timestamp time.Time
}

// NewCredentialsUpdatedEvent creates a new CredentialsUpdatedEvent.
func NewCredentialsUpdatedEvent(credentials interface{}, secretVersion string) *CredentialsUpdatedEvent {
	return &CredentialsUpdatedEvent{
		Credentials:   credentials,
		SecretVersion: secretVersion,
		timestamp:     time.Now(),
	}
}

func (e *CredentialsUpdatedEvent) EventType() string    { return EventTypeCredentialsUpdated }
func (e *CredentialsUpdatedEvent) Timestamp() time.Time { return e.timestamp }

// CredentialsInvalidEvent is published when credential loading or validation fails.
//
// The controller will continue running with the previous valid credentials and wait.
// for the next Secret update.
type CredentialsInvalidEvent struct {
	SecretVersion string
	Error         string

	timestamp time.Time
}

// NewCredentialsInvalidEvent creates a new CredentialsInvalidEvent.
func NewCredentialsInvalidEvent(secretVersion, errMsg string) *CredentialsInvalidEvent {
	return &CredentialsInvalidEvent{
		SecretVersion: secretVersion,
		Error:         errMsg,
		timestamp:     time.Now(),
	}
}

func (e *CredentialsInvalidEvent) EventType() string    { return EventTypeCredentialsInvalid }
func (e *CredentialsInvalidEvent) Timestamp() time.Time { return e.timestamp }
