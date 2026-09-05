package incremental

import (
	"fmt"
	"testing"
)

func TestDependencyFramePromotionPreservesExactObservations(t *testing.T) {
	frame := newDependencyFrame()
	for index := 11; index >= 0; index-- {
		key := NewInputKey(fmt.Sprintf("input-%02d", index))
		entry := inputEntry{
			revision:  NewRevision(fmt.Sprintf("revision-%02d", index)),
			found:     true,
			changedAt: uint64(index + 1),
		}
		if err := frame.addInput(key, entry); err != nil {
			t.Fatalf("addInput(%d) error = %v", index, err)
		}
		if err := frame.addInput(key, entry); err != nil {
			t.Fatalf("duplicate addInput(%d) error = %v", index, err)
		}
	}
	if frame.dependencyMap == nil || frame.inputMap == nil {
		t.Fatal("large dependency frame did not promote its indexes")
	}

	dependencies := frame.sortedDependencies()
	inputs := frame.sortedInputs()
	if len(dependencies) != 12 || len(inputs) != 12 {
		t.Fatalf("frame lengths = %d dependencies, %d inputs", len(dependencies), len(inputs))
	}
	for index := range inputs {
		want := fmt.Sprintf("input-%02d", index)
		if dependencies[index].key.input.Opaque() != want || inputs[index].Key.Opaque() != want {
			t.Fatalf("frame[%d] = %q/%q, want %q", index,
				dependencies[index].key.input.Opaque(), inputs[index].Key.Opaque(), want)
		}
	}

	conflictKey := NewInputKey("input-00")
	conflict := inputEntry{revision: NewRevision("different"), found: true, changedAt: 1}
	if err := frame.addInput(conflictKey, conflict); err == nil {
		t.Fatal("conflicting observation succeeded after index promotion")
	}
}

func BenchmarkDependencyFrameSmall(b *testing.B) {
	keys := []InputKey{
		NewInputKey("first"),
		NewInputKey("second"),
		NewInputKey("third"),
		NewInputKey("fourth"),
	}
	entries := []inputEntry{
		{revision: NewRevision("r1"), found: true, changedAt: 1},
		{revision: NewRevision("r2"), found: true, changedAt: 1},
		{revision: NewRevision("r3"), found: true, changedAt: 1},
		{revision: NewRevision("r4"), found: true, changedAt: 1},
	}
	b.ReportAllocs()
	for range b.N {
		frame := newDependencyFrame()
		for index := range keys {
			if err := frame.addInput(keys[index], entries[index]); err != nil {
				b.Fatal(err)
			}
		}
		if len(frame.sortedDependencies()) != len(keys) || len(frame.sortedInputs()) != len(keys) {
			b.Fatal("dependency frame lost observations")
		}
	}
}
