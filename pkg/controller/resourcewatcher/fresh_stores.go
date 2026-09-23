package resourcewatcher

import (
	"context"
	"fmt"
	"sync"

	"golang.org/x/sync/errgroup"

	"gitlab.com/haproxy-haptic/haptic/pkg/k8s/types"
)

// FreshStores snapshots each watched collection directly from the API server.
func (r *ResourceWatcherComponent) FreshStores(ctx context.Context) (map[string]types.Store, error) {
	result := r.GetAllStores()
	var mu sync.Mutex
	group, groupCtx := errgroup.WithContext(ctx)
	group.SetLimit(4)
	for name, watcher := range r.watchers {
		group.Go(func() error {
			fresh, err := watcher.FreshStore(groupCtx)
			if err != nil {
				return fmt.Errorf("refreshing %s: %w", name, err)
			}
			mu.Lock()
			result[name] = fresh
			mu.Unlock()
			return nil
		})
	}
	if err := group.Wait(); err != nil {
		return nil, err
	}
	return result, nil
}
