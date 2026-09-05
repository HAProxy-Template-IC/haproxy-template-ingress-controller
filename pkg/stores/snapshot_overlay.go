package stores

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"slices"

	"k8s.io/apimachinery/pkg/runtime"
	ktypes "k8s.io/apimachinery/pkg/types"
)

// SnapshotChange is one identity replacement or deletion with its projected keys.
type SnapshotChange struct {
	Namespace string
	Name      string
	Deleted   bool
	Value     any
	OldKeys   []string
	NewKeys   []string
}

// OverlayReadSnapshot composes projected changes over an immutable base snapshot.
func OverlayReadSnapshot(base ReadSnapshot, changes []SnapshotChange) (ReadSnapshot, error) {
	return OverlayReadSnapshotContext(context.Background(), base, changes)
}

// OverlayReadSnapshotContext composes an overlay without detaching external reads from ctx.
func OverlayReadSnapshotContext(
	ctx context.Context,
	base ReadSnapshot,
	changes []SnapshotChange,
) (ReadSnapshot, error) {
	if base == nil || base.RevisionSource() == 0 {
		return nil, ErrSnapshotUnsupported
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	overlay, err := newProjectedReadSnapshot(ctx, base, changes)
	if err != nil {
		return nil, err
	}
	if len(overlay.ordered) == 0 {
		return base, nil
	}
	return overlay, nil
}

type projectedSnapshotChange struct {
	SnapshotChange
	revision Revision
}

type projectedReadSnapshot struct {
	base     ReadSnapshot
	changes  map[ktypes.NamespacedName]*projectedSnapshotChange
	ordered  []*projectedSnapshotChange
	revision Revision
}

func newProjectedReadSnapshot(
	ctx context.Context,
	base ReadSnapshot,
	changes []SnapshotChange,
) (*projectedReadSnapshot, error) {
	overlay := &projectedReadSnapshot{
		base:    base,
		changes: make(map[ktypes.NamespacedName]*projectedSnapshotChange, len(changes)),
	}
	keyCount := 0
	seen := make(map[ktypes.NamespacedName]struct{}, len(changes))
	for index, candidate := range changes {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		identity := ktypes.NamespacedName{Namespace: candidate.Namespace, Name: candidate.Name}
		if _, duplicate := seen[identity]; duplicate {
			return nil, fmt.Errorf("overlay snapshot identity %s/%s is duplicated: %w",
				candidate.Namespace, candidate.Name, ErrSnapshotUnsupported)
		}
		seen[identity] = struct{}{}
		change, effective, err := normalizeSnapshotChange(ctx, base, &candidate, &keyCount)
		if err != nil {
			return nil, fmt.Errorf("overlay snapshot change %d: %w", index, err)
		}
		if !effective {
			continue
		}
		overlay.changes[identity] = change
		overlay.ordered = append(overlay.ordered, change)
	}
	slices.SortFunc(overlay.ordered, compareProjectedChanges)
	parts := make([]overlayRevisionPart, 0, len(overlay.ordered))
	for _, change := range overlay.ordered {
		parts = append(parts, overlayRevisionPart{operation: "change", payload: []byte(change.revision)})
	}
	overlay.revision = exactRevision(parts)
	return overlay, nil
}

func normalizeSnapshotChange(
	ctx context.Context,
	base ReadSnapshot,
	candidate *SnapshotChange,
	keyCount *int,
) (*projectedSnapshotChange, bool, error) {
	if candidate.Name == "" {
		return nil, false, fmt.Errorf("resource name is empty: %w", ErrSnapshotUnsupported)
	}
	identity := ktypes.NamespacedName{Namespace: candidate.Namespace, Name: candidate.Name}
	baseValue, baseFound, err := readSnapshotIdentity(ctx, base, candidate.Namespace, candidate.Name)
	if err != nil {
		return nil, false, err
	}
	if err := validateSnapshotKeys(candidate, baseFound, keyCount); err != nil {
		return nil, false, err
	}
	if err := validateSnapshotOldProjection(ctx, base, candidate, identity, baseFound); err != nil {
		return nil, false, err
	}
	if candidate.Deleted && !baseFound {
		return nil, false, nil
	}
	var frozen any
	if !candidate.Deleted {
		frozen, err = freezeSnapshotChangeValue(candidate, identity)
		if err != nil {
			return nil, false, err
		}
	}
	if !candidate.Deleted && baseFound &&
		slices.Equal(candidate.OldKeys, candidate.NewKeys) && reflect.DeepEqual(baseValue, frozen) {
		return nil, false, nil
	}
	change, err := newProjectedSnapshotChange(candidate, frozen)
	return change, err == nil, err
}

func validateSnapshotOldProjection(
	ctx context.Context,
	base ReadSnapshot,
	change *SnapshotChange,
	identity ktypes.NamespacedName,
	baseFound bool,
) error {
	if !baseFound {
		return nil
	}
	items, err := readSnapshotGet(ctx, base, change.OldKeys...)
	if err != nil {
		return err
	}
	if !containsSnapshotIdentity(items, identity) {
		return fmt.Errorf("old keys do not contain %s/%s: %w",
			change.Namespace, change.Name, ErrSnapshotUnsupported)
	}
	return nil
}

func freezeSnapshotChangeValue(change *SnapshotChange, identity ktypes.NamespacedName) (any, error) {
	frozen, err := cloneSnapshotValue(change.Value)
	if err != nil {
		return nil, err
	}
	resourceIdentity := getResourceKey(frozen)
	if resourceIdentity == nil || *resourceIdentity != identity {
		return nil, fmt.Errorf("value identity does not match %s/%s: %w",
			change.Namespace, change.Name, ErrSnapshotUnsupported)
	}
	return frozen, nil
}

func newProjectedSnapshotChange(
	candidate *SnapshotChange,
	frozen any,
) (*projectedSnapshotChange, error) {
	normalized := *candidate
	normalized.OldKeys = slices.Clone(candidate.OldKeys)
	normalized.NewKeys = slices.Clone(candidate.NewKeys)
	if candidate.Deleted {
		normalized.Value = nil
		normalized.NewKeys = nil
	} else {
		normalized.Value = frozen
	}
	revision, err := snapshotChangeRevision(&normalized)
	if err != nil {
		return nil, err
	}
	return &projectedSnapshotChange{SnapshotChange: normalized, revision: revision}, nil
}

func validateSnapshotKeys(change *SnapshotChange, baseFound bool, keyCount *int) error {
	if change.Deleted && len(change.NewKeys) != 0 {
		return fmt.Errorf("deleted resource has new keys: %w", ErrSnapshotUnsupported)
	}
	if baseFound && len(change.OldKeys) == 0 {
		return fmt.Errorf("existing resource has no old keys: %w", ErrSnapshotUnsupported)
	}
	if !baseFound && len(change.OldKeys) != 0 {
		return fmt.Errorf("missing resource has old keys: %w", ErrSnapshotUnsupported)
	}
	if !change.Deleted && len(change.NewKeys) == 0 {
		return fmt.Errorf("replacement resource has no new keys: %w", ErrSnapshotUnsupported)
	}
	for _, keys := range [][]string{change.OldKeys, change.NewKeys} {
		if len(keys) == 0 {
			continue
		}
		if *keyCount == 0 {
			*keyCount = len(keys)
		}
		if len(keys) != *keyCount {
			return fmt.Errorf("projected key count changed from %d to %d: %w",
				*keyCount, len(keys), ErrSnapshotUnsupported)
		}
	}
	return nil
}

func containsSnapshotIdentity(items []any, identity ktypes.NamespacedName) bool {
	for _, item := range items {
		if key := getResourceKey(item); key != nil && *key == identity {
			return true
		}
	}
	return false
}

func readSnapshotGet(ctx context.Context, snapshot ReadSnapshot, keys ...string) ([]any, error) {
	if contextual, ok := snapshot.(ContextReadSnapshot); ok {
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

func readSnapshotList(ctx context.Context, snapshot ReadSnapshot) ([]any, error) {
	if contextual, ok := snapshot.(ContextReadSnapshot); ok {
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

func readSnapshotIdentity(
	ctx context.Context,
	snapshot ReadSnapshot,
	namespace, name string,
) (item any, found bool, err error) {
	if contextual, ok := snapshot.(ContextReadSnapshot); ok {
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

func cloneSnapshotValue(value any) (any, error) {
	if object, ok := value.(runtime.Object); ok {
		if isNilSnapshotObject(object) {
			return nil, fmt.Errorf("resource value is nil: %w", ErrSnapshotUnsupported)
		}
		copied := object.DeepCopyObject()
		if copied == nil {
			return nil, fmt.Errorf("resource copy is nil: %w", ErrSnapshotUnsupported)
		}
		return copied, nil
	}
	switch typed := value.(type) {
	case nil, string, bool, int, int8, int16, int32, int64,
		uint, uint8, uint16, uint32, uint64, float32, float64, json.Number:
		return typed, nil
	case map[string]any:
		cloned := make(map[string]any, len(typed))
		for key, item := range typed {
			value, err := cloneSnapshotValue(item)
			if err != nil {
				return nil, err
			}
			cloned[key] = value
		}
		return cloned, nil
	case map[string]string:
		cloned := make(map[string]string, len(typed))
		for key, item := range typed {
			cloned[key] = item
		}
		return cloned, nil
	case []any:
		cloned := make([]any, len(typed))
		for index, item := range typed {
			value, err := cloneSnapshotValue(item)
			if err != nil {
				return nil, err
			}
			cloned[index] = value
		}
		return cloned, nil
	case []string:
		return slices.Clone(typed), nil
	case []byte:
		return slices.Clone(typed), nil
	default:
		return nil, fmt.Errorf("resource value type %T is not immutable: %w", value, ErrSnapshotUnsupported)
	}
}

func isNilSnapshotObject(object runtime.Object) bool {
	if object == nil {
		return true
	}
	value := reflect.ValueOf(object)
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return value.IsNil()
	default:
		return false
	}
}

func snapshotChangeRevision(change *SnapshotChange) (Revision, error) {
	payload, err := json.Marshal(change)
	if err != nil {
		return "", fmt.Errorf("encoding overlay snapshot revision: %w", err)
	}
	return exactRevision([]overlayRevisionPart{{operation: "snapshot", payload: payload}}), nil
}

func compareProjectedChanges(left, right *projectedSnapshotChange) int {
	if byNamespace := cmp.Compare(left.Namespace, right.Namespace); byNamespace != 0 {
		return byNamespace
	}
	return cmp.Compare(left.Name, right.Name)
}

func (s *projectedReadSnapshot) RevisionSource() RevisionSource {
	return s.base.RevisionSource()
}

func (s *projectedReadSnapshot) IdentityOrderSource() RevisionSource {
	if !HasIdentityOrderedReads(s.base) {
		return 0
	}
	return s.RevisionSource()
}

func (s *projectedReadSnapshot) Sequence() uint64 {
	return s.base.Sequence()
}

func (s *projectedReadSnapshot) ListRevision() Revision {
	return combineRevisions("list-overlay", s.base.ListRevision(), s.revision)
}

func (s *projectedReadSnapshot) GetRevision(keys ...string) Revision {
	baseRevision := s.base.GetRevision(keys...)
	if baseRevision == "" {
		return ""
	}
	revisions := []Revision{baseRevision}
	for _, change := range s.ordered {
		if snapshotKeysMatch(change.OldKeys, keys) || snapshotKeysMatch(change.NewKeys, keys) {
			revisions = append(revisions, change.revision)
		}
	}
	if len(revisions) == 1 {
		return baseRevision
	}
	return combineRevisions("get-overlay", revisions...)
}

func (s *projectedReadSnapshot) IdentityRevision(namespace, name string) Revision {
	identity := ktypes.NamespacedName{Namespace: namespace, Name: name}
	change, changed := s.changes[identity]
	if !changed {
		return s.base.IdentityRevision(namespace, name)
	}
	return replacementIdentityRevision(identity, s.base.RevisionSource(), change.revision)
}

func (s *projectedReadSnapshot) Get(keys ...string) ([]any, error) {
	return s.GetContext(context.Background(), keys...)
}

func (s *projectedReadSnapshot) GetContext(ctx context.Context, keys ...string) ([]any, error) {
	items, err := readSnapshotGet(ctx, s.base, keys...)
	if err != nil {
		return nil, err
	}
	result := s.withoutChangedIdentities(items)
	for _, change := range s.ordered {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if !change.Deleted && snapshotKeysMatch(change.NewKeys, keys) {
			value, cloneErr := cloneSnapshotValue(change.Value)
			if cloneErr != nil {
				return nil, cloneErr
			}
			result = append(result, value)
		}
	}
	sortSnapshotItems(result)
	return result, nil
}

func (s *projectedReadSnapshot) List() ([]any, error) {
	return s.ListContext(context.Background())
}

func (s *projectedReadSnapshot) ListContext(ctx context.Context) ([]any, error) {
	items, err := readSnapshotList(ctx, s.base)
	if err != nil {
		return nil, err
	}
	result := s.withoutChangedIdentities(items)
	for _, change := range s.ordered {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if !change.Deleted {
			value, cloneErr := cloneSnapshotValue(change.Value)
			if cloneErr != nil {
				return nil, cloneErr
			}
			result = append(result, value)
		}
	}
	sortSnapshotItems(result)
	return result, nil
}

func (s *projectedReadSnapshot) GetIdentity(namespace, name string) (item any, found bool, err error) {
	return s.GetIdentityContext(context.Background(), namespace, name)
}

func (s *projectedReadSnapshot) GetIdentityContext(
	ctx context.Context,
	namespace, name string,
) (item any, found bool, err error) {
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	identity := ktypes.NamespacedName{Namespace: namespace, Name: name}
	change, changed := s.changes[identity]
	if !changed {
		return readSnapshotIdentity(ctx, s.base, namespace, name)
	}
	if change.Deleted {
		return nil, false, nil
	}
	value, err := cloneSnapshotValue(change.Value)
	if err != nil {
		return nil, false, err
	}
	return value, true, nil
}

func (s *projectedReadSnapshot) withoutChangedIdentities(items []any) []any {
	result := make([]any, 0, len(items)+len(s.ordered))
	for _, item := range items {
		identity := getResourceKey(item)
		if identity == nil {
			result = append(result, item)
			continue
		}
		if _, changed := s.changes[*identity]; !changed {
			result = append(result, item)
		}
	}
	return result
}

func snapshotKeysMatch(projected, query []string) bool {
	return len(query) > 0 && len(query) <= len(projected) && slices.Equal(projected[:len(query)], query)
}

func sortSnapshotItems(items []any) {
	slices.SortStableFunc(items, func(left, right any) int {
		leftIdentity := getResourceKey(left)
		rightIdentity := getResourceKey(right)
		if leftIdentity == nil || rightIdentity == nil {
			return 0
		}
		if byNamespace := cmp.Compare(leftIdentity.Namespace, rightIdentity.Namespace); byNamespace != 0 {
			return byNamespace
		}
		return cmp.Compare(leftIdentity.Name, rightIdentity.Name)
	})
}

var _ ReadSnapshot = (*projectedReadSnapshot)(nil)
var _ ContextReadSnapshot = (*projectedReadSnapshot)(nil)
var _ IdentityOrderedReadSnapshot = (*projectedReadSnapshot)(nil)
