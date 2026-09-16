package controller

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	k8sstore "gitlab.com/haproxy-haptic/haptic/pkg/k8s/store"
	"gitlab.com/haproxy-haptic/haptic/pkg/k8s/types"
	"gitlab.com/haproxy-haptic/haptic/pkg/stores"
	"gitlab.com/haproxy-haptic/haptic/pkg/stores/storetest"
)

type contextualProviderStore struct {
	*storetest.MockStore
}

func (s *contextualProviderStore) GetContext(ctx context.Context, keys ...string) ([]any, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return s.Get(keys...)
}

func TestBuildStoreProviderPreservesCapabilities(t *testing.T) {
	memory := k8sstore.NewMemoryStore(2)
	readErr := errors.New("store read failed")
	legacy := &storetest.MockStore{GetErr: readErr}
	contextual := &contextualProviderStore{MockStore: &storetest.MockStore{}}
	sources := map[string]types.Store{
		"memory": memory, "legacy": legacy, "contextual": contextual, "absent": nil,
	}
	provider := buildStoreProvider(sources)
	require.ElementsMatch(t, []string{"memory", "legacy", "contextual"}, provider.StoreNames())
	require.Nil(t, provider.GetStore("absent"))
	require.Nil(t, provider.GetStore("unknown"))
	for name, source := range sources {
		if source != nil {
			require.Same(t, source, provider.GetStore(name))
		}
	}
	delete(sources, "memory")
	require.Same(t, memory, provider.GetStore("memory"))

	published := provider.GetStore("memory")
	require.True(t, stores.SupportsExactRevisionJournal(published))
	require.True(t, stores.SupportsSnapshotCommitFence(published))
	pinned, err := published.(stores.SnapshotProvider).Pin()
	require.NoError(t, err)
	require.Equal(t, memory.RevisionSource(), pinned.RevisionSource())
	require.True(t, stores.HasIdentityOrderedReads(pinned))

	unsupported := provider.GetStore("legacy")
	_, snapshotSupported := unsupported.(stores.SnapshotProvider)
	require.False(t, snapshotSupported)
	require.False(t, stores.SupportsExactRevisionJournal(unsupported))
	require.False(t, stores.SupportsSnapshotCommitFence(unsupported))
	_, err = unsupported.Get("default", "target")
	require.ErrorIs(t, err, readErr)

	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err = provider.GetStore("contextual").(stores.ContextGetter).GetContext(ctx, "default", "target")
	require.ErrorIs(t, err, context.Canceled)
}

func TestBuildStoreProviderAcceptsNoStores(t *testing.T) {
	provider := buildStoreProvider(nil)
	require.Empty(t, provider.StoreNames())
	require.Nil(t, provider.GetStore("missing"))
}
