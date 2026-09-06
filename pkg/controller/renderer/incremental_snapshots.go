// Copyright 2026 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package renderer

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"gitlab.com/haproxy-haptic/haptic/pkg/core/config"
	"gitlab.com/haproxy-haptic/haptic/pkg/k8s/indexer"
	"gitlab.com/haproxy-haptic/haptic/pkg/stores"
)

type incrementalStoreSnapshots struct {
	baseStores     map[string]stores.Store
	renderStores   map[string]stores.Store
	base           map[string]stores.ReadSnapshot
	render         map[string]stores.ReadSnapshot
	overlayChanges map[string][]stores.SnapshotChange
	hasK8sOverlays bool
}

func pinIncrementalStoreSnapshots(
	cfg *config.Config,
	required map[string]struct{},
	provider stores.StoreProvider,
) (*incrementalStoreSnapshots, error) {
	return pinIncrementalStoreSnapshotsContext(context.Background(), cfg, required, provider)
}

func pinIncrementalStoreSnapshotsContext(
	ctx context.Context,
	cfg *config.Config,
	required map[string]struct{},
	provider stores.StoreProvider,
) (*incrementalStoreSnapshots, error) {
	result := newIncrementalStoreSnapshots(cfg)
	overlayProvider, hasOverlayProvider := provider.(*stores.OverlayStoreProvider)
	for _, name := range watchedResourceNames(cfg) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if err := result.pinWatchedResource(
			ctx, cfg, name, required, provider, overlayProvider, hasOverlayProvider,
		); err != nil {
			return nil, err
		}
	}
	return result, nil
}

func newIncrementalStoreSnapshots(cfg *config.Config) *incrementalStoreSnapshots {
	return &incrementalStoreSnapshots{
		baseStores:     make(map[string]stores.Store, len(cfg.WatchedResources)),
		renderStores:   make(map[string]stores.Store, len(cfg.WatchedResources)),
		base:           make(map[string]stores.ReadSnapshot, len(cfg.WatchedResources)),
		render:         make(map[string]stores.ReadSnapshot, len(cfg.WatchedResources)),
		overlayChanges: map[string][]stores.SnapshotChange{},
	}
}

func (s *incrementalStoreSnapshots) pinWatchedResource(
	ctx context.Context,
	cfg *config.Config,
	name string,
	required map[string]struct{},
	provider stores.StoreProvider,
	overlayProvider *stores.OverlayStoreProvider,
	hasOverlayProvider bool,
) error {
	renderStore := provider.GetStore(name)
	baseStore, overlay := incrementalBaseStore(name, renderStore, overlayProvider, hasOverlayProvider)
	s.baseStores[name] = baseStore
	s.renderStores[name] = renderStore

	base, supported, err := pinStoreSnapshot(baseStore)
	if err != nil {
		return fmt.Errorf("pinning watched resource %q: %w", name, err)
	}
	_, isRequired := required[name]
	if !supported {
		if baseStore != nil || isRequired {
			return fmt.Errorf("%w: watched resource %q cannot pin an immutable root",
				errIncrementalUnsupported, name)
		}
		return nil
	}
	if err := validateIncrementalStoreProtocol("watched resource", name, baseStore); err != nil {
		return err
	}
	s.base[name] = base
	s.render[name] = base
	if overlay == nil || overlay.IsEmpty() {
		return nil
	}
	return s.pinOverlay(ctx, cfg, name, base, overlay)
}

// validateIncrementalStoreProtocol checks the store's capabilities without
// touching its fence: this runs under the render state lock, and a commit
// holds the fences while it takes that same lock.
func validateIncrementalStoreProtocol(kind, name string, store stores.Store) error {
	if !stores.SupportsExactRevisionJournal(store) {
		return fmt.Errorf("%w: %s %q has no exact change journal", errIncrementalUnsupported, kind, name)
	}
	if !stores.SupportsSnapshotCommitFence(store) {
		return fmt.Errorf("%w: %s %q has no atomic commit fence", errIncrementalUnsupported, kind, name)
	}
	return nil
}

