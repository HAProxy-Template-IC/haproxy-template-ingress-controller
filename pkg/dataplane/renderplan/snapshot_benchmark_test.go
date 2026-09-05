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

package renderplan

import "testing"

func BenchmarkSnapshot3000Entries(b *testing.B) {
	const entries = 3000
	plan := snapshotPlanFixture(entries)
	authority := NewAuthority()
	previous := benchmarkPlanSnapshot(b, authority, plan, nil)
	changedPlan := plan.Clone()
	changedBackend := changedPlan.Backends["backend-001500"]
	changedBackend.Servers[0].Address = "192.0.2.15"
	changedPlan.Backends[changedBackend.Name] = changedBackend
	changedPlan.ComputeID()
	changed := benchmarkPlanSnapshot(b, authority, changedPlan, previous)
	foreign := benchmarkPlanSnapshot(b, NewAuthority(), plan.Clone(), nil)
	fixture := planSnapshotBenchmark{
		plan: plan, changedPlan: changedPlan, authority: authority,
		previous: previous, changed: changed, foreign: foreign,
	}
	b.Run("cold", fixture.cold)
	b.Run("exact-root-reuse", fixture.exactRootReuse)
	b.Run("one-change", fixture.oneChange)
	b.Run("same-root", fixture.sameRoot)
	b.Run("foreign-exact", fixture.foreignExact)
	b.Run("legacy-copy", fixture.legacyCopy)
}

type planSnapshotBenchmark struct {
	plan        *Plan
	changedPlan *Plan
	authority   *Authority
	previous    *Snapshot
	changed     *Snapshot
	foreign     *Snapshot
}

func (f planSnapshotBenchmark) cold(b *testing.B) {
	b.ReportAllocs()
	for range b.N {
		benchmarkPlanSnapshotSink = benchmarkPlanSnapshot(b, NewAuthority(), f.plan, nil)
	}
}

func (f planSnapshotBenchmark) exactRootReuse(b *testing.B) {
	b.ReportAllocs()
	for range b.N {
		benchmarkPlanSnapshotSink = benchmarkPlanSnapshot(b, f.authority, f.plan, f.previous)
	}
}

func (f planSnapshotBenchmark) oneChange(b *testing.B) {
	b.ReportAllocs()
	for range b.N {
		benchmarkPlanSnapshotSink = benchmarkPlanSnapshot(b, f.authority, f.changedPlan, f.previous)
	}
}

func (f planSnapshotBenchmark) sameRoot(b *testing.B) {
	b.ReportAllocs()
	for range b.N {
		var err error
		benchmarkPlanSnapshotBoolSink, err = f.previous.SameRoot(f.previous)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func (f planSnapshotBenchmark) foreignExact(b *testing.B) {
	b.ReportAllocs()
	for range b.N {
		var err error
		benchmarkPlanSnapshotBoolSink, err = f.previous.ExactEqual(f.foreign)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func (f planSnapshotBenchmark) legacyCopy(b *testing.B) {
	b.ReportAllocs()
	for range b.N {
		var err error
		benchmarkPlanLegacySink, err = f.changed.LegacyCopy()
		if err != nil {
			b.Fatal(err)
		}
	}
}

func benchmarkPlanSnapshot(
	tb testing.TB,
	authority *Authority,
	plan *Plan,
	previous *Snapshot,
) *Snapshot {
	tb.Helper()
	snapshot, err := NewSnapshot(authority, plan, previous)
	if err != nil {
		tb.Fatal(err)
	}
	return snapshot
}

var (
	benchmarkPlanSnapshotSink     *Snapshot
	benchmarkPlanLegacySink       *Plan
	benchmarkPlanSnapshotBoolSink bool
)
