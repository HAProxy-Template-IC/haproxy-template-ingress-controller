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
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane"
)

// handleDeploymentCancelRequest cancels an in-progress deployment if the correlation ID matches.
func (c *Component) handleDeploymentCancelRequest(event *events.DeploymentCancelRequestEvent) {
	correlationID := event.CorrelationID()

	c.cancelMu.Lock()
	defer c.cancelMu.Unlock()

	// Check if there's an active deployment with matching correlation ID
	if c.activeCorrelationID == "" || c.activeCancelFunc == nil {
		c.logger.Debug("Received cancel request but no deployment in progress",
			"requested_correlation_id", correlationID,
			"reason", event.Reason)
		return
	}

	if c.activeCorrelationID != correlationID {
		c.logger.Debug("Received cancel request but correlation ID does not match",
			"requested_correlation_id", correlationID,
			"active_correlation_id", c.activeCorrelationID,
			"reason", event.Reason)
		return
	}

	c.logger.Info("Cancelling in-progress deployment",
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

	c.logger.Info("Cancelling active deployment",
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

// isNoOpDeployment returns true if this deployment made no meaningful changes
// that warrant publishing a ConfigAppliedToPodEvent.
//
// We skip event publishing when ALL of the following are true:
//   - No operations were performed (TotalOperations=0)
//   - No HAProxy reload was triggered.
//
// This applies to both drift checks and regular reconciliation-triggered deployments.
// If HAProxy state is unchanged (no operations, no reload), there's no need to
// update RuntimeConfig status, regardless of what triggered the deployment.
func (c *Component) isNoOpDeployment(syncResult *dataplane.SyncResult) bool {
	if syncResult == nil {
		return true
	}

	// Always publish if operations were performed
	if syncResult.Details.TotalOperations > 0 {
		return false
	}

	// Always publish if reload was triggered
	if syncResult.ReloadTriggered {
		return false
	}

	// No meaningful changes - safe to skip
	return true
}
