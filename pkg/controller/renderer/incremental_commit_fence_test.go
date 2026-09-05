package renderer

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/rendercontext"
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
