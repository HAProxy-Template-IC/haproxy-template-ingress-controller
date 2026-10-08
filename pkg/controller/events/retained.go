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

// RetainedConfigEvent reports recovery activity or refusal on the template owner.
type RetainedConfigEvent struct {
	Namespace string
	Name      string
	UID       string
	Reason    string
	Message   string
	timestamped
}

const EventTypeRetainedConfig = "config.retained"

func NewRetainedConfigEvent(namespace, name, uid, reason, message string) *RetainedConfigEvent {
	return &RetainedConfigEvent{Namespace: namespace, Name: name, UID: uid, Reason: reason, Message: message, timestamped: newTimestamped()}
}

func (e *RetainedConfigEvent) EventType() string { return EventTypeRetainedConfig }
