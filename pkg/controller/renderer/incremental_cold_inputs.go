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
	"strings"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/rendercontext"
	"gitlab.com/haproxy-haptic/haptic/pkg/core/config"
	"gitlab.com/haproxy-haptic/haptic/pkg/k8s/indexer"
	"gitlab.com/haproxy-haptic/haptic/pkg/stores"
	"gitlab.com/haproxy-haptic/haptic/pkg/templating"
)

type strictIncrementalHTTPFetcher struct {
	base templating.HTTPFetcher
}

func (f *strictIncrementalHTTPFetcher) Fetch(args ...any) (any, error) {
	canonical, err := templating.CanonicalIncrementalHTTPArgs(args...)
	if err != nil {
		return nil, err
	}
	return f.base.Fetch(canonical...)
}

type coldIncrementalStoreSnapshot struct {
	items     []any
	indexSize int
	byKey     map[string][]any
}

type coldIncrementalResourceView struct {
	snapshots map[string]*coldIncrementalStoreSnapshot

	// instanceContext carries the immutable inputs (item, props, render
	// subject) of the instance currently rendering. Serving it from here
	// rather than baking it into each StoreWrapper is what lets the resources
	// facade — every adapter and its reflect.MakeFunc trampolines — be built
	// once per render instead of once per instance. The cold renderer resolves
	// instances serially, so a single slot is enough.
	instanceContext context.Context
}

// StoreReadContext implements the store's read-context provider, which
// StoreWrapper prefers over its own baked context.
func (v *coldIncrementalResourceView) StoreReadContext() context.Context {
	return v.instanceContext
}

var (
	_ rendercontext.StoreSnapshotView        = (*coldIncrementalResourceView)(nil)
	_ rendercontext.StoreLookupKeyNormalizer = (*coldIncrementalResourceView)(nil)
)

func newColdIncrementalResourceView(
	ctx context.Context,
	cfg *config.Config,
	required map[string]struct{},
	provider stores.StoreProvider,
) (*coldIncrementalResourceView, map[string]stores.Store, error) {
	if cfg == nil {
		return nil, nil, errors.New("incremental renderer has no configuration")
	}
	if provider == nil {
		return nil, nil, errors.New("incremental renderer has no store provider")
	}
	storesByName := make(map[string]stores.Store, len(cfg.WatchedResources))
	names := watchedResourceNames(cfg)
	for _, name := range names {
		storesByName[name] = provider.GetStore(name)
	}
	requiredNames := make([]string, 0, len(required))
	for name := range required {
		requiredNames = append(requiredNames, name)
	}
	slices.Sort(requiredNames)
	for _, name := range requiredNames {
		if _, configured := cfg.WatchedResources[name]; !configured {
			return nil, nil, fmt.Errorf("incremental component requires unknown resource %q", name)
		}
	}
	view := &coldIncrementalResourceView{
		snapshots: make(map[string]*coldIncrementalStoreSnapshot, len(names)),
	}
	for _, name := range names {
		watched := cfg.WatchedResources[name]
		store := storesByName[name]
		if store == nil {
			if _, isRequired := required[name]; isRequired {
				return nil, nil, fmt.Errorf("incremental component requires unavailable resource %q", name)
			}
			continue
		}
		items, err := listColdIncrementalStore(ctx, store)
		if err != nil {
			return nil, nil, fmt.Errorf("snapshotting cold incremental resource %q: %w", name, err)
		}
		snapshot, err := newColdIncrementalStoreSnapshot(name, watched.IndexBy, items)
		if err != nil {
			return nil, nil, err
		}
		view.snapshots[name] = snapshot
	}
	return view, storesByName, nil
}

