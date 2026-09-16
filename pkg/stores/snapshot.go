package stores

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

var _ SnapshotProvider = (*CompositeStore)(nil)
