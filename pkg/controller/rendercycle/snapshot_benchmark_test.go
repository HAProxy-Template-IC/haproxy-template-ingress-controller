// Copyright 2026 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package rendercycle

import (
	"testing"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/renderoutput"
)

func BenchmarkSnapshot3000Effects(b *testing.B) {
	benchmark := newCycleBenchmark(b, 3000)
	b.Run("authenticated-root-reuse", benchmark.authenticatedRootReuse)
	b.Run("changed-root-seal", benchmark.changedRootSeal)
	b.Run("same-root", benchmark.sameRoot)
	b.Run("child-access", benchmark.childAccess)
	b.Run("foreign-exact", benchmark.foreignExact)
}

type cycleBenchmark struct {
	fixture       cycleFixture
	output        *renderoutput.Snapshot
	effects       effectSnapshots
	changedEvents effectSnapshots
	previous      *Snapshot
	foreign       *Snapshot
}

func newCycleBenchmark(tb testing.TB, count int) *cycleBenchmark {
	tb.Helper()
	fixture := newCycleFixture(tb)
	output := fixture.newOutput(tb, "global\n", nil)
	effects := newEffectSnapshots(tb, "stable", count)
	changedEvents := newEffectSnapshots(tb, "changed", count)
	previous := mustCycleSnapshot(tb, fixture.cycleAuthority, output, effects, nil)

	foreignFixture := newCycleFixture(tb)
	foreignOutput := foreignFixture.newOutput(tb, "global\n", nil)
	foreignEffects := newEffectSnapshots(tb, "stable", count)
	foreign := mustCycleSnapshot(
		tb, foreignFixture.cycleAuthority, foreignOutput, foreignEffects, nil,
	)
	return &cycleBenchmark{
		fixture: fixture, output: output, effects: effects,
		changedEvents: changedEvents, previous: previous, foreign: foreign,
	}
}

func (f *cycleBenchmark) authenticatedRootReuse(b *testing.B) {
	b.ReportAllocs()
	for range b.N {
		benchmarkCycleSnapshotSink = mustCycleSnapshot(
			b, f.fixture.cycleAuthority, f.output, f.effects, f.previous,
		)
	}
}

func (f *cycleBenchmark) changedRootSeal(b *testing.B) {
	effects := effectSnapshots{
		status: f.effects.status, events: f.changedEvents.events, resources: f.effects.resources,
	}
	b.ReportAllocs()
	for range b.N {
		benchmarkCycleSnapshotSink = mustCycleSnapshot(
			b, f.fixture.cycleAuthority, f.output, effects, f.previous,
		)
	}
}

func (f *cycleBenchmark) sameRoot(b *testing.B) {
	b.ReportAllocs()
	for range b.N {
		var err error
		benchmarkCycleBoolSink, err = f.previous.SameRoot(f.previous)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func (f *cycleBenchmark) childAccess(b *testing.B) {
	b.ReportAllocs()
	for range b.N {
		var err error
		benchmarkCycleOutputSink, err = f.previous.OutputSnapshot()
		if err != nil {
			b.Fatal(err)
		}
	}
}

func (f *cycleBenchmark) foreignExact(b *testing.B) {
	b.ReportAllocs()
	for range b.N {
		var err error
		benchmarkCycleBoolSink, err = f.previous.ExactEqual(f.foreign)
		if err != nil || !benchmarkCycleBoolSink {
			b.Fatalf("equal=%t error=%v", benchmarkCycleBoolSink, err)
		}
	}
}

var (
	benchmarkCycleSnapshotSink *Snapshot
	benchmarkCycleOutputSink   *renderoutput.Snapshot
	benchmarkCycleBoolSink     bool
)
