//go:build e2e

package e2e

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"sigs.k8s.io/e2e-framework/pkg/envconf"
	"sigs.k8s.io/e2e-framework/pkg/features"

	"gitlab.com/haproxy-haptic/haptic/tests/e2e/httpclient"
)

// The probe goes through the Service, so it exercises the window between a
// pod's termination and kube-proxy dropping its endpoint; the chart's preStop
// sleep keeps the listeners open across it (#224). Serial: it takes a replica
// out of the fleet and waits for the fleet to settle before returning.
func TestHAProxyPodDeletionZeroDowntime(t *testing.T) {
	const host = "haproxy-pod-deletion.localdev.me"
	feature := features.New("HAProxy: deleting a pod refuses no connection").
		Assess("the probe stream stays clean across the deletion", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			t.Helper()
			client, err := cfg.NewClient()
			require.NoError(t, err)
			clientset, err := newClientsetForE2E(client.RESTConfig())
			require.NoError(t, err)
			namespace := NamespaceForTest(ctx, t, client)
			DumpLogsOnFailure(t, namespace)
			backend := NewEchoServerBackend(ctx, t, client, namespace)
			NewIngress(ctx, t, client, namespace, &IngressSpec{
				Name: "echo", Host: host, BackendService: backend.Service, BackendPort: backend.Port,
			})
			httpclient.New(t).GET(host, "/").ExpectOK(t)

			pods := listHAProxyPods(t)
			require.NotEmpty(t, pods)
			victim := pods[0]

			proberCtx, stopProber := context.WithCancel(ctx)
			results := &probeRecorder{snapshotter: newProberSnapshotter(t, namespace)}
			var probeWG sync.WaitGroup
			probeWG.Add(1)
			go func() {
				defer probeWG.Done()
				runProbeLoopEvery(proberCtx, t, host, results, 20*time.Millisecond)
			}()
			finish := func() {
				stopProber()
				probeWG.Wait()
				results.waitForSnapshots()
			}
			t.Cleanup(finish)

			time.Sleep(2 * time.Second)
			deleted := time.Now()
			require.NoError(t, clientset.CoreV1().Pods(ControllerNamespace).Delete(ctx, victim, metav1.DeleteOptions{}))
			served := servedAfterDeletion(ctx, victim, deleted)
			require.GreaterOrEqualf(t, served, 4*time.Second,
				"%s stopped answering %s after its deletion; the preStop sleep must keep its listeners open", victim, served)
			waitForPodGone(ctx, t, clientset, victim)
			require.NoError(t, waitForDeploymentRolloutComplete(ctx, client, ControllerNamespace, HAProxyDeploymentName, 2*time.Minute))
			settled := waitForQuietFleet(ctx, t, clientset)
			require.Len(t, settled, len(pods))
			require.NotContains(t, settled, victim)
			// The replacement pod joined and the fleet settled; keep probing a little
			// longer so a late endpoint change would still be seen.
			time.Sleep(5 * time.Second)
			finish()

			failures := results.snapshotFailures()
			require.Emptyf(t, failures, "%d of %d probes failed across the deletion of %s: %+v", len(failures), results.count(), victim, failures)
			require.Positive(t, results.count())
			return ctx
		}).Feature()
	testEnv.Test(t, feature)
}

// servedAfterDeletion polls the deleted pod's own stats port and returns how
// long it kept answering, up to the preStop budget it must cover. This is the
// deterministic half: kube-proxy in kind drops the endpoint faster than the
// Service probe can catch, so the race itself does not reproduce there.
func servedAfterDeletion(ctx context.Context, pod string, deleted time.Time) time.Duration {
	for time.Since(deleted) < 5*time.Second {
		callCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		_, err := apiProxyGet(callCtx, pod, HAProxyStatsPort, "metrics")
		cancel()
		if err != nil {
			return time.Since(deleted)
		}
		time.Sleep(250 * time.Millisecond)
	}
	return time.Since(deleted)
}

// waitForPodGone returns once the pod no longer exists; its termination, preStop
// included, is the window the probe must survive.
func waitForPodGone(ctx context.Context, t *testing.T, cs kubernetes.Interface, name string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Minute)
	for {
		_, err := cs.CoreV1().Pods(ControllerNamespace).Get(ctx, name, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			return
		}
		require.NoError(t, err)
		require.False(t, time.Now().After(deadline), "pod %s is still present", name)
		time.Sleep(500 * time.Millisecond)
	}
}
