package stores

// SupportsExactRevisionJournal reports whether journal changes are certified
// complete for the store's immutable snapshot source.
func SupportsExactRevisionJournal(store Store) bool {
	source := ExactRevisionJournalSource(store)
	return source != 0
}

// ExactRevisionJournalSource returns the source whose journal contract is exact.
func ExactRevisionJournalSource(store Store) RevisionSource {
	journal, ok := store.(ExactRevisionJournal)
	if !ok {
		return 0
	}
	return journal.ExactRevisionJournalSource()
}

// HasIdentityOrderedReads reports whether collection order is authenticated for the snapshot source.
func HasIdentityOrderedReads(snapshot ReadSnapshot) bool {
	ordered, ok := snapshot.(IdentityOrderedReadSnapshot)
	return ok && ordered.IdentityOrderSource() != 0 && ordered.IdentityOrderSource() == snapshot.RevisionSource()
}

// SupportsSnapshotCommitFence reports whether fence calls reach an underlying implementation.
func SupportsSnapshotCommitFence(store Store) bool {
	switch value := store.(type) {
	case *CompositeStore:
		return value != nil && value.overlay != nil && value.overlay.IsEmpty() &&
			SupportsSnapshotCommitFence(value.base)
	default:
		_, ok := store.(SnapshotCommitFencer)
		return ok
	}
}
