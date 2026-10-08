// Copyright 2026 Philipp Hossner
// SPDX-License-Identifier: Apache-2.0

package scenarios

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"

	"gitlab.com/haproxy-haptic/haptic/tests/kubeexec"
	"gitlab.com/haproxy-haptic/haptic/tests/process"
	"gitlab.com/haproxy-haptic/haptic/tests/testutil"
)

func TestGitOpsSyncRequiresCurrentRevisionAndOperation(t *testing.T) {
	for _, provider := range []string{"argo", "flux"} {
		t.Run(provider, func(t *testing.T) {
			object := &unstructured.Unstructured{Object: map[string]any{}}
			data := `{"apiVersion":"helm.toolkit.fluxcd.io/v2","kind":"HelmRelease","metadata":{"generation":2},"status":{"lastAttemptedRevision":"new","lastHandledReconcileAt":"request","conditions":[{"type":"Ready","status":"True","observedGeneration":2}]}}`
			if provider == "argo" {
				data = `{"apiVersion":"argoproj.io/v1alpha1","kind":"Application","status":{"operationState":{"startedAt":"new-time","phase":"Succeeded","syncResult":{"revision":"new"}},"sync":{"status":"Synced"},"health":{"status":"Healthy"}}}`
			}
			require.NoError(t, json.Unmarshal([]byte(data), object))
			result, err := gitopsFinished(object, provider, "new", false, true, "old-time", "request")
			require.NoError(t, err)
			require.Equal(t, testutil.PollSucceeded, result)
			result, err = gitopsFinished(object, provider, "stale", false, true, "old-time", "request")
			require.NoError(t, err)
			require.Equal(t, testutil.PollPending, result)
			result, err = gitopsFinished(object, provider, "new", true, false, "old-time", "request")
			require.NoError(t, err)
			require.Equal(t, testutil.PollPending, result)
			if provider == "argo" {
				result, err = gitopsFinished(object, provider, "new", false, true, "new-time", "request")
				require.NoError(t, err)
				require.Equal(t, testutil.PollPending, result)
				require.NoError(t, unstructured.SetNestedField(object.Object, "Failed", "status", "operationState", "phase"))
				result, err = gitopsFinished(object, provider, "new", false, false, "old-time", "request")
				require.Error(t, err)
				require.Equal(t, testutil.PollFailed, result)
			} else {
				result, err = gitopsFinished(object, provider, "new", false, false, "old-time", "different-request")
				require.NoError(t, err)
				require.Equal(t, testutil.PollPending, result)
				object.SetGeneration(3)
				result, err = gitopsFinished(object, provider, "new", false, false, "old-time", "request")
				require.NoError(t, err)
				require.Equal(t, testutil.PollPending, result)
				object.SetGeneration(2)
				require.NoError(t, unstructured.SetNestedSlice(object.Object, []any{map[string]any{"type": "Released", "status": "False", "reason": "UpgradeFailed", "observedGeneration": int64(2)}}, "status", "conditions"))
			}
			result, err = gitopsFinished(object, provider, "new", true, false, "old-time", "request")
			require.NoError(t, err)
			require.Equal(t, testutil.PollSucceeded, result)
		})
	}
}

func TestGitOpsSnapshotRejectsRestartsAndMissingFleet(t *testing.T) {
	pods := make([]corev1.Pod, 0, 4)
	for _, component := range []string{"controller", "loadbalancer"} {
		for _, uid := range []string{"a", "b"} {
			pods = append(pods, corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: component + uid, UID: types.UID(component + uid), Labels: map[string]string{"app.kubernetes.io/component": component}}})
		}
	}
	secrets := []corev1.Secret{{ObjectMeta: metav1.ObjectMeta{Name: "credentials"}, Data: map[string][]byte{"key": []byte("old")}}, {ObjectMeta: metav1.ObjectMeta{Name: "sh.helm.release.v1"}}}
	before, err := snapshotGitOps("config", pods, secrets)
	require.NoError(t, err)
	require.Len(t, before.Secrets, 1)
	after, err := snapshotGitOps("config", pods, secrets)
	require.NoError(t, err)
	require.True(t, before.equal(after))
	secrets[0].Data["key"] = []byte("rotated")
	after, err = snapshotGitOps("config", pods, secrets)
	require.NoError(t, err)
	require.False(t, before.equal(after))
	pods[0].Status.InitContainerStatuses = []corev1.ContainerStatus{{Name: "agent", RestartCount: 1}}
	_, err = snapshotGitOps("config", pods, secrets)
	require.Error(t, err)
	pods[0].Status.InitContainerStatuses = nil
	pods[0].DeletionTimestamp = &metav1.Time{Time: time.Now()}
	_, err = snapshotGitOps("config", pods, secrets)
	require.Error(t, err)
}

func TestProviderManifestFilteringPreservesRBAC(t *testing.T) {
	content := []byte("apiVersion: apps/v1\nkind: Deployment\nmetadata:\n  name: source-controller\n---\napiVersion: apps/v1\nkind: Deployment\nmetadata:\n  name: notification-controller\n---\napiVersion: rbac.authorization.k8s.io/v1\nkind: ClusterRole\nmetadata:\n  name: shared\n")
	filtered, err := filterDeployments(content, []string{"source-controller"})
	require.NoError(t, err)
	require.Contains(t, string(filtered), "source-controller")
	require.NotContains(t, string(filtered), "notification-controller")
	require.Contains(t, string(filtered), "ClusterRole")
}