func addColdIncrementalControllerResources(
	ctx context.Context,
	baseContext map[string]any,
	view *coldIncrementalResourceView,
	storesByName map[string]stores.Store,
) error {
	controller, _ := baseContext[incrementalControllerContextName].(map[string]templating.ResourceStore)
	fields := make([]string, 0, len(controller))
	for field := range controller {
		fields = append(fields, field)
	}
	slices.Sort(fields)
	for _, field := range fields {
		wrapper, ok := controller[field].(*rendercontext.StoreWrapper)
		if !ok || wrapper == nil || wrapper.Store == nil {
			return fmt.Errorf("controller resource %q cannot build an immutable cold snapshot", field)
		}
		alias := wrapper.ResourceType
		if alias == "" {
			return fmt.Errorf("controller resource %q has no resource type", field)
		}
		if _, exists := view.snapshots[alias]; exists {
			return fmt.Errorf("controller resource %q conflicts with watched resource %q", field, alias)
		}
		items, err := listColdIncrementalStore(ctx, wrapper.Store)
		if err != nil {
			return fmt.Errorf("snapshotting cold controller resource %q: %w", field, err)
		}
		snapshot, err := newColdIncrementalStoreSnapshot(alias, wrapper.IndexBy, items)
		if err != nil {
			return err
		}
		storesByName[alias] = wrapper.Store
		view.snapshots[alias] = snapshot
	}
	return nil
}