func incrementalBaseStore(
	name string,
	renderStore stores.Store,
	overlayProvider *stores.OverlayStoreProvider,
	hasOverlayProvider bool,
) (stores.Store, *stores.StoreOverlay) {
	if !hasOverlayProvider {
		return renderStore, nil
	}
	return overlayProvider.GetBaseStore(name), overlayProvider.GetK8sOverlay(name)
}

func (s *incrementalStoreSnapshots) pinOverlay(
	ctx context.Context,
	cfg *config.Config,
	name string,
	base stores.ReadSnapshot,
	overlay *stores.StoreOverlay,
) error {
	changes, err := projectOverlayChangesContext(ctx, cfg.WatchedResources[name].IndexBy, base, overlay)
	if err != nil {
		return incrementalOverlayError("projecting", name, err)
	}
	rendered, err := stores.OverlayReadSnapshotContext(ctx, base, changes)
	if err != nil {
		return incrementalOverlayError("pinning", name, err)
	}
	changes, err = freezeIncrementalOverlayChanges(ctx, rendered, changes)
	if err != nil {
		return incrementalOverlayError("freezing", name, err)
	}
	s.render[name] = rendered
	s.overlayChanges[name] = changes
	s.hasK8sOverlays = true
	return nil
}

func incrementalOverlayError(operation, name string, err error) error {
	if errors.Is(err, stores.ErrSnapshotUnsupported) {
		return fmt.Errorf("%w: watched resource %q cannot pin its admission overlay: %v",
			errIncrementalUnsupported, name, err)
	}
	if operation == "projecting" {
		return fmt.Errorf("projecting admission overlay for watched resource %q: %w", name, err)
	}
	return fmt.Errorf("%s admission overlay for watched resource %q: %w", operation, name, err)
}

func freezeIncrementalOverlayChanges(
	ctx context.Context,
	rendered stores.ReadSnapshot,
	changes []stores.SnapshotChange,
) ([]stores.SnapshotChange, error) {
	frozen := make([]stores.SnapshotChange, len(changes))
	for index := range changes {
		change := &changes[index]
		frozen[index] = stores.SnapshotChange{
			Namespace: change.Namespace,
			Name:      change.Name,
			Deleted:   change.Deleted,
			OldKeys:   slices.Clone(change.OldKeys),
			NewKeys:   slices.Clone(change.NewKeys),
		}
		if change.Deleted {
			continue
		}
		value, found, err := readIncrementalSnapshotIdentity(ctx, rendered, change.Namespace, change.Name)
		if err != nil {
			return nil, err
		}
		if !found {
			return nil, fmt.Errorf("projected identity %s/%s is missing", change.Namespace, change.Name)
		}
		frozen[index].Value = value
	}
	return frozen, nil
}

func rebaseIncrementalOverlaySnapshot(
	ctx context.Context,
	indexBy []string,
	base stores.ReadSnapshot,
	changes []stores.SnapshotChange,
) (stores.ReadSnapshot, error) {
	idx, err := indexer.New(indexer.Config{IndexBy: slices.Clone(indexBy)})
	if err != nil {
		return nil, err
	}
	rebased := make([]stores.SnapshotChange, len(changes))
	for index := range changes {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		change := &changes[index]
		rebased[index] = stores.SnapshotChange{
			Namespace: change.Namespace,
			Name:      change.Name,
			Deleted:   change.Deleted,
			Value:     change.Value,
			NewKeys:   slices.Clone(change.NewKeys),
		}
		current, found, err := readIncrementalSnapshotIdentity(ctx, base, change.Namespace, change.Name)
		if err != nil {
			return nil, err
		}
		if !found {
			continue
		}
		rebased[index].OldKeys, err = idx.ExtractKeys(current)
		if err != nil {
			return nil, fmt.Errorf("indexing current value %s/%s: %w", change.Namespace, change.Name, err)
		}
	}
	return stores.OverlayReadSnapshotContext(ctx, base, rebased)
}

