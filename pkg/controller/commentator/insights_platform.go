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

package commentator

import (
	"fmt"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/events"
	busevents "gitlab.com/haproxy-haptic/haptic/pkg/events"
)

// leaderInsight handles LeaderElectionStarted, BecameLeader, LostLeadership,
// and NewLeaderObserved events.
func (ec *EventCommentator) leaderInsight(event busevents.Event, attrs []any) (insight string, args []any) {
	switch e := event.(type) {
	case *events.LeaderElectionStartedEvent:
		return fmt.Sprintf("Leader election started: identity=%s, lease=%s/%s",
				e.Identity, e.LeaseNamespace, e.LeaseName),
			append(attrs,
				"identity", e.Identity,
				"lease_name", e.LeaseName,
				"lease_namespace", e.LeaseNamespace)

	case *events.BecameLeaderEvent:
		return fmt.Sprintf("Became leader: %s", e.Identity),
			append(attrs, "identity", e.Identity)

	case *events.LostLeadershipEvent:
		reasonMsg := ""
		if e.Reason != "" {
			reasonMsg = fmt.Sprintf(" (reason: %s)", e.Reason)
		}
		return fmt.Sprintf("Lost leadership: %s%s", e.Identity, reasonMsg),
			append(attrs,
				"identity", e.Identity,
				"reason", e.Reason)

	case *events.NewLeaderObservedEvent:
		observerMsg := "another replica"
		if e.IsSelf {
			observerMsg = "this replica"
		}
		return fmt.Sprintf("New leader observed: %s (%s)",
				e.NewLeaderIdentity, observerMsg),
			append(attrs,
				"leader_identity", e.NewLeaderIdentity,
				"is_self", e.IsSelf)

	default:
		return "", attrs
	}
}

// statusInsight handles StatusUpdateCompleted and StatusUpdateFailed events.
func (ec *EventCommentator) statusInsight(event busevents.Event, attrs []any) (insight string, args []any) {
	switch e := event.(type) {
	case *events.StatusUpdateCompletedEvent:
		return fmt.Sprintf("Status patches applied (%s phase): %d applied, %d skipped (%dms)",
				e.Phase, e.AppliedCount, e.SkippedCount, e.DurationMs),
			append(attrs,
				"phase", string(e.Phase),
				"applied", e.AppliedCount,
				"skipped", e.SkippedCount,
				"duration_ms", e.DurationMs)

	case *events.StatusUpdateFailedEvent:
		retriableInfo := ""
		if e.Retriable {
			retriableInfo = " (retriable)"
		}
		return fmt.Sprintf("Status patch failed for %s/%s [%s]%s: %s",
				e.Namespace, e.Name, e.GVR, retriableInfo, e.Error),
			append(attrs,
				"namespace", e.Namespace,
				"name", e.Name,
				"gvr", e.GVR,
				"error", e.Error,
				"retriable", e.Retriable)

	default:
		return "", attrs
	}
}
