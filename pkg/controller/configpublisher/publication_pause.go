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

package configpublisher

import "context"

func (c *Component) publicationAllowed() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return !c.publicationBlocked && !c.gatePinned
}

func (c *Component) deferOrDiscardPublication(ctx context.Context, work *publishWorkItem) {
	c.mu.RLock()
	deferReceipt := ctx.Err() == nil && (c.publicationBlocked || c.gatePinned) &&
		(work.term == 0 || work.term == c.publicationTerm) && work.deployDriven &&
		work.entry != nil && work.entry.confirmedPod != nil
	c.mu.RUnlock()
	if deferReceipt {
		c.requeueDeployedFront(work)
		return
	}
	c.discardCachedConfig(work.correlationID)
}

func (c *Component) resumeAcknowledgedPublication() {
	if !c.publicationAllowed() {
		return
	}
	select {
	case c.deployedTrigger <- struct{}{}:
	default:
	}
}
