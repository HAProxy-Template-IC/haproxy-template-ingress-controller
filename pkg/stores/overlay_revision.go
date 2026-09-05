package stores

import (
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"strconv"

	"k8s.io/apimachinery/pkg/runtime"
	ktypes "k8s.io/apimachinery/pkg/types"
)

type overlayRevisionState struct {
	full       Revision
	identities map[ktypes.NamespacedName]Revision
	values     map[ktypes.NamespacedName][]any
	replaces   map[ktypes.NamespacedName]bool
	ambiguous  bool
}

type overlayRevisionPart struct {
	operation string
	payload   []byte
}

type overlayRevisionBuilder struct {
	state         overlayRevisionState
	fullParts     []overlayRevisionPart
	identityParts map[ktypes.NamespacedName][]overlayRevisionPart
}

func buildOverlayRevisionState(overlay *StoreOverlay) overlayRevisionState {
	builder := newOverlayRevisionBuilder()
	if overlay.IsEmpty() {
		return builder.state
	}
	builder.addObjects("addition", overlay.Additions, overlay.convertedAdditions, false)
	builder.addObjects("modification", overlay.Modifications, overlay.convertedModifications, true)
	builder.addDeletions(overlay.Deletions)
	return builder.finish()
}

func newOverlayRevisionBuilder() *overlayRevisionBuilder {
	return &overlayRevisionBuilder{
		state: overlayRevisionState{
			identities: make(map[ktypes.NamespacedName]Revision),
			values:     make(map[ktypes.NamespacedName][]any),
			replaces:   make(map[ktypes.NamespacedName]bool),
		},
		identityParts: make(map[ktypes.NamespacedName][]overlayRevisionPart),
	}
}

func (b *overlayRevisionBuilder) addObjects(
	operation string,
	objects []runtime.Object,
	converted []any,
	replaces bool,
) {
	for index, object := range objects {
		payload, err := json.Marshal(object)
		if err != nil {
			b.state.ambiguous = true
			continue
		}
		part := overlayRevisionPart{operation: operation, payload: payload}
		b.fullParts = append(b.fullParts, part)
		key := getResourceKey(object)
		if key == nil || key.Name == "" {
			b.state.ambiguous = true
			continue
		}
		b.identityParts[*key] = append(b.identityParts[*key], part)
		value := any(object)
		if index < len(converted) {
			value = converted[index]
		}
		b.state.values[*key] = append(b.state.values[*key], value)
		b.state.replaces[*key] = b.state.replaces[*key] || replaces
	}
}

func (b *overlayRevisionBuilder) addDeletions(deletions []ktypes.NamespacedName) {
	for _, deletion := range deletions {
		if deletion.Name == "" {
			b.state.ambiguous = true
			continue
		}
		payload, err := json.Marshal(deletion)
		if err != nil {
			b.state.ambiguous = true
			continue
		}
		part := overlayRevisionPart{operation: "deletion", payload: payload}
		b.fullParts = append(b.fullParts, part)
		b.identityParts[deletion] = append(b.identityParts[deletion], part)
		b.state.replaces[deletion] = true
	}
}

func (b *overlayRevisionBuilder) finish() overlayRevisionState {
	if b.state.ambiguous {
		return b.state
	}
	b.state.full = exactRevision(b.fullParts)
	for identity, parts := range b.identityParts {
		b.state.identities[identity] = exactRevision(parts)
	}
	return b.state
}

func exactRevision(parts []overlayRevisionPart) Revision {
	encoded := make([]byte, 0, len("overlay-revision-v1"))
	encoded = append(encoded, "overlay-revision-v1"...)
	for _, part := range parts {
		encoded = appendExactRevisionPart(encoded, []byte(part.operation))
		encoded = appendExactRevisionPart(encoded, part.payload)
	}
	return Revision("exact:" + base64.RawURLEncoding.EncodeToString(encoded))
}

func appendExactRevisionPart(encoded, value []byte) []byte {
	var size [8]byte
	binary.BigEndian.PutUint64(size[:], uint64(len(value)))
	encoded = append(encoded, size[:]...)
	return append(encoded, value...)
}

func combineRevisions(scope string, revisions ...Revision) Revision {
	parts := make([]overlayRevisionPart, 0, len(revisions)+1)
	parts = append(parts, overlayRevisionPart{operation: "scope", payload: []byte(scope)})
	for _, revision := range revisions {
		if revision == "" {
			return ""
		}
		parts = append(parts, overlayRevisionPart{operation: "revision", payload: []byte(revision)})
	}
	return exactRevision(parts)
}

func (s *CompositeStore) ListRevision() Revision {
	revisioned, ok := s.base.(Revisioned)
	if !ok {
		return ""
	}
	baseRevision := revisioned.ListRevision()
	if s.overlay.IsEmpty() {
		return baseRevision
	}
	if s.revision.ambiguous {
		return ""
	}
	return combineRevisions("list-overlay", baseRevision, s.revision.full)
}