func listColdIncrementalStore(ctx context.Context, store stores.Store) ([]any, error) {
	if contextual, ok := store.(stores.ContextLister); ok {
		return contextual.ListContext(ctx)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	items, err := store.List()
	if ctxErr := ctx.Err(); ctxErr != nil {
		return nil, ctxErr
	}
	return items, err
}

func newColdIncrementalStoreSnapshot(
	resourceType string,
	indexBy []string,
	items []any,
) (*coldIncrementalStoreSnapshot, error) {
	snapshot := &coldIncrementalStoreSnapshot{
		items:     make([]any, 0, len(items)),
		indexSize: len(indexBy),
	}
	var idx *indexer.Indexer
	if len(indexBy) > 0 {
		var err error
		idx, err = indexer.New(indexer.Config{IndexBy: slices.Clone(indexBy)})
		if err != nil {
			return nil, fmt.Errorf("indexing cold incremental resource %q: %w", resourceType, err)
		}
		snapshot.byKey = make(map[string][]any)
	}
	for position, raw := range items {
		item, err := plainColdIncrementalResource(raw)
		if err != nil {
			return nil, fmt.Errorf("snapshotting cold incremental resource %q item %d: %w", resourceType, position, err)
		}
		snapshot.items = append(snapshot.items, item)
		if idx == nil {
			continue
		}
		keys, err := idx.ExtractKeys(item)
		if err != nil {
			return nil, fmt.Errorf("indexing cold incremental resource %q item %d: %w", resourceType, position, err)
		}
		encoded := indexer.EncodeKey(keys)
		snapshot.byKey[encoded] = append(snapshot.byKey[encoded], item)
	}
	slices.SortFunc(snapshot.items, compareColdIncrementalResources)
	for key := range snapshot.byKey {
		slices.SortFunc(snapshot.byKey[key], compareColdIncrementalResources)
	}
	return snapshot, nil
}

func compareColdIncrementalResources(left, right any) int {
	leftNamespace, leftName, _ := resourceIdentity(left)
	rightNamespace, rightName, _ := resourceIdentity(right)
	if compared := strings.Compare(leftNamespace, rightNamespace); compared != 0 {
		return compared
	}
	return strings.Compare(leftName, rightName)
}

func (*coldIncrementalResourceView) NormalizeLookupKeys(_ string, keys []any) ([]string, error) {
	return templating.CanonicalIncrementalResourceKeys(keys...)
}

func (v *coldIncrementalResourceView) List(resourceType string, _ stores.Store) ([]any, error) {
	snapshot, exists := v.snapshots[resourceType]
	if !exists {
		return nil, fmt.Errorf("resource %q is unavailable to the incremental component", resourceType)
	}
	return cloneColdIncrementalResources(snapshot.items)
}

func (v *coldIncrementalResourceView) Get(
	resourceType string,
	_ stores.Store,
	keys ...string,
) ([]any, error) {
	snapshot, exists := v.snapshots[resourceType]
	if !exists {
		return nil, fmt.Errorf("resource %q is unavailable to the incremental component", resourceType)
	}
	if snapshot.indexSize == 0 {
		return nil, fmt.Errorf("resource %q has no indexBy for incremental lookup", resourceType)
	}
	if len(keys) == 0 || len(keys) > snapshot.indexSize {
		return nil, fmt.Errorf("resource %q lookup has %d keys; pass between 1 and %d", resourceType, len(keys), snapshot.indexSize)
	}
	encoded := indexer.EncodeKey(keys)
	if len(keys) == snapshot.indexSize {
		return cloneColdIncrementalResources(snapshot.byKey[encoded])
	}
	var result []any
	for key, items := range snapshot.byKey {
		if indexer.HasEncodedKeyPrefix(key, encoded) {
			result = append(result, items...)
		}
	}
	slices.SortFunc(result, compareColdIncrementalResources)
	return cloneColdIncrementalResources(result)
}

func cloneColdIncrementalResources(items []any) ([]any, error) {
	result := make([]any, len(items))
	for index := range items {
		item, err := cloneNormalizedColdIncrementalResource(items[index])
		if err != nil {
			return nil, err
		}
		result[index] = item
	}
	return result, nil
}

// cloneNormalizedColdIncrementalResource copies a resource that
// plainColdIncrementalResource has already normalized.
//
// The snapshot stores normalized objects, so re-running the JSON round trip on
// them re-derives a shape they already have: it was 24% of a cold render's
// allocations. Copying structurally gives the same value. Raw store items are
// NOT normalized and must still go through plainColdIncrementalResource.
func cloneNormalizedColdIncrementalResource(value any) (map[string]any, error) {
	object, ok := value.(map[string]any)
	if !ok {
		return plainColdIncrementalResource(value)
	}
	copied, _ := cloneNormalizedResourceValue(object).(map[string]any)
	return copied, nil
}

// cloneNormalizedResourceValue deep-copies the container types a normalized
// resource holds. Scalars are immutable, so they are shared.
func cloneNormalizedResourceValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		copied := make(map[string]any, len(typed))
		for key, element := range typed {
			copied[key] = cloneNormalizedResourceValue(element)
		}
		return copied
	case []any:
		copied := make([]any, len(typed))
		for index, element := range typed {
			copied[index] = cloneNormalizedResourceValue(element)
		}
		return copied
	default:
		return value
	}
}

func plainColdIncrementalResource(value any) (map[string]any, error) {
	encoded, err := encodeResourceValue(value)
	if err != nil {
		return nil, err
	}
	decoded, err := decodeResourceValue(encoded)
	if err != nil {
		return nil, err
	}
	object, ok := decoded.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("incremental source contains %T, expected an object", value)
	}
	return object, nil
}

func coldIncrementalRenderSubject(
	baseContext map[string]any,
	mode rendercontext.RenderMode,
	source, namespace, name string,
) (map[string]any, error) {
	selectedMode := string(rendercontext.RenderModeReconcile)
	if mode == rendercontext.RenderModeAdmission {
		subject, _ := baseContext["admissionSubject"].(map[string]any)
		if len(subject) == 0 || admissionSubjectMatches(subject, source, namespace, name) {
			selectedMode = string(rendercontext.RenderModeAdmission)
		}
	}
	encoded, err := encodeResourceValue(map[string]any{
		"mode":                       selectedMode,
		incrementalSourceContextName: source,
		"namespace":                  namespace,
		"name":                       name,
	})
	if err != nil {
		return nil, err
	}
	decoded, err := decodeResourceValue(encoded)
	if err != nil {
		return nil, err
	}
	return decoded.(map[string]any), nil
}
