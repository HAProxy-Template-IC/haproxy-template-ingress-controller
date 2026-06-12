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

package deployer

import (
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/events"
)

// handleDeploymentCancelRequest cancels an in-progress deployment if the correlation ID matches.
func (c *Component) handleDeploymentCancelRequest(event *events.DeploymentCancelRequestEvent) {
	correlationID := event.CorrelationID()

	c.cancelMu.Lock()
	defer c.cancelMu.Unlock()

	// Check if there's an active deployment with matching correlation ID
	if c.activeCorrelationID == "" || c.activeCancelFunc == nil {
		c.Logger().Debug("Received cancel request but no deployment in progress",
			"requested_correlation_id", correlationID,
			"reason", event.Reason)
		return
	}

	if c.activeCorrelationID != correlationID {
		c.Logger().Debug("Received cancel request but correlation ID does not match",
			"requested_correlation_id", correlationID,
			"active_correlation_id", c.activeCorrelationID,
			"reason", event.Reason)
		return
	}

	c.Logger().Info("Cancelling in-progress deployment",
		"correlation_id", correlationID,
		"reason", event.Reason)

	// Cancel the deployment context
	c.activeCancelFunc()
}

// cancelActiveDeployment cancels any active deployment regardless of correlation ID.
// Used for graceful shutdown.
func (c *Component) cancelActiveDeployment(reason string) {
	c.cancelMu.Lock()
	defer c.cancelMu.Unlock()

	if c.activeCancelFunc == nil {
		return
	}

	c.Logger().Info("Cancelling active deployment",
		"correlation_id", c.activeCorrelationID,
		"reason", reason)

	c.activeCancelFunc()

	// Wait for deployment to complete if deploymentDone channel exists
	if c.deploymentDone != nil {
		c.cancelMu.Unlock()
		<-c.deploymentDone
		c.cancelMu.Lock()
	}
}
