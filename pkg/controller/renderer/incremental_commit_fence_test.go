package renderer

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sync/errgroup"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/rendercontext"
	"gitlab.com/haproxy-haptic/haptic/pkg/httpstore"
	k8sstore "gitlab.com/haproxy-haptic/haptic/pkg/k8s/store"
	"gitlab.com/haproxy-haptic/haptic/pkg/stores"
)

func TestIncrementalCommitFencesMutationThroughStatePublication(t *testing.T) {
	fixture := newAmbientResourceFixture(t, false)
	services := newObservedCommitFenceStore(fixture.services)
	fixture.provider = stores.NewRealStoreProvider(map[string]stores.Store{
		"routes": fixture.routes, "services": services,
	})
	assert.Equal(t, "route=v1\n", fixture.render(t))

	result, err := fixture.service.Render(t.Context(), fixture.provider, rendercontext.RenderModeReconcile)
	require.NoError(t, err)
	transaction, ok := result.InputTransaction.(*combinedRenderInputTransaction)
	require.True(t, ok)
	before := fixture.service.incremental.snapshot
	services.holdNextFence.Store(true)
	commitResult := make(chan error, 1)
	go func() {
		commitResult <- transaction.Commit(t.Context())
	}()
	<-services.fenceHeld

	mutationResult := make(chan error, 1)
	go func() {
		mutationResult <- services.Update(
			incrementalTestResource("default", "service", map[string]any{"value": "v2"}),
			[]string{"default", "service"},
		)
	}()
	<-services.blocked
	select {
	case mutationErr := <-mutationResult:
		t.Fatalf("mutation crossed the incremental publication fence: %v", mutationErr)
	default:
	}
	assert.Same(t, before, fixture.service.incremental.snapshot)

	close(services.continueFence)
	require.NoError(t, <-commitResult)
	require.NoError(t, <-mutationResult)
	assert.NotSame(t, before, fixture.service.incremental.snapshot)
	assert.Equal(t, "route=v2\n", fixture.render(t))
}

func TestIncrementalCommitFenceValidatesEverySourceAlias(t *testing.T) {
	base := k8sstore.NewMemoryStore(1)
	first, err := base.Pin()
	require.NoError(t, err)
	second, err := base.Pin()
	require.NoError(t, err)
	runtime := &incrementalRenderSession{
		baseSnapshots: map[string]stores.ReadSnapshot{
			"supported":   first,
			"unsupported": second,
		},
		baseStores: map[string]stores.Store{
			"supported":   base,
			"unsupported": &unfencedStore{Store: base},
		},
	}

	_, err = runtime.acquireStoreCommitFences(t.Context())
	require.ErrorIs(t, err, stores.ErrSnapshotCommitFenceUnsupported)
	require.ErrorContains(t, err, "unsupported")
}

func TestIncrementalCommitFenceRejectsMissingRelease(t *testing.T) {
	base := k8sstore.NewMemoryStore(1)
	snapshot, err := base.Pin()
	require.NoError(t, err)
	runtime := &incrementalRenderSession{
		baseSnapshots: map[string]stores.ReadSnapshot{"routes": snapshot},
		baseStores: map[string]stores.Store{
			"routes": &nilReleaseFenceStore{Store: base},
		},
	}

	_, err = runtime.acquireStoreCommitFences(t.Context())
	require.ErrorContains(t, err, "returned no release")
}

func TestCombinedInputTransactionCancellationDuringFencePreservesCause(t *testing.T) {
	fixture := newIncrementalHTTPTestFixture(t)
	result, err := fixture.service.Render(t.Context(), fixture.provider, rendercontext.RenderModeReconcile)
	require.NoError(t, err)
	transaction, ok := result.InputTransaction.(*combinedRenderInputTransaction)
	require.True(t, ok)
	routes, ok := transaction.incremental.baseStores["routes"].(*k8sstore.MemoryStore)
	require.True(t, ok)
	fence := &causeDroppingCommitFenceStore{
		MemoryStore: routes,
		entered:     make(chan struct{}),
	}
	transaction.incremental.baseStores["routes"] = fence
	publication := &postProcessTestPublication{}
	transaction.stagePublicationFinalizer(publication.Publish, publication.Abort)
	ctx, cancel := context.WithCancelCause(t.Context())
	wantErr := errors.New("commit canceled")
	commitResult := make(chan error, 1)
	go func() {
		commitResult <- transaction.Commit(ctx)
	}()
	<-fence.entered
	cancel(wantErr)

	require.ErrorIs(t, <-commitResult, wantErr)
	assert.Zero(t, publication.publishes.Load())
	assert.EqualValues(t, 1, publication.aborts.Load())
	assert.Zero(t, fixture.service.incremental.graph.Generation())
}