func (s *CompositeStore) GetRevision(keys ...string) Revision {
	revisioned, ok := s.base.(Revisioned)
	if !ok {
		return ""
	}
	if !s.overlay.IsEmpty() {
		return ""
	}
	return revisioned.GetRevision(keys...)
}

func (s *CompositeStore) IdentityRevision(namespace, name string) Revision {
	revisioned, ok := s.base.(Revisioned)
	if !ok || s.revision.ambiguous {
		return ""
	}
	identity := ktypes.NamespacedName{Namespace: namespace, Name: name}
	overlayRevision, affected := s.revision.identities[identity]
	if !affected {
		return revisioned.IdentityRevision(namespace, name)
	}
	if s.revision.replaces[identity] {
		reader, ok := s.base.(SnapshotReader)
		if !ok || reader.RevisionSource() == 0 {
			return ""
		}
		return replacementIdentityRevision(identity, reader.RevisionSource(), overlayRevision)
	}
	return combineRevisions("identity-overlay", revisioned.IdentityRevision(namespace, name), overlayRevision)
}

func replacementIdentityRevision(
	identity ktypes.NamespacedName,
	source RevisionSource,
	overlay Revision,
) Revision {
	return exactRevision([]overlayRevisionPart{
		{operation: "source", payload: []byte(strconv.FormatUint(uint64(source), 10))},
		{operation: "namespace", payload: []byte(identity.Namespace)},
		{operation: "name", payload: []byte(identity.Name)},
		{operation: "overlay", payload: []byte(overlay)},
	})
}

func (s *CompositeStore) ListSnapshot() (items []any, sequence uint64, err error) {
	if journal, ok := s.base.(RevisionJournal); ok {
		items, sequence, err := journal.ListSnapshot()
		if err != nil {
			return nil, sequence, err
		}
		return s.mergeList(items), sequence, nil
	}
	items, err = s.base.List()
	if err != nil {
		return nil, 0, err
	}
	return s.mergeList(items), 0, nil
}

func (s *CompositeStore) ChangesSince(sequence uint64) (uint64, []RevisionChange, bool) {
	journal, ok := s.base.(RevisionJournal)
	if !ok {
		return 0, nil, false
	}
	if !s.overlay.IsEmpty() {
		current, _, _ := journal.ChangesSince(sequence)
		return current, nil, false
	}
	return journal.ChangesSince(sequence)
}

func (s *CompositeStore) ExactRevisionJournalSource() RevisionSource {
	if s == nil || s.overlay == nil || !s.overlay.IsEmpty() {
		return 0
	}
	return ExactRevisionJournalSource(s.base)
}

func (s *CompositeStore) GetIdentity(namespace, name string) (item any, found bool, err error) {
	identity := ktypes.NamespacedName{Namespace: namespace, Name: name}
	values := s.revision.values[identity]
	if len(values) > 1 {
		return nil, false, ErrIdentityLookupUnsupported
	}
	if len(values) == 1 {
		if !s.revision.replaces[identity] {
			getter, ok := s.base.(IdentityGetter)
			if !ok {
				return nil, false, ErrIdentityLookupUnsupported
			}
			_, found, err := getter.GetIdentity(namespace, name)
			if err != nil {
				return nil, false, err
			}
			if found {
				return nil, false, ErrIdentityLookupUnsupported
			}
		}
		return values[0], true, nil
	}
	if s.revision.replaces[identity] {
		return nil, false, nil
	}
	getter, ok := s.base.(IdentityGetter)
	if !ok {
		return nil, false, ErrIdentityLookupUnsupported
	}
	return getter.GetIdentity(namespace, name)
}

func (s *CompositeStore) RevisionSource() RevisionSource {
	if !s.overlay.IsEmpty() {
		return 0
	}
	reader, ok := s.base.(SnapshotReader)
	if !ok {
		return 0
	}
	return reader.RevisionSource()
}

func (s *CompositeStore) GetSnapshot(
	keys ...string,
) (items []any, revision Revision, sequence uint64, err error) {
	if !s.overlay.IsEmpty() {
		return nil, "", 0, ErrSnapshotUnsupported
	}
	reader, ok := s.base.(SnapshotReader)
	if !ok {
		return nil, "", 0, ErrSnapshotUnsupported
	}
	return reader.GetSnapshot(keys...)
}

func (s *CompositeStore) IdentitySnapshot(
	namespace, name string,
) (item any, found bool, revision Revision, sequence uint64, err error) {
	if !s.overlay.IsEmpty() {
		return nil, false, "", 0, ErrSnapshotUnsupported
	}
	reader, ok := s.base.(SnapshotReader)
	if !ok {
		return nil, false, "", 0, ErrSnapshotUnsupported
	}
	return reader.IdentitySnapshot(namespace, name)
}

var (
	_ Revisioned           = (*CompositeStore)(nil)
	_ RevisionJournal      = (*CompositeStore)(nil)
	_ ExactRevisionJournal = (*CompositeStore)(nil)
	_ IdentityGetter       = (*CompositeStore)(nil)
	_ SnapshotReader       = (*CompositeStore)(nil)
)
