// Copyright 2026 Philipp Hossner
// SPDX-License-Identifier: Apache-2.0

package scenarios

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"gitlab.com/haproxy-haptic/haptic/tests/kubeexec"
	"gitlab.com/haproxy-haptic/haptic/tests/process"
)

func TestRejectedUpgradeFingerprintCoversEveryEffectiveInput(t *testing.T) {
	inputs := make([]unstructured.Unstructured, 0, 3)
	for _, kind := range []string{"HAProxyTemplateConfig", "HAProxyTemplateLibrary", "HAProxyValidationTests"} {
		inputs = append(inputs, unstructured.Unstructured{Object: map[string]any{"kind": kind, "metadata": map[string]any{"name": "same-name"}, "spec": map[string]any{"template": "valid"}}})
	}
	before, err := snapshot(inputs)
	require.NoError(t, err)
	reordered := slices.Clone(inputs)
	slices.Reverse(reordered)
	after, err := snapshot(reordered)
	require.NoError(t, err)
	require.NoError(t, before.RequireUnchanged(after))
	for i := range inputs {
		t.Run(inputs[i].GetKind(), func(t *testing.T) {
			changed := make([]unstructured.Unstructured, len(inputs))
			for j := range inputs {
				changed[j] = *inputs[j].DeepCopy()
			}
			require.NoError(t, unstructured.SetNestedField(changed[i].Object, "broken", "spec", "template"))
			after, err := snapshot(changed)
			require.NoError(t, err)
			require.Error(t, before.RequireUnchanged(after))
		})
	}
	_, err = snapshot(nil)
	require.Error(t, err)
}

func TestBrokenReleaseCorruptsTheMainTemplateAndLeavesSourceUntouched(t *testing.T) {
	const source = "templateSnippets:\n  unused:\n    template: |\n      valid\nhaproxyConfig:\n  template: |\n    global\n"
	input := []byte(source)
	broken, err := corruptMainTemplate(input)
	require.NoError(t, err)
	require.Equal(t, source, string(input))
	require.Contains(t, string(broken), "haproxyConfig:\n  template: |\n    {%- var x = %}\n    global")
	_, err = corruptMainTemplate([]byte("templateSnippets:\n  unused:\n    template: |\n      valid\n"))
	require.Error(t, err)
}

func TestUpgradeTrafficUsesActualNamedContainerPorts(t *testing.T) {
	for _, ports := range [][2]int32{{80, 443}, {8080, 8443}} {
		pod := &corev1.Pod{Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "haproxy", Ports: []corev1.ContainerPort{{Name: "https", ContainerPort: ports[1]}, {Name: "http", ContainerPort: ports[0]}}}}}}
		got, err := podRoutePorts(pod)
		require.NoError(t, err)
		require.Equal(t, int(ports[0]), got[0].Target)
		require.Equal(t, "http", got[0].Remote)
		require.Equal(t, int(ports[1]), got[1].Target)
		pod.Spec.Containers = append(pod.Spec.Containers, pod.Spec.Containers[0])
		_, err = podRoutePorts(pod)
		require.Error(t, err)
	}
	_, err := podRoutePorts(&corev1.Pod{})
	require.Error(t, err)
}

type scenarioRunner struct {
	run func(context.Context, *process.Command) (process.Result, error)
}

func (r scenarioRunner) Run(ctx context.Context, command *process.Command) (process.Result, error) {
	return r.run(ctx, command)
}
func (scenarioRunner) Start(context.Context, *process.Command) (process.Running, error) {
	panic("unexpected async command")
}

func TestControllerDiagnosticsIgnoreTerminatingPodsButFailLiveErrors(t *testing.T) {
	for _, mode := range []string{"healthy", "rejected", "listing failure", "logs failure"} {
		t.Run(mode, func(t *testing.T) {
			reads := 0
			runner := controllerDiagnosticsRunner(t, mode, &reads)
			dir := t.TempDir()
			session := &Session{Runner: runner, Client: kubeexec.Client{Runner: runner, Kubeconfig: filepath.Join(dir, "kubeconfig"), Context: "kind-private", Namespace: "test"}, Artifacts: dir}
			err := session.CheckControllerOutput(t.Context())
			if mode == "healthy" {
				require.NoError(t, err)
				require.Equal(t, 1, reads)
			} else {
				require.Error(t, err)
				require.False(t, strings.Contains(err.Error(), "old"))
			}
		})
	}
}

func controllerDiagnosticsRunner(t *testing.T, mode string, reads *int) scenarioRunner {
	t.Helper()
	return scenarioRunner{run: func(_ context.Context, command *process.Command) (process.Result, error) {
		if slices.Contains(command.Args, "get") {
			if mode == "listing failure" {
				return process.Result{}, errors.New("forbidden")
			}
			return process.Result{Stdout: `{"items":[{"metadata":{"name":"old","deletionTimestamp":"2026-10-01T00:00:00Z"}},{"metadata":{"name":"new"}}]}`}, nil
		}
		require.Contains(t, command.Args, "logs")
		require.Contains(t, command.Args, "pod/new")
		require.NotContains(t, command.Args, "pod/old")
		*reads++
		if mode == "logs failure" {
			return process.Result{}, errors.New("logs unavailable")
		}
		if mode == "rejected" {
			return process.Result{Stdout: "Rendered output rejected"}, nil
		}
		return process.Result{Stdout: "reconciled"}, nil
	}}
}