type unfencedStore struct {
	stores.Store
}

type nilReleaseFenceStore struct {
	stores.Store
}

func (*nilReleaseFenceStore) AcquireSnapshotCommitFence(context.Context) (release func(), err error) {
	return
}

type causeDroppingCommitFenceStore struct {
	*k8sstore.MemoryStore
	entered chan struct{}
}

func (s *causeDroppingCommitFenceStore) AcquireSnapshotCommitFence(ctx context.Context) (func(), error) {
	close(s.entered)
	<-ctx.Done()
	return nil, ctx.Err()
}

type observedCommitFenceStore struct {
	*k8sstore.MemoryStore
	permit  chan struct{}
	blocked chan struct{}
	once    sync.Once

	holdNextFence atomic.Bool
	fenceHeld     chan struct{}
	continueFence chan struct{}
}

func newObservedCommitFenceStore(base *k8sstore.MemoryStore) *observedCommitFenceStore {
	store := &observedCommitFenceStore{
		MemoryStore:   base,
		permit:        make(chan struct{}, 1),
		blocked:       make(chan struct{}),
		fenceHeld:     make(chan struct{}),
		continueFence: make(chan struct{}),
	}
	store.permit <- struct{}{}
	return store
}

func (s *observedCommitFenceStore) AcquireSnapshotCommitFence(ctx context.Context) (func(), error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-s.permit:
	}
	if s.holdNextFence.CompareAndSwap(true, false) {
		close(s.fenceHeld)
		<-s.continueFence
	}
	var once sync.Once
	return func() {
		once.Do(func() { s.permit <- struct{}{} })
	}, nil
}

func (s *observedCommitFenceStore) Update(resource any, keys []string) error {
	select {
	case <-s.permit:
	default:
		s.once.Do(func() { close(s.blocked) })
		<-s.permit
	}
	defer func() { s.permit <- struct{}{} }()
	return s.MemoryStore.Update(resource, keys)
}

var _ stores.SnapshotCommitFencer = (*observedCommitFenceStore)(nil)

// An admission render begins while a reconcile commit holds the store fences.
// The commit later needs the render state, so a session that waits on a fence
// while holding that state deadlocks both until the admission deadline.
func TestAdmissionRenderBeginsWhileCommitHoldsTheFence(t *testing.T) {
	fixture := newAmbientResourceFixture(t, false)
	services := newObservedCommitFenceStore(fixture.services)
	fixture.provider = stores.NewRealStoreProvider(map[string]stores.Store{
		"routes": fixture.routes, "services": services,
	})
	assert.Equal(t, "route=v1\n", fixture.render(t))

	result, err := fixture.service.Render(t.Context(), fixture.provider, rendercontext.RenderModeReconcile)
	require.NoError(t, err)
	transaction, ok := result.InputTransaction.(*combinedRenderInputTransaction)
	require.True(t, ok)
	services.holdNextFence.Store(true)
	commitResult := make(chan error, 1)
	go func() {
		commitResult <- transaction.Commit(t.Context())
	}()
	<-services.fenceHeld

	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	admission, err := fixture.service.Render(ctx, fixture.provider, rendercontext.RenderModeAdmission)
	require.NoError(t, err, "an admission render must not wait on a fence a commit holds")
	assert.Equal(t, "route=v1\n", admission.HAProxyConfig)

	close(services.continueFence)
	require.NoError(t, <-commitResult)
}

