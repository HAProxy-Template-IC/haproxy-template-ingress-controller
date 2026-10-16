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

package deployer

import (
	"context"

	"github.com/haproxy-haptic/HAPTIC/pkg/controller/rendercycle"
)

// Drain stops dispatching any plan other than the one last dispatched, which
// is the plan the deployment intent names, and returns once that deployment
// and its pending-reload follow-ups have completed: a paced reload confirms
// its worker receipt only on the follow-up.
func (s *DeploymentScheduler) Drain(ctx context.Context) error {
	s.schedulerMutex.Lock()
	// Checked under the lock Start resets the drain state with, so a drain
	// whose term already ended cannot latch the next one.
	if err := ctx.Err(); err != nil {
		s.schedulerMutex.Unlock()
		return err
	}
	s.draining = true
	s.drainOccurrence = s.state.lastScheduled
	if s.state.pending != nil && !s.drainAdmitsLocked(s.state.pending.occurrence) {
		s.state.pending = nil
	}
	s.schedulerMutex.Unlock()

	for {
		s.schedulerMutex.Lock()
		settled := !s.dispatching && s.state.pending == nil && !s.followUpArmed
		s.schedulerMutex.Unlock()
		if settled {
			return nil
		}
		select {
		case <-s.drainProgress:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func (s *DeploymentScheduler) drainAdmitsLocked(occurrence *rendercycle.Occurrence) bool {
	return !s.draining || samePlan(occurrence, s.drainOccurrence)
}

func (s *DeploymentScheduler) signalDrainProgressLocked() {
	select {
	case s.drainProgress <- struct{}{}:
	default:
	}
}
