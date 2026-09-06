package main

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"golang.org/x/sync/errgroup"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/rendercontext"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/renderer"
	"gitlab.com/haproxy-haptic/haptic/pkg/stores"
)

var httpAuthRef = regexp.MustCompile(`http_auth\(([^)]+)\)`)

// undefinedUserlist names a userlist a rendered backend references without the
// configuration defining it.
func undefinedUserlist(config string) string {
	for _, match := range httpAuthRef.FindAllStringSubmatch(config, -1) {
		if !strings.Contains(config, "userlist "+match[1]+"\n") {
			return match[1]
		}
	}
	return ""
}

func basicAuthSecretAndIngress(index int) (secret, ingress map[string]any, namespace string) {
	namespace = fmt.Sprintf("authns-%d", index)
	secret = map[string]any{
		"apiVersion": "v1", "kind": "Secret",
		"metadata": map[string]any{"name": "echo-auth-secret", "namespace": namespace},
		"type":     "Opaque",
		"data": map[string]any{
			"auth": "YWRtaW46JDJ5JDA1JG1OMVdWazVRbmJnNFF3ZEFkWGJmei44YjNjZUg2UTVLT1ZDS3hSMklrTkFmSmdMaTVwSUtXCg==",
		},
	}
	ingress = benchIngressContent("auth", "svc-0")
	ingress["metadata"] = map[string]any{
		"name": "auth", "namespace": namespace,
		"annotations": map[string]any{
			"haproxy-haptic.org/auth-type":        "basic",
			"haproxy-haptic.org/auth-secret":      "echo-auth-secret",
			"haproxy-haptic.org/auth-secret-type": "auth-file",
		},
	}
	return secret, ingress, namespace
}

type admissionChurnFixture struct {
	service  *renderer.RenderService
	provider stores.StoreProvider
	overlay  stores.StoreProvider
	storeMap map[string]stores.Store
}

func newAdmissionChurnFixture(t *testing.T) *admissionChurnFixture {
	t.Helper()
	cfg, setup, logger, cleanup := bundledChartSetup(t)
	t.Cleanup(cleanup)
	storeMap, err := createStoresForBenchmark(cfg, setup.Engine, benchScaleFixtures(cfg, 2))
	require.NoError(t, err)
	engine := newIncrementalBenchmarkCountingEngine(t, setup.Engine)
	lifecycle := newIncrementalBenchmarkCacheLifecycle(nil)
	service := newBundledIncrementalBenchmarkService(cfg, setup, engine, logger, lifecycle)
	provider := stores.NewRealStoreProvider(storeMap)
	_, err = runIncrementalBenchmarkRenderCacheReady(t.Context(), service, provider, lifecycle)
	require.NoError(t, err)
	_, err = runIncrementalBenchmarkRenderResult(service, provider)
	require.NoError(t, err)

	victim := benchIngressContent("victim", "svc-1")
	victim["metadata"] = map[string]any{"name": "victim", "namespace": "other"}
	overlay := stores.NewOverlayStoreProvider(provider, stores.NewValidationContext(
		map[string]*stores.StoreOverlay{
			ingressStoreName: stores.NewStoreOverlayForCreate(&unstructured.Unstructured{Object: victim}),
		},
	))
	return &admissionChurnFixture{service: service, provider: provider, overlay: overlay, storeMap: storeMap}
}

// reconcileWithNewBasicAuthIngresses adds one basic-auth Ingress and its
// Secret per iteration and commits a reconcile render, until ctx ends.
func (f *admissionChurnFixture) reconcileWithNewBasicAuthIngresses(ctx context.Context, count *atomic.Int64) error {
	for index := 0; ctx.Err() == nil; index++ {
		secret, ingress, namespace := basicAuthSecretAndIngress(index)
		if err := f.storeMap["secrets"].Update(secret, []string{namespace, "echo-auth-secret"}); err != nil {
			return err
		}
		if err := f.storeMap[ingressStoreName].Update(ingress, []string{namespace, "auth"}); err != nil {
			return err
		}
		result, err := f.service.Render(ctx, f.provider, rendercontext.RenderModeReconcile)
		if err == nil {
			err = result.InputTransaction.Commit(ctx)
		}
		if err != nil {
			return ignoreContextEnd(ctx, fmt.Errorf("reconcile %d: %w", index, err))
		}
		if tear := undefinedUserlist(result.HAProxyConfig); tear != "" {
			return fmt.Errorf("reconcile %d references undefined userlist %s", index, tear)
		}
		count.Add(1)
	}
	return nil
}

// admitUntilDone renders the overlay subject on the shared graph until ctx
// ends and fails on the first torn output.
func (f *admissionChurnFixture) admitUntilDone(ctx context.Context, count *atomic.Int64) error {
	for ctx.Err() == nil {
		result, err := f.service.Render(ctx, f.overlay, rendercontext.RenderModeAdmission,
			rendercontext.WithAdmissionSubject(ingressStoreName, "other", "victim"))
		if err != nil {
			return ignoreContextEnd(ctx, fmt.Errorf("admission: %w", err))
		}
		if tear := undefinedUserlist(result.HAProxyConfig); tear != "" {
			return fmt.Errorf("admission %d (cache %v) references undefined userlist %s",
				count.Load(), result.CacheState, tear)
		}
		if !strings.Contains(result.HAProxyConfig, "other_victim") {
			return fmt.Errorf("admission %d lost its own subject", count.Load())
		}
		count.Add(1)
	}
	return nil
}

// ignoreContextEnd drops an error the deadline caused: the loops stop on it.
func ignoreContextEnd(ctx context.Context, err error) error {
	select {
	case <-ctx.Done():
		return nil
	default:
		return err
	}
}

// Admission renders on the shared warm graph while reconciles add basic-auth
// Ingresses and commit: no admission output may reference a userlist it does
// not define. Before the session read its cached activations from the
// generation it began on, a commit landing during the render made it skip the
// publication component of the newest Ingress while still rendering its
// backend.
func TestBundledChartAdmissionUserlistsStayConsistentUnderReconcileChurn(t *testing.T) {
	fixture := newAdmissionChurnFixture(t)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	var admissions, reconciles atomic.Int64
	group, gctx := errgroup.WithContext(ctx)
	group.Go(func() error { return fixture.reconcileWithNewBasicAuthIngresses(gctx, &reconciles) })
	for worker := 0; worker < 3; worker++ {
		group.Go(func() error { return fixture.admitUntilDone(gctx, &admissions) })
	}
	require.NoError(t, group.Wait())
	t.Logf("reconciles=%d admissions=%d", reconciles.Load(), admissions.Load())
	require.Positive(t, reconciles.Load())
	require.Positive(t, admissions.Load())
}
