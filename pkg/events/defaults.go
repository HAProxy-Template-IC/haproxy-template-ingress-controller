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

// Buffer size tiers for event subscriptions. Subscribers pick the tier that
// matches their expected inbound rate so tuning stays centralized and each
// component avoids defining its own magic number.
//
// When in doubt, start with StandardSubscriberBuffer. Move to
// HighVolumeSubscriberBuffer once the component is shown to fan in events
// from many resource types, and to PublishingSubscriberBuffer only for
// components that briefly buffer bursts of outbound-adjacent events.
const (
	// LowVolumeSubscriberBuffer suits components that receive at most a
	// handful of events per reconciliation cycle (for example, config
	// validators that respond to a single request event).
	LowVolumeSubscriberBuffer = 10

	// StandardSubscriberBuffer is the default tier for most controller
	// components - roughly one event per second under normal load.
	StandardSubscriberBuffer = 50

	// HighVolumeSubscriberBuffer covers components that fan in many
	// resource-change or reconciliation-triggered events (the reconciler
	// debouncer, pod discovery).
	HighVolumeSubscriberBuffer = 100

	// PublishingSubscriberBuffer absorbs bursts from publishing paths that
	// batch many small events back-to-back before draining.
	PublishingSubscriberBuffer = 200

	// ResourceChurnSubscriberBuffer covers the subscribers whose input arrives
	// once per resource change: a bulk apply, a namespace teardown or a fleet
	// rolling restart delivers hundreds back-to-back, faster than any handler
	// is scheduled to drain them. These subscribers are critical, so a drop
	// ends the controller iteration rather than losing one event — which is
	// why the tier is sized for the burst rather than the average.
	ResourceChurnSubscriberBuffer = 1000

	// DebugSubscriberBuffer is used for debug/introspection subscriptions
	// that tap every event flowing through the bus.
	DebugSubscriberBuffer = 1000
)
