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

// SecretResourceChangedEvent is published when the Secret resource is added, updated, or deleted.
//
// This is a low-level event published directly by the SingleWatcher callback in the controller package.
// The CredentialsLoaderComponent subscribes to this event and handles parsing.
type SecretResourceChangedEvent struct {
	// Resource contains the raw Secret resource.
	// Type: any to avoid circular dependencies.
	// Consumers should type-assert to *unstructured.Unstructured or *corev1.Secret.
	Resource any

	timestamped
}

// NewSecretResourceChangedEvent creates a new SecretResourceChangedEvent.
func NewSecretResourceChangedEvent(resource any) *SecretResourceChangedEvent {
	return &SecretResourceChangedEvent{
		Resource:    resource,
		timestamped: newTimestamped(),
	}
}

func (e *SecretResourceChangedEvent) EventType() string { return EventTypeSecretResourceChanged }

// CredentialsUpdatedEvent is published when credentials have been successfully.
// loaded and validated from the Secret.
type CredentialsUpdatedEvent struct {
	// Credentials contains the validated credentials.
	// Type: any to avoid circular dependencies.
	// Consumers should type-assert to their expected credentials type.
	Credentials any

	// SecretVersion is the resourceVersion of the Secret.
	SecretVersion string

	timestamped
}

// NewCredentialsUpdatedEvent creates a new CredentialsUpdatedEvent.
func NewCredentialsUpdatedEvent(credentials any, secretVersion string) *CredentialsUpdatedEvent {
	return &CredentialsUpdatedEvent{
		Credentials:   credentials,
		SecretVersion: secretVersion,
		timestamped:   newTimestamped(),
	}
}

func (e *CredentialsUpdatedEvent) EventType() string { return EventTypeCredentialsUpdated }
