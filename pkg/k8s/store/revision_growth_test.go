package store

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRevisionJournalRetainsChangesAcrossGrowthAndWrap(t *testing.T) {
	for _, capacity := range []int{0, 1, 3, 17, 33} {
		t.Run(fmt.Sprintf("capacity=%d", capacity), func(t *testing.T) {
			state := newRevisionState(capacity)
			for sequence := 1; sequence <= 2*capacity+3; sequence++ {
				identity := resourceIdentity{namespace: "default", name: fmt.Sprintf("item-%d", sequence)}
				state.recordUpsert(identity, true, []string{identity.namespace, identity.name})
				oldestAvailable := max(0, sequence-capacity)
				current, changes, complete := state.changesSince(uint64(oldestAvailable))
				require.True(t, complete)
				require.Equal(t, uint64(sequence), current)
				require.Len(t, changes, min(sequence, capacity))
				for index, change := range changes {
					expected := oldestAvailable + index + 1
					require.Equal(t, uint64(expected), change.Sequence)
					require.Equal(t, fmt.Sprintf("item-%d", expected), change.Name)
					require.Equal(t, []string{"default", change.Name}, change.NewKeys)
				}
				if oldestAvailable > 0 {
					_, _, complete = state.changesSince(uint64(oldestAvailable - 1))
					require.False(t, complete)
				}
			}
		})
	}
}

func BenchmarkMemoryStoreFixtureCreation(b *testing.B) {
	for _, count := range []int{0, 1, 8} {
		b.Run(fmt.Sprintf("resources=%d", count), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				resourceStore := NewMemoryStore(2)
				for index := range count {
					name := fmt.Sprintf("item-%d", index)
					if err := resourceStore.Add(namedResource("default", name), []string{"default", name}); err != nil {
						b.Fatal(err)
					}
				}
				if resourceStore.Size() != count {
					b.Fatal("fixture resources were lost")
				}
			}
		})
	}
}