func pinStoreSnapshot(store stores.Store) (stores.ReadSnapshot, bool, error) {
	if store == nil {
		return nil, false, nil
	}
	provider, ok := store.(stores.SnapshotProvider)
	if !ok {
		return nil, false, nil
	}
	snapshot, err := provider.Pin()
	if errors.Is(err, stores.ErrSnapshotUnsupported) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	if snapshot == nil || snapshot.RevisionSource() == 0 {
		return nil, false, nil
	}
	return snapshot, true, nil
}

func projectOverlayChangesContext(
	ctx context.Context,
	indexBy []string,
	base stores.ReadSnapshot,
	overlay *stores.StoreOverlay,
) ([]stores.SnapshotChange, error) {
	if len(indexBy) == 0 {
		return nil, fmt.Errorf("configured indexBy is empty: %w", stores.ErrSnapshotUnsupported)
	}
	idx, err := indexer.New(indexer.Config{IndexBy: slices.Clone(indexBy)})
	if err != nil {
		return nil, err
	}
	identityChanges, exact := overlay.IdentityChanges()
	if !exact {
		return nil, fmt.Errorf("overlay has ambiguous identities: %w", stores.ErrSnapshotUnsupported)
	}
	changes := make([]stores.SnapshotChange, 0, len(identityChanges))
	for _, change := range identityChanges {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		projected := stores.SnapshotChange{
			Namespace: change.Namespace,
			Name:      change.Name,
			Deleted:   change.Deleted,
			Value:     change.Value,
		}
		oldValue, found, err := readIncrementalSnapshotIdentity(ctx, base, change.Namespace, change.Name)
		if err != nil {
			return nil, err
		}
		if found {
			projected.OldKeys, err = idx.ExtractKeys(oldValue)
			if err != nil {
				return nil, fmt.Errorf("indexing old value %s/%s: %w", change.Namespace, change.Name, err)
			}
		}
		if !change.Deleted {
			projected.NewKeys, err = idx.ExtractKeys(change.Value)
			if err != nil {
				return nil, fmt.Errorf("indexing new value %s/%s: %w", change.Namespace, change.Name, err)
			}
		}
		changes = append(changes, projected)
	}
	return changes, nil
}

func readIncrementalSnapshotGet(
	ctx context.Context,
	snapshot stores.ReadSnapshot,
	keys ...string,
) ([]any, error) {
	if contextual, ok := snapshot.(stores.ContextReadSnapshot); ok {
		return contextual.GetContext(ctx, keys...)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	items, err := snapshot.Get(keys...)
	if contextErr := ctx.Err(); contextErr != nil {
		return nil, contextErr
	}
	return items, err
}

func readIncrementalSnapshotList(ctx context.Context, snapshot stores.ReadSnapshot) ([]any, error) {
	if contextual, ok := snapshot.(stores.ContextReadSnapshot); ok {
		return contextual.ListContext(ctx)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	items, err := snapshot.List()
	if contextErr := ctx.Err(); contextErr != nil {
		return nil, contextErr
	}
	return items, err
}

func readIncrementalSnapshotIdentity(
	ctx context.Context,
	snapshot stores.ReadSnapshot,
	namespace, name string,
) (item any, found bool, err error) {
	if contextual, ok := snapshot.(stores.ContextReadSnapshot); ok {
		return contextual.GetIdentityContext(ctx, namespace, name)
	}
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	item, found, err = snapshot.GetIdentity(namespace, name)
	if contextErr := ctx.Err(); contextErr != nil {
		return nil, false, contextErr
	}
	return item, found, err
}

func journalChangesThrough(
	journal stores.RevisionJournal,
	from, through uint64,
) ([]stores.RevisionChange, bool) {
	if journal == nil || through < from {
		return nil, false
	}
	current, changes, complete := journal.ChangesSince(from)
	if !complete || current < through {
		return nil, false
	}
	bounded := make([]stores.RevisionChange, 0, len(changes))
	expected := from
	for _, change := range changes {
		if change.Sequence <= from {
			return nil, false
		}
		if change.Sequence > through {
			break
		}
		if expected == ^uint64(0) || change.Sequence != expected+1 {
			return nil, false
		}
		expected = change.Sequence
		bounded = append(bounded, change)
	}
	if expected != through {
		return nil, false
	}
	return bounded, true
}
