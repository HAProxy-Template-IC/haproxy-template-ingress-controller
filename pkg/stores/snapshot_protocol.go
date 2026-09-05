package stores

// SupportsRevisionJournal reports whether journal calls reach an underlying implementation.
func SupportsRevisionJournal(store Store) bool {
	switch value := store.(type) {
	case *TypesStoreAdapter:
		return value != nil && value.Inner != nil && SupportsRevisionJournal(Store(value.Inner))
	case *CompositeStore:
		return value != nil && value.overlay != nil && value.overlay.IsEmpty() &&
			SupportsRevisionJournal(value.base)
	default:
		_, ok := store.(RevisionJournal)
		return ok
	}
}

// SupportsExactRevisionJournal reports whether journal changes are certified
// complete for the store's immutable snapshot source.
func SupportsExactRevisionJournal(store Store) bool {
	source := ExactRevisionJournalSource(store)
	return source != 0
}

// ExactRevisionJournalSource returns the source whose journal contract is exact.
func ExactRevisionJournalSource(store Store) RevisionSource {
	switch value := store.(type) {
	case *TypesStoreAdapter:
		if value == nil || value.Inner == nil {
			return 0
		}
		return ExactRevisionJournalSource(Store(value.Inner))
	case *CompositeStore:
		if value == nil || value.overlay == nil || !value.overlay.IsEmpty() {
			return 0
		}
		return ExactRevisionJournalSource(value.base)
	default:
		journal, ok := store.(ExactRevisionJournal)
		if !ok {
			return 0
		}
		return journal.ExactRevisionJournalSource()
	}
}

// HasIdentityOrderedReads reports whether collection order is authenticated for the snapshot source.
func HasIdentityOrderedReads(snapshot ReadSnapshot) bool {
	ordered, ok := snapshot.(IdentityOrderedReadSnapshot)
	return ok && ordered.IdentityOrderSource() != 0 && ordered.IdentityOrderSource() == snapshot.RevisionSource()
}

// SupportsSnapshotCommitFence reports whether fence calls reach an underlying implementation.
func SupportsSnapshotCommitFence(store Store) bool {
	switch value := store.(type) {
	case *TypesStoreAdapter:
		return value != nil && value.Inner != nil && SupportsSnapshotCommitFence(Store(value.Inner))
	case *CompositeStore:
		return value != nil && value.overlay != nil && value.overlay.IsEmpty() &&
			SupportsSnapshotCommitFence(value.base)
	default:
		_, ok := store.(SnapshotCommitFencer)
		return ok
	}
}
