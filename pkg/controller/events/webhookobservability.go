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

// WebhookValidationRequestEvent is published when an admission request is received.
type WebhookValidationRequestEvent struct {
	RequestUID string
	Kind       string
	Name       string
	Namespace  string
	Operation  string
	timestamp  time.Time
}

// NewWebhookValidationRequestEvent creates a new WebhookValidationRequestEvent.
func NewWebhookValidationRequestEvent(requestUID, kind, name, namespace, operation string) *WebhookValidationRequestEvent {
	return &WebhookValidationRequestEvent{
		RequestUID: requestUID,
		Kind:       kind,
		Name:       name,
		Namespace:  namespace,
		Operation:  operation,
		timestamp:  time.Now(),
	}
}

func (e *WebhookValidationRequestEvent) EventType() string {
	return EventTypeWebhookValidationRequest
}
func (e *WebhookValidationRequestEvent) Timestamp() time.Time { return e.timestamp }

// WebhookValidationAllowedEvent is published when a resource is admitted.
type WebhookValidationAllowedEvent struct {
	RequestUID string
	Kind       string
	Name       string
	Namespace  string
	timestamp  time.Time
}

// NewWebhookValidationAllowedEvent creates a new WebhookValidationAllowedEvent.
func NewWebhookValidationAllowedEvent(requestUID, kind, name, namespace string) *WebhookValidationAllowedEvent {
	return &WebhookValidationAllowedEvent{
		RequestUID: requestUID,
		Kind:       kind,
		Name:       name,
		Namespace:  namespace,
		timestamp:  time.Now(),
	}
}

func (e *WebhookValidationAllowedEvent) EventType() string {
	return EventTypeWebhookValidationAllowed
}
func (e *WebhookValidationAllowedEvent) Timestamp() time.Time { return e.timestamp }

// WebhookValidationDeniedEvent is published when a resource is denied.
type WebhookValidationDeniedEvent struct {
	RequestUID string
	Kind       string
	Name       string
	Namespace  string
	Reason     string
	timestamp  time.Time
}

// NewWebhookValidationDeniedEvent creates a new WebhookValidationDeniedEvent.
func NewWebhookValidationDeniedEvent(requestUID, kind, name, namespace, reason string) *WebhookValidationDeniedEvent {
	return &WebhookValidationDeniedEvent{
		RequestUID: requestUID,
		Kind:       kind,
		Name:       name,
		Namespace:  namespace,
		Reason:     reason,
		timestamp:  time.Now(),
	}
}

func (e *WebhookValidationDeniedEvent) EventType() string {
	return EventTypeWebhookValidationDenied
}
func (e *WebhookValidationDeniedEvent) Timestamp() time.Time { return e.timestamp }

// WebhookValidationErrorEvent is published when validation encounters an error.
type WebhookValidationErrorEvent struct {
	RequestUID string
	Kind       string
	Error      string
	timestamp  time.Time
}

// NewWebhookValidationErrorEvent creates a new WebhookValidationErrorEvent.
func NewWebhookValidationErrorEvent(requestUID, kind, errorMsg string) *WebhookValidationErrorEvent {
	return &WebhookValidationErrorEvent{
		RequestUID: requestUID,
		Kind:       kind,
		Error:      errorMsg,
		timestamp:  time.Now(),
	}
}

func (e *WebhookValidationErrorEvent) EventType() string    { return EventTypeWebhookValidationError }
func (e *WebhookValidationErrorEvent) Timestamp() time.Time { return e.timestamp }
