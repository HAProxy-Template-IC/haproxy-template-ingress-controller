package stores

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

type exactSnapshotProtocolStore struct {
	*snapshotFenceMockStore
}

func (s *exactSnapshotProtocolStore) ListSnapshot() (items []any, revision uint64, err error) {
	items, err = s.List()
	return items, 0, err
}

func (*exactSnapshotProtocolStore) ChangesSince(uint64) (uint64, []RevisionChange, bool) {
	return 0, nil, true
}

func (*exactSnapshotProtocolStore) ExactRevisionJournalSource() RevisionSource {
	return 42
}

func TestSnapshotProtocolSupportFollowsAdapters(t *testing.T) {
	supported := &exactSnapshotProtocolStore{
		snapshotFenceMockStore: &snapshotFenceMockStore{mockStore: newMockStore()},
	}
	unsupported := newMockStore()
	tests := map[string]struct {
		store         Store
		wantSupported bool
	}{
		"direct supported":   {store: supported, wantSupported: true},
		"direct unsupported": {store: unsupported},
		"nil":                {},
		"nil composite":      {store: (*CompositeStore)(nil)},
		"nil overlay":        {store: &CompositeStore{base: supported}},
		"composite supported": {
			store:         NewCompositeStore(supported, NewStoreOverlay()),
			wantSupported: true,
		},
		"composite unsupported": {store: NewCompositeStore(unsupported, NewStoreOverlay())},
		"pending overlay": {
			store: NewCompositeStore(supported, NewStoreOverlayForDelete("default", "target")),
		},
		"nested supported": {
			store:         NewCompositeStore(NewCompositeStore(supported, NewStoreOverlay()), NewStoreOverlay()),
			wantSupported: true,
		},
		"nested unsupported": {
			store: NewCompositeStore(NewCompositeStore(unsupported, NewStoreOverlay()), NewStoreOverlay()),
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			assert.Equal(t, tt.wantSupported, SupportsSnapshotCommitFence(tt.store))
			assert.Equal(t, tt.wantSupported, SupportsExactRevisionJournal(tt.store))
			if tt.wantSupported {
				assert.Equal(t, RevisionSource(42), ExactRevisionJournalSource(tt.store))
			} else {
				assert.Zero(t, ExactRevisionJournalSource(tt.store))
			}
		})
	}
}
