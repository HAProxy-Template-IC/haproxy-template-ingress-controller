//go:build e2e

package e2e

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	"sigs.k8s.io/e2e-framework/pkg/envconf"
	"sigs.k8s.io/e2e-framework/pkg/features"
)

func TestControllerShutdownPreservesAdmission(t *testing.T) {
	feature := features.New("Controller shutdown preserves admission").
		Assess("valid updates pass and invalid updates are denied through follower and leader termination", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			t.Helper()
			client, err := cfg.NewClient()
			require.NoError(t, err)
			cs, err := newClientsetForE2E(client.RESTConfig())
			require.NoError(t, err)
			namespace := NamespaceForTest(ctx, t, client)
			DumpLogsOnFailure(t, namespace)
			backend := NewEchoServerBackend(ctx, t, client, namespace)
			NewIngress(ctx, t, client, namespace, &IngressSpec{
				Name: "echo", Host: "shutdown.localdev.me", BackendService: backend.Service, BackendPort: backend.Port,
			})
			identity, err := expectedControllerIdentity()
			require.NoError(t, err)
			desired := int32(len(controllerPodNames(ctx, t, cs)))
			require.GreaterOrEqual(t, desired, int32(2))
			for _, leader := range []bool{false, true} {
				victim := currentLeader(ctx, t, cs)
				if !leader {
					victim = standbyController(ctx, t, cs, victim)
				}
				stop, results := probeAdmission(ctx, cs, namespace)
				t.Cleanup(func() { stop(); <-results.done })
				<-results.started
				require.NoError(t, cs.CoreV1().Pods(ControllerNamespace).Delete(ctx, victim, metav1.DeleteOptions{}))
				_, err = waitForControllerIdentityPods(ctx, cs, identity, desired)
				stop()
				<-results.done
				require.NoError(t, err)
				require.Emptyf(t, results.failures, "admission failures while deleting %s: %v", victim, results.failures)
				require.Positive(t, results.allowed)
				require.Positive(t, results.denied)
				t.Logf("deleted %s (leader=%t): %d allowed and %d correctly denied admissions", victim, leader, results.allowed, results.denied)
			}
			return ctx
		}).Feature()
	testEnv.Test(t, feature)
}

type admissionProbeResults struct {
	started  chan struct{}
	done     chan struct{}
	allowed  int
	denied   int
	failures []error
}

func probeAdmission(ctx context.Context, cs kubernetes.Interface, namespace string) (context.CancelFunc, *admissionProbeResults) {
	probeCtx, stop := context.WithCancel(ctx)
	result := &admissionProbeResults{started: make(chan struct{}), done: make(chan struct{})}
	go func() {
		defer close(result.done)
		ticker := time.NewTicker(25 * time.Millisecond)
		defer ticker.Stop()
		var started sync.Once
		for {
			for _, valid := range []bool{true, false} {
				err := checkAdmission(ctx, cs, namespace, valid)
				if err != nil {
					result.failures = append(result.failures, err)
				} else if valid {
					result.allowed++
				} else {
					result.denied++
				}
			}
			started.Do(func() { close(result.started) })
			select {
			case <-probeCtx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
	return stop, result
}

func checkAdmission(ctx context.Context, cs kubernetes.Interface, namespace string, valid bool) error {
	timeout := "30s"
	if !valid {
		timeout = "invalid-timeout"
	}
	body := fmt.Appendf(nil, `{"metadata":{"annotations":{"haproxy-haptic.org/timeout-server":%q}}}`, timeout)
	callCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	_, err := cs.NetworkingV1().Ingresses(namespace).Patch(callCtx, "echo", types.MergePatchType, body, metav1.PatchOptions{DryRun: []string{metav1.DryRunAll}})
	if valid && err == nil {
		return nil
	}
	if !valid && err != nil && strings.Contains(err.Error(), "denied the request") && strings.Contains(err.Error(), "timeout-server") {
		return nil
	}
	return fmt.Errorf("admission valid=%t: %v", valid, err)
}
