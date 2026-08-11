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
	"context"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/events"
)

func (c *Component) flushPendingCancellationRequests() {
	for {
		select {
		case <-c.cancelEventChan:
		default:
			return
		}
	}
}

func (c *Component) runCancellationLoop(ctx context.Context, done chan<- struct{}) {
	defer close(done)
	for {
		select {
		case <-ctx.Done():
			return
		case event := <-c.cancelEventChan:
			request, ok := event.(*events.DeploymentCancelRequestEvent)
			if ok {
				c.handleDeploymentCancelRequest(request)
			}
		}
	}
}

// handleDeploymentCancelRequest cancels the exact in-progress deployment.
func (c *Component) handleDeploymentCancelRequest(event *events.DeploymentCancelRequestEvent) {
	c.cancelMu.Lock()
	defer c.cancelMu.Unlock()

	if c.activeDeploymentID == "" || c.activeCancelFunc == nil {
		c.Logger().Debug("Received cancel request but no deployment in progress",
			"requested_deployment_id", event.DeploymentID,
			"reason", event.Reason)
		return
	}

	if c.activeDeploymentID != event.DeploymentID {
		c.Logger().Debug("Received cancel request for a different deployment",
			"requested_deployment_id", event.DeploymentID,
			"active_deployment_id", c.activeDeploymentID,
			"reason", event.Reason)
		return
	}

	c.Logger().Info("Cancelling in-progress deployment",
		"deployment_id", event.DeploymentID,
		"correlation_id", c.activeCorrelationID,
		"reason", event.Reason)

	// Cancel the deployment context
	c.activeCancelFunc()
}

// cancelActiveDeployment cancels any active deployment regardless of its ID.
// Used for graceful shutdown.
func (c *Component) cancelActiveDeployment(reason string) {
	c.cancelMu.Lock()
	defer c.cancelMu.Unlock()

	if c.activeCancelFunc == nil {
		return
	}

	c.Logger().Info("Cancelling active deployment",
		"deployment_id", c.activeDeploymentID,
		"correlation_id", c.activeCorrelationID,
		"reason", reason)

	c.activeCancelFunc()

	// Wait for deployment to complete if deploymentDone channel exists
	done := c.deploymentDone
	if done != nil {
		c.cancelMu.Unlock()
		<-done
		c.cancelMu.Lock()
	}
}
