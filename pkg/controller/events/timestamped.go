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

// timestamped is an unexported embeddable type that provides the Timestamp()
// accessor required by every domain event. Events embed it by value so the
// method is promoted; callers cannot access or replace the inner value because
// the type itself is unexported.
type timestamped struct {
	ts time.Time
}

// newTimestamped returns a timestamped initialized to the current wall clock.
// It is the canonical way to populate the embed inside event constructors.
func newTimestamped() timestamped {
	return timestamped{ts: time.Now()}
}

// Timestamp returns the time at which the event was created.
func (t *timestamped) Timestamp() time.Time {
	return t.ts
}