func TestPublicationObservationRetryAndTerminalDecode(t *testing.T) {
	for _, operation := range []string{"exec", "pods", "haproxycfg", "haproxycfgs", "secrets", "invalid JSON", "persistent"} {
		t.Run(operation, func(t *testing.T) {
			failed := false
			calls := 0
			runner := publicationRunner(operation, &failed, &calls)
			dir := t.TempDir()
			session := &Session{Client: kubeexec.Client{Runner: runner, Kubeconfig: filepath.Join(dir, "kubeconfig"), Context: "kind-owned"}, Artifacts: dir}
			g := &gitops{session: session}
			timeout := time.Second
			if operation == "persistent" {
				timeout = 10 * time.Millisecond
			}
			err := g.waitPublication(t.Context(), "upgraded", timeout, time.Millisecond)
			switch operation {
			case "invalid JSON":
				var decode *observationDecodeError
				require.ErrorAs(t, err, &decode)
				require.Equal(t, 2, calls)
			case "persistent":
				require.ErrorIs(t, err, context.DeadlineExceeded)
			default:
				require.NoError(t, err)
				require.True(t, failed)
			}
		})
	}
}

func TestPublicationWaitsUntilAnObsoleteCheckpointSecretDisappears(t *testing.T) {
	failed, calls, observations := false, 0, 0
	base := publicationRunner("", &failed, &calls)
	runner := scenarioRunner{run: func(ctx context.Context, command *process.Command) (process.Result, error) {
		if slices.Contains(command.Args, "secrets") {
			observations++
			if observations == 1 {
				return process.Result{Stdout: `{"items":[{"metadata":{"name":"obsolete-certificate","ownerReferences":[{"apiVersion":"haproxy-haptic.org/v1alpha1","kind":"HAProxyCfg","name":"obsolete-checkpoint","uid":"obsolete","controller":true}]}}]}`}, nil
			}
		}
		return base.Run(ctx, command)
	}}
	dir := t.TempDir()
	g := &gitops{session: &Session{Client: kubeexec.Client{Runner: runner, Kubeconfig: filepath.Join(dir, "kubeconfig"), Context: "kind-owned"}, Artifacts: dir}}
	require.NoError(t, g.waitPublication(t.Context(), "installed", time.Second, time.Millisecond))
	require.Equal(t, 2, observations)
}

func publicationResponse(args []string) string {
	if slices.Contains(args, "pods") {
		return `{"items":[{"metadata":{"name":"a","uid":"a"}},{"metadata":{"name":"b","uid":"b"}}]}`
	}
	if slices.Contains(args, "secrets") {
		return `{"items":[]}`
	}
	if slices.Contains(args, "haproxycfg") {
		return publicationConfigResponse(false)
	}
	if slices.Contains(args, "haproxycfgs") {
		return `{"items":[` + publicationConfigResponse(false) + `,` + publicationConfigResponse(true) + `]}`
	}
	panic("unexpected kubectl command")
}

func publicationConfigResponse(retained bool) string {
	current := `{"metadata":{"name":"config","uid":"cfg","ownerReferences":[OWNER],"annotations":{"haproxy-haptic.org/auxiliary-set-id":"set"}},"spec":{"checksum":"checksum"},"status":{"retainedConfigs":[{"name":"checkpoint","uid":"checkpoint","checksum":"checksum"}],"auxiliaryFiles":{"setID":"set"},"deployedToPods":[{"podUID":"a","checksum":"checksum",PROOF},{"podUID":"b","checksum":"checksum",PROOF}]}}`
	if retained {
		current = `{"metadata":{"name":"checkpoint","uid":"checkpoint","ownerReferences":[OWNER],"labels":{"haproxy-haptic.org/retained-for":"template"},"annotations":{"haproxy-haptic.org/auxiliary-set-id":"set"}},"spec":{"checksum":"checksum","retainedPlan":"encoded"},"status":{"auxiliaryFiles":{"setID":"set"},"deployedToPods":[{"podUID":"a","checksum":"checksum",PROOF}]}}`
	}
	return strings.NewReplacer("OWNER", `{"apiVersion":"haproxy-haptic.org/v1alpha1","kind":"HAProxyTemplateConfig","name":"template","uid":"template","controller":true}`, "PROOF", `"appliedPlanID":"applied","runningPlanID":"running","workerOpsPlanID":"worker"`).Replace(current)
}

func publicationRunner(operation string, failed *bool, calls *int) scenarioRunner {
	return scenarioRunner{run: func(_ context.Context, command *process.Command) (process.Result, error) {
		*calls++
		if operation == "persistent" {
			return process.Result{}, errors.New("agent unavailable")
		}
		if !*failed && slices.Contains(command.Args, operation) {
			*failed = true
			return process.Result{}, errors.New("resource disappeared")
		}
		if slices.Contains(command.Args, "exec") {
			if operation == "invalid JSON" {
				return process.Result{Stdout: "invalid JSON"}, nil
			}
			return process.Result{Stdout: `{"applied_plan_id":"applied","running_plan_id":"running","worker_ops_plan_id":"worker"}`}, nil
		}
		return process.Result{Stdout: publicationResponse(command.Args)}, nil
	}}
}
