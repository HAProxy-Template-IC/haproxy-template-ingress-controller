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

// WebhookValidationRequestEvent is published when an admission request is received.
type WebhookValidationRequestEvent struct {
	RequestUID string
	Kind       string
	Name       string
	Namespace  string
	Operation  string
	timestamped
}

// NewWebhookValidationRequestEvent creates a new WebhookValidationRequestEvent.
func NewWebhookValidationRequestEvent(requestUID, kind, name, namespace, operation string) *WebhookValidationRequestEvent {
	return &WebhookValidationRequestEvent{
		RequestUID:  requestUID,
		Kind:        kind,
		Name:        name,
		Namespace:   namespace,
		Operation:   operation,
		timestamped: newTimestamped(),
	}
}

func (e *WebhookValidationRequestEvent) EventType() string {
	return EventTypeWebhookValidationRequest
}

// WebhookValidationAllowedEvent is published when a resource is admitted.
type WebhookValidationAllowedEvent struct {
	RequestUID string
	Kind       string
	Name       string
	Namespace  string
	timestamped
}

// NewWebhookValidationAllowedEvent creates a new WebhookValidationAllowedEvent.
func NewWebhookValidationAllowedEvent(requestUID, kind, name, namespace string) *WebhookValidationAllowedEvent {
	return &WebhookValidationAllowedEvent{
		RequestUID:  requestUID,
		Kind:        kind,
		Name:        name,
		Namespace:   namespace,
		timestamped: newTimestamped(),
	}
}

func (e *WebhookValidationAllowedEvent) EventType() string {
	return EventTypeWebhookValidationAllowed
}

// WebhookValidationDeniedEvent is published when a resource is denied.
type WebhookValidationDeniedEvent struct {
	RequestUID string
	Kind       string
	Name       string
	Namespace  string
	Reason     string
	timestamped
}

// NewWebhookValidationDeniedEvent creates a new WebhookValidationDeniedEvent.
func NewWebhookValidationDeniedEvent(requestUID, kind, name, namespace, reason string) *WebhookValidationDeniedEvent {
	return &WebhookValidationDeniedEvent{
		RequestUID:  requestUID,
		Kind:        kind,
		Name:        name,
		Namespace:   namespace,
		Reason:      reason,
		timestamped: newTimestamped(),
	}
}

func (e *WebhookValidationDeniedEvent) EventType() string {
	return EventTypeWebhookValidationDenied
}

// WebhookValidationErrorEvent is published when validation encounters an error.
type WebhookValidationErrorEvent struct {
	RequestUID string
	Kind       string
	Error      string
	timestamped
}

// NewWebhookValidationErrorEvent creates a new WebhookValidationErrorEvent.
func NewWebhookValidationErrorEvent(requestUID, kind, errorMsg string) *WebhookValidationErrorEvent {
	return &WebhookValidationErrorEvent{
		RequestUID:  requestUID,
		Kind:        kind,
		Error:       errorMsg,
		timestamped: newTimestamped(),
	}
}

func (e *WebhookValidationErrorEvent) EventType() string { return EventTypeWebhookValidationError }
