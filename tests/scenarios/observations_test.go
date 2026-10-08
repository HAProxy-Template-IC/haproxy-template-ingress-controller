// Copyright 2026 Philipp Hossner
// SPDX-License-Identifier: Apache-2.0

package scenarios

import (
	"testing"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"gitlab.com/haproxy-haptic/haptic/tests/testutil"
)

func TestHAProxyReadinessRequiresTheWholeObservedFleet(t *testing.T) {
	ready := corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "haproxy"}, Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "haproxy"}, {Name: "agent"}}}, Status: corev1.PodStatus{ContainerStatuses: []corev1.ContainerStatus{{Name: "haproxy", Ready: true}, {Name: "agent", Ready: true}}}}
	for _, state := range []string{"ready", "empty", "bootstrap", "missing sidecar", "terminating", "no containers"} {
		t.Run(state, func(t *testing.T) {
			pods := []corev1.Pod{*ready.DeepCopy(), *ready.DeepCopy()}
			switch state {
			case "empty":
				pods = nil
			case "bootstrap":
				pods[1].Status.ContainerStatuses[0].Ready = false
			case "missing sidecar":
				pods[1].Status.ContainerStatuses = pods[1].Status.ContainerStatuses[:1]
			case "terminating":
				now := metav1.Now()
				pods[1].DeletionTimestamp = &now
			case "no containers":
				pods[1].Spec.Containers = nil
				pods[1].Status.ContainerStatuses = nil
			}
			result, err := readyPods(pods)
			if state == "ready" {
				require.NoError(t, err)
				require.Equal(t, testutil.PollSucceeded, result)
			} else {
				require.Error(t, err)
				require.Equal(t, testutil.PollPending, result)
			}
		})
	}
}

func TestPlainInstallRejectsBootstrapAndIncompleteRender(t *testing.T) {
	for _, tt := range []struct {
		name, config string
		valid        bool
	}{
		{"render", "global\n    default-path origin\nfrontend http\n    bind :80\n", true},
		{"bootstrap", "global\nfrontend bootstrap\n    http-request return status 503\n", false},
		{"no frontend", "global\n    default-path origin\n", false},
		{"empty", "", false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			err := renderedConfig(tt.config)
			if tt.valid {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}
		})
	}
}

func TestValidatedConditionRequiresAnExplicitTrue(t *testing.T) {
	for _, tt := range []struct {
		name       string
		conditions []any
		valid      bool
	}{
		{"validated", []any{map[string]any{"type": "Validated", "status": "True"}}, true},
		{"other condition", []any{map[string]any{"type": "Ready", "status": "True"}}, false},
		{"denied", []any{map[string]any{"type": "Validated", "status": "False", "reason": "LoadFailed", "message": "bad template"}}, false},
		{"missing", nil, false},
		{"malformed", []any{"unexpected"}, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			err := validatedCondition(tt.conditions)
			if tt.valid {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}
		})
	}
}
