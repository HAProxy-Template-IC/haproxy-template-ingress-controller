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
		"typed supported": {
			store:         &TypesStoreAdapter{Inner: supported},
			wantSupported: true,
		},
		"typed unsupported": {store: &TypesStoreAdapter{Inner: unsupported}},
		"composite supported": {
			store:         NewCompositeStore(supported, NewStoreOverlay()),
			wantSupported: true,
		},
		"composite unsupported": {store: NewCompositeStore(unsupported, NewStoreOverlay())},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			assert.Equal(t, tt.wantSupported, SupportsRevisionJournal(tt.store))
			assert.Equal(t, tt.wantSupported, SupportsSnapshotCommitFence(tt.store))
		})
	}
}
