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

// LeaderElectionStartedEvent is published when leader election is initiated.
type LeaderElectionStartedEvent struct {
	Identity       string
	LeaseName      string
	LeaseNamespace string
	timestamped
}

// NewLeaderElectionStartedEvent creates a new LeaderElectionStartedEvent.
func NewLeaderElectionStartedEvent(identity, leaseName, leaseNamespace string) *LeaderElectionStartedEvent {
	return &LeaderElectionStartedEvent{
		Identity:       identity,
		LeaseName:      leaseName,
		LeaseNamespace: leaseNamespace,
		timestamped:    newTimestamped(),
	}
}

func (e *LeaderElectionStartedEvent) EventType() string { return EventTypeLeaderElectionStarted }

// BecameLeaderEvent is published when this replica becomes the leader.
type BecameLeaderEvent struct {
	Identity string
	timestamped
}

// NewBecameLeaderEvent creates a new BecameLeaderEvent.
func NewBecameLeaderEvent(identity string) *BecameLeaderEvent {
	return &BecameLeaderEvent{
		Identity:    identity,
		timestamped: newTimestamped(),
	}
}

func (e *BecameLeaderEvent) EventType() string { return EventTypeBecameLeader }

// LostLeadershipEvent is published when this replica loses leadership.
type LostLeadershipEvent struct {
	Identity string
	Reason   string // graceful_shutdown, lease_expired, etc.
	timestamped
}

// NewLostLeadershipEvent creates a new LostLeadershipEvent.
func NewLostLeadershipEvent(identity, reason string) *LostLeadershipEvent {
	return &LostLeadershipEvent{
		Identity:    identity,
		Reason:      reason,
		timestamped: newTimestamped(),
	}
}

func (e *LostLeadershipEvent) EventType() string { return EventTypeLostLeadership }

// NewLeaderObservedEvent is published when a new leader is observed.
type NewLeaderObservedEvent struct {
	NewLeaderIdentity string
	IsSelf            bool // true if this replica is the new leader
	timestamped
}

// NewNewLeaderObservedEvent creates a new NewLeaderObservedEvent.
func NewNewLeaderObservedEvent(newLeaderIdentity string, isSelf bool) *NewLeaderObservedEvent {
	return &NewLeaderObservedEvent{
		NewLeaderIdentity: newLeaderIdentity,
		IsSelf:            isSelf,
		timestamped:       newTimestamped(),
	}
}

func (e *NewLeaderObservedEvent) EventType() string { return EventTypeNewLeaderObserved }