// Admission renders share the reconciliation service. A reconcile commit
// takes the store fences, the HTTP store, the graph and then the render
// state; a session's begin holds the render state. Any wait on one of the
// former while holding the latter deadlocks both until a deadline, or for
// good when the wait has no context. This drives the two paths against each
// other; a deadlock surfaces as an admission render that outlives its budget.
func TestAdmissionRendersInterleaveWithReconcileCommits(t *testing.T) {
	fixture := newIncrementalHTTPTestFixture(t)
	fixture.render(t)
	routes := fixture.provider.GetStore("routes")

	ctx, cancel := context.WithTimeout(t.Context(), 4*time.Second)
	defer cancel()
	var group errgroup.Group
	group.Go(func() error { return reconcileUntilDone(ctx, fixture, routes) })
	for worker := 0; worker < 4; worker++ {
		group.Go(func() error { return admitUntilDone(ctx, fixture) })
	}
	finished := make(chan error, 1)
	go func() { finished <- group.Wait() }()
	select {
	case err := <-finished:
		require.NoError(t, err)
	case <-time.After(15 * time.Second):
		t.Fatal("renders deadlocked: a render outlived its deadline without returning")
	}
}

func reconcileUntilDone(ctx context.Context, fixture *incrementalHTTPTestFixture, routes stores.Store) error {
	for iteration := 0; ctx.Err() == nil; iteration++ {
		body := fixture.urlA
		if iteration%2 == 1 {
			body = fixture.urlB
		}
		if err := routes.Update(
			incrementalTestResource("default", "a", map[string]any{"url": body}),
			[]string{"default", "a"},
		); err != nil {
			return err
		}
		result, err := fixture.service.Render(ctx, fixture.provider, rendercontext.RenderModeReconcile)
		if err == nil {
			err = result.InputTransaction.Commit(ctx)
		}
		if err != nil && ctx.Err() == nil {
			return err
		}
	}
	return nil
}

func admitUntilDone(ctx context.Context, fixture *incrementalHTTPTestFixture) error {
	for ctx.Err() == nil {
		attempt, attemptCancel := context.WithTimeout(ctx, 2*time.Second)
		_, err := fixture.service.Render(attempt, fixture.provider, rendercontext.RenderModeAdmission)
		attemptCancel()
		if err != nil && ctx.Err() == nil {
			return fmt.Errorf("admission render: %w", err)
		}
	}
	return nil
}

// A session pins its graph base at begin. Its HTTP lease used to begin only
// after the journals replayed, so a commit in between moved the store's token
// and the attempt restarted. The lease taken with the state lock is the one the
// session keeps using once the token has moved.
func TestSessionLeasePinnedAtBeginSurvivesACommit(t *testing.T) {
	fixture := newIncrementalHTTPTestFixture(t)
	fixture.render(t)
	state := fixture.service.incremental

	lease, err := state.lockStateWithHTTPLease(fixture.httpComponent)
	require.NoError(t, err)
	token := state.baseHTTPTokenLocked()
	state.mu.Unlock()
	require.Equal(t, token, lease.Token())

	routes := fixture.provider.GetStore("routes")
	require.NoError(t, routes.Update(
		incrementalTestResource("default", "a", map[string]any{"url": fixture.urlB}),
		[]string{"default", "a"},
	))
	fixture.render(t)

	_, err = fixture.httpComponent.BeginActiveLeases(state.httpLeaseSet, token)
	require.ErrorIs(t, err, httpstore.ErrActiveLeaseTokenStale)
	session := &incrementalRenderSession{
		state: state, httpComponent: fixture.httpComponent, beginHTTPLease: lease,
	}
	pinned, err := session.beginActiveLeases(token)
	require.NoError(t, err)
	require.Same(t, lease, pinned)
}

// The renderer base is copied under the state lock and the graph session
// begins outside it. A commit in between leaves the graph one generation ahead
// of the copied base, which a dry-run would render as a torn configuration.
func TestSessionRestartsWhenTheBaseMovesBeforeItsGraphSession(t *testing.T) {
	fixture := newIncrementalHTTPTestFixture(t)
	fixture.render(t)
	state := fixture.service.incremental
	state.mu.Lock()
	base := state.snapshot
	state.mu.Unlock()

	pinned := &incrementalRenderSession{state: state, base: base}
	require.NoError(t, pinned.pinGraphBase())
	require.NotNil(t, pinned.graphSession)
	pinned.graphSession.Abort()

	routes := fixture.provider.GetStore("routes")
	require.NoError(t, routes.Update(
		incrementalTestResource("default", "a", map[string]any{"url": fixture.urlB}),
		[]string{"default", "a"},
	))
	fixture.render(t)

	moved := &incrementalRenderSession{state: state, base: base}
	require.ErrorIs(t, moved.pinGraphBase(), errIncrementalBaseMoved)
	require.Nil(t, moved.graphSession)
}
