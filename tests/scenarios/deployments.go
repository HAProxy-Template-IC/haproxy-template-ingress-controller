// Copyright 2026 Philipp Hossner
// SPDX-License-Identifier: Apache-2.0

package scenarios

import (
	"context"
	"fmt"
	"strings"
	"time"

	appsv1 "k8s.io/api/apps/v1"

	"gitlab.com/haproxy-haptic/haptic/tests/testutil"
)

func (s *Session) Deployment(ctx context.Context, suffix string) (*appsv1.Deployment, error) {
	list, err := readJSON[appsv1.DeploymentList](ctx, s, "get", "deployments")
	if err != nil {
		return nil, err
	}
	var found *appsv1.Deployment
	for i := range list.Items {
		if strings.HasSuffix(list.Items[i].Name, suffix) {
			if found != nil {
				return nil, fmt.Errorf("multiple deployments end with %s", suffix)
			}
			found = &list.Items[i]
		}
	}
	if found == nil {
		return nil, fmt.Errorf("no deployment ends with %s", suffix)
	}
	return found, nil
}

func (s *Session) WaitControllerReady(ctx context.Context, timeout time.Duration) error {
	return poll(ctx, timeout, 5*time.Second, "controller deployment Ready", func(ctx context.Context) (testutil.PollResult, error) {
		deployment, err := s.Deployment(ctx, "controller")
		if err != nil {
			return testutil.PollPending, err
		}
		if deployment.Spec.Replicas == nil || *deployment.Spec.Replicas == 0 || deployment.Status.ReadyReplicas != *deployment.Spec.Replicas {
			return testutil.PollPending, fmt.Errorf("controller deployment %s is not Ready", deployment.Name)
		}
		return testutil.PollSucceeded, nil
	})
}

func (s *Session) ControllerRollout(ctx context.Context) error {
	deployment, err := s.Deployment(ctx, "controller")
	if err != nil {
		return err
	}
	_, err = s.Kube(ctx, nil, "rollout", "status", "deployment/"+deployment.Name, "--timeout=7m")
	return err
}
