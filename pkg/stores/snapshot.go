package stores

// Pin delegates immutable snapshot creation to the underlying store.
func (a *TypesStoreAdapter) Pin() (ReadSnapshot, error) {
	provider, ok := a.Inner.(SnapshotProvider)
	if !ok {
		return nil, ErrSnapshotUnsupported
	}
	return provider.Pin()
}

// Pin delegates empty overlays and rejects overlays without projected index keys.
func (s *CompositeStore) Pin() (ReadSnapshot, error) {
	if !s.overlay.IsEmpty() {
		return nil, ErrSnapshotUnsupported
	}
	provider, ok := s.base.(SnapshotProvider)
	if !ok {
		return nil, ErrSnapshotUnsupported
	}
	return provider.Pin()
}

var (
	_ SnapshotProvider = (*TypesStoreAdapter)(nil)
	_ SnapshotProvider = (*CompositeStore)(nil)
)
