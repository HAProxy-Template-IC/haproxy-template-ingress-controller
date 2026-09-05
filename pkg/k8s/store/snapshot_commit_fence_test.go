package store

import (
	"context"
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	kruntime "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/dynamic/fake"

	"gitlab.com/haproxy-haptic/haptic/pkg/stores"
)

type storeFenceFixture struct {
	fencer stores.SnapshotCommitFencer
	mutate func() error
}

func TestStoreMutationsWaitForSnapshotCommitFence(t *testing.T) {
	backends := map[string]func(*testing.T, string) storeFenceFixture{
		"memory": memoryFenceFixture,
		"cached": cachedFenceFixture,
	}
	for backend, fixture := range backends {
		for _, operation := range []string{"add", "update", "delete", "clear"} {
			t.Run(backend+"/"+operation, func(t *testing.T) {
				assertStoreMutationWaitsForFence(t, fixture(t, operation))
			})
		}
	}
}

func assertStoreMutationWaitsForFence(t *testing.T, fixture storeFenceFixture) {
	t.Helper()
	previousProcs := runtime.GOMAXPROCS(1)
	defer runtime.GOMAXPROCS(previousProcs)
	release, err := fixture.fencer.AcquireSnapshotCommitFence(t.Context())
	require.NoError(t, err)
	started := make(chan struct{})
	result := make(chan error, 1)
	go func() {
		close(started)
		result <- fixture.mutate()
	}()
	<-started
	select {
	case mutationErr := <-result:
		t.Fatalf("mutation completed while fenced: %v", mutationErr)
	default:
	}

	release()
	select {
	case mutationErr := <-result:
		require.NoError(t, mutationErr)
	case <-time.After(2 * time.Second):
		t.Fatal("mutation did not resume after fence release")
	}
}

func memoryFenceFixture(t *testing.T, operation string) storeFenceFixture {
	t.Helper()
	resourceStore := NewMemoryStore(2)
	target := namedResource("default", "target")
	if operation != "add" {
		require.NoError(t, resourceStore.Add(target, []string{"default", "target"}))
	}
	return storeFenceFixture{
		fencer: resourceStore,
		mutate: func() error {
			switch operation {
			case "add":
				return resourceStore.Add(target, []string{"default", "target"})
			case "update":
				updated := namedResource("default", "target")
				updated["value"] = "updated"
				return resourceStore.Update(updated, []string{"default", "target"})
			case "delete":
				return resourceStore.Delete("default", "target", []string{"default", "target"})
			default:
				return resourceStore.Clear()
			}
		},
	}
}

func cachedFenceFixture(t *testing.T, operation string) storeFenceFixture {
	t.Helper()
	client := fake.NewSimpleDynamicClient(kruntime.NewScheme())
	resourceStore := newProjectedSnapshotStore(t, client)
	target := cachedSnapshotResource("default", "target", "1", "value")
	if operation != "add" {
		require.NoError(t, resourceStore.Add(cachedSnapshotRef(target), []string{"default", "target"}))
	}
	return storeFenceFixture{
		fencer: resourceStore,
		mutate: func() error {
			switch operation {
			case "add":
				return resourceStore.Add(cachedSnapshotRef(target), []string{"default", "target"})
			case "update":
				updated := cachedSnapshotResource("default", "target", "2", "updated")
				return resourceStore.Update(cachedSnapshotRef(updated), []string{"default", "target"})
			case "delete":
				return resourceStore.Delete("default", "target", []string{"default", "target"})
			default:
				return resourceStore.Clear()
			}
		},
	}
}

func TestSnapshotCommitFenceDoesNotBlockPinnedReads(t *testing.T) {
	t.Run("memory", func(t *testing.T) {
		resourceStore := NewMemoryStore(2)
		require.NoError(t, resourceStore.Add(namedResource("default", "target"), []string{"default", "target"}))
		assertPinnedReadWhileFenced(t, resourceStore, func(ctx context.Context) error {
			snapshot, err := resourceStore.Pin()
			if err != nil {
				return err
			}
			_, err = snapshot.Get("default", "target")
			return err
		})
	})

	t.Run("cached lazy API read", func(t *testing.T) {
		resource := cachedSnapshotResource("default", "target", "1", "value")
		client := fake.NewSimpleDynamicClient(kruntime.NewScheme(), resource)
		resourceStore := newProjectedSnapshotStore(t, client)
		require.NoError(t, resourceStore.Add(cachedSnapshotRef(resource), []string{"default", "target"}))
		assertPinnedReadWhileFenced(t, resourceStore, func(ctx context.Context) error {
			snapshot, err := resourceStore.Pin()
			if err != nil {
				return err
			}
			contextual := snapshot.(stores.ContextReadSnapshot)
			_, err = contextual.GetContext(ctx, "default", "target")
			return err
		})
	})
}

func assertPinnedReadWhileFenced(
	t *testing.T,
	fencer stores.SnapshotCommitFencer,
	read func(context.Context) error,
) {
	t.Helper()
	release, err := fencer.AcquireSnapshotCommitFence(t.Context())
	require.NoError(t, err)
	defer release()
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	result := make(chan error, 1)
	go func() {
		result <- read(ctx)
	}()
	select {
	case readErr := <-result:
		require.NoError(t, readErr)
	case <-ctx.Done():
		t.Fatalf("pinned read blocked on snapshot commit fence: %v", ctx.Err())
	}
}
