// Copyright 2026 Philipp Hossner
// SPDX-License-Identifier: Apache-2.0

package scenarios

import (
	"context"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"gitlab.com/haproxy-haptic/haptic/tests/testutil"
)

func (s *Session) Pods(ctx context.Context, component string) ([]corev1.Pod, error) {
	list, err := readJSON[corev1.PodList](ctx, s, "get", "pods", "-l", "app.kubernetes.io/component="+component)
	return list.Items, err
}

func readyPods(pods []corev1.Pod) (testutil.PollResult, error) {
	if len(pods) == 0 {
		return testutil.PollPending, fmt.Errorf("no HAProxy pods")
	}
	for i := range pods {
		pod := &pods[i]
		if pod.DeletionTimestamp != nil || len(pod.Status.ContainerStatuses) != len(pod.Spec.Containers) || len(pod.Spec.Containers) == 0 {
			return testutil.PollPending, fmt.Errorf("pod %s is incomplete or terminating", pod.Name)
		}
		for j := range pod.Status.ContainerStatuses {
			container := &pod.Status.ContainerStatuses[j]
			if !container.Ready {
				return testutil.PollPending, fmt.Errorf("pod %s container %s is not Ready", pod.Name, container.Name)
			}
		}
	}
	return testutil.PollSucceeded, nil
}

func (s *Session) WaitHAProxyReady(ctx context.Context, timeout time.Duration) error {
	return poll(ctx, timeout, 5*time.Second, "all HAProxy pods Ready", func(ctx context.Context) (testutil.PollResult, error) {
		pods, err := s.Pods(ctx, "loadbalancer")
		if err != nil {
			return testutil.PollPending, err
		}
		return readyPods(pods)
	})
}

func (s *Session) WaitValidated(ctx context.Context, timeout time.Duration) error {
	return poll(ctx, timeout, 5*time.Second, "HAProxyTemplateConfig Validated", func(ctx context.Context) (testutil.PollResult, error) {
		list, err := readJSON[unstructured.UnstructuredList](ctx, s, "get", "haproxytemplateconfigs")
		if err != nil {
			return testutil.PollPending, err
		}
		if len(list.Items) == 0 {
			return testutil.PollPending, fmt.Errorf("HAProxyTemplateConfig is absent")
		}
		for i := range list.Items {
			conditions, _, err := unstructured.NestedSlice(list.Items[i].Object, "status", "conditions")
			if err != nil {
				return testutil.PollFailed, err
			}
			if err = validatedCondition(conditions); err != nil {
				return testutil.PollPending, fmt.Errorf("%s: %w", list.Items[i].GetName(), err)
			}
		}
		return testutil.PollSucceeded, nil
	})
}

func validatedCondition(conditions []any) error {
	for _, value := range conditions {
		condition, ok := value.(map[string]any)
		if !ok {
			return fmt.Errorf("invalid condition %T", value)
		}
		if condition["type"] == "Validated" {
			if condition["status"] == conditionTrue {
				return nil
			}
			return fmt.Errorf("condition Validated=%v (%v: %v)", condition["status"], condition["reason"], condition["message"])
		}
	}
	return fmt.Errorf("condition Validated is missing")
}

func (s *Session) RequireNoControllerRestarts(ctx context.Context) error {
	pods, err := s.Pods(ctx, "controller")
	if err != nil {
		return err
	}
	if len(pods) == 0 {
		return fmt.Errorf("no controller pods")
	}
	for i := range pods {
		for j := range pods[i].Status.ContainerStatuses {
			container := &pods[i].Status.ContainerStatuses[j]
			if container.RestartCount != 0 {
				return fmt.Errorf("controller %s container %s restarted %d times", pods[i].Name, container.Name, container.RestartCount)
			}
		}
	}
	return nil
}
