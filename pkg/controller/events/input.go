// Copyright 2026 Philipp Hossner
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
	"slices"

	corev1 "k8s.io/api/core/v1"
)

// InputRejection identifies a resource revision excluded from accepted inputs.
type InputRejection struct {
	Object  corev1.ObjectReference
	Deleted bool
	Reason  string
}

// WatchedInputsRejectedEvent carries the complete current rejection set.
type WatchedInputsRejectedEvent struct {
	Rejections []InputRejection
	timestamped
}

// NewWatchedInputsRejectedEvent creates a detached rejection notification.
func NewWatchedInputsRejectedEvent(rejections []InputRejection) *WatchedInputsRejectedEvent {
	return &WatchedInputsRejectedEvent{Rejections: slices.Clone(rejections), timestamped: newTimestamped()}
}

func (e *WatchedInputsRejectedEvent) EventType() string { return EventTypeWatchedInputsRejected }
