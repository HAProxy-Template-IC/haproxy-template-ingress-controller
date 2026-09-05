package stores

import (
	"context"
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type snapshotFenceMockStore struct {
	*mockStore
	fence SnapshotCommitMutex
}

func (s *snapshotFenceMockStore) AcquireSnapshotCommitFence(ctx context.Context) (func(), error) {
	return s.fence.Acquire(ctx)
}

func TestSnapshotCommitMutexHonorsCancellationAndRelease(t *testing.T) {
	var mutex SnapshotCommitMutex
	release, err := mutex.Acquire(t.Context())
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(t.Context())
	started := make(chan struct{})
	result := make(chan error, 1)
	previousProcs := runtime.GOMAXPROCS(1)
	defer runtime.GOMAXPROCS(previousProcs)
	go func() {
		close(started)
		_, acquireErr := mutex.Acquire(ctx)
		result <- acquireErr
	}()
	<-started
	cancel()

	select {
	case acquireErr := <-result:
		require.ErrorIs(t, acquireErr, context.Canceled)
	case <-time.After(2 * time.Second):
		t.Fatal("canceled fence acquisition did not return")
	}

	release()
	release()
	nextRelease, err := mutex.Acquire(t.Context())
	require.NoError(t, err)
	nextRelease()
}

func TestSnapshotCommitFencerAdaptersDelegate(t *testing.T) {
	base := &snapshotFenceMockStore{mockStore: newMockStore()}
	targets := map[string]SnapshotCommitFencer{
		"types adapter": &TypesStoreAdapter{Inner: base},
		"composite":     NewCompositeStore(base, NewStoreOverlay()),
	}
	for name, target := range targets {
		t.Run(name, func(t *testing.T) {
			release, err := target.AcquireSnapshotCommitFence(t.Context())
			require.NoError(t, err)
			ctx, cancel := context.WithCancel(t.Context())
			cancel()
			_, err = base.AcquireSnapshotCommitFence(ctx)
			require.ErrorIs(t, err, context.Canceled)
			release()
		})
	}
}

func TestSnapshotCommitFencerAdaptersRejectUnsupportedStores(t *testing.T) {
	base := newMockStore()
	targets := map[string]SnapshotCommitFencer{
		"types adapter": &TypesStoreAdapter{Inner: base},
		"composite":     NewCompositeStore(base, NewStoreOverlay()),
	}
	for name, target := range targets {
		t.Run(name, func(t *testing.T) {
			_, err := target.AcquireSnapshotCommitFence(t.Context())
			require.ErrorIs(t, err, ErrSnapshotCommitFenceUnsupported)
		})
	}
}

var _ SnapshotCommitFencer = (*snapshotFenceMockStore)(nil)
