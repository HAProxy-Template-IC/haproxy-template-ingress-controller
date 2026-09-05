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

package renderartifact

import (
	"fmt"
	"testing"
)

func BenchmarkSnapshot3000Artifacts(b *testing.B) {
	const count = 3000
	specs := make([]artifactSpec, count)
	for index := range specs {
		specs[index] = artifactSpec{
			descriptor: Descriptor{Family: Map, Path: fmt.Sprintf("map-%06d", index)},
			content:    NewLiteralContent(fmt.Sprintf("key-%06d value-%06d\n", index, index)),
		}
	}
	authority := NewAuthority()
	previous := benchmarkBuildSnapshot(b, authority, nil, specs)
	changed := make([]artifactSpec, len(specs))
	copy(changed, specs)
	changed[count/2].content = NewLiteralContent("changed\n")
	foreign := benchmarkBuildSnapshot(b, NewAuthority(), nil, specs)
	fixture := snapshotBenchmark{
		authority: authority,
		previous:  previous,
		foreign:   foreign,
		specs:     specs,
		changed:   changed,
	}
	b.Run("cold", fixture.cold)
	b.Run("exact-root-reuse", fixture.exactRootReuse)
	b.Run("one-change", fixture.oneChange)
	b.Run("same-root", fixture.sameRoot)
	b.Run("foreign-exact", fixture.foreignExact)
}

type snapshotBenchmark struct {
	authority *Authority
	previous  *Snapshot
	foreign   *Snapshot
	specs     []artifactSpec
	changed   []artifactSpec
}

func (f snapshotBenchmark) cold(b *testing.B) {
	b.ReportAllocs()
	for range b.N {
		benchmarkSnapshotSink = benchmarkBuildSnapshot(b, NewAuthority(), nil, f.specs)
	}
}

func (f snapshotBenchmark) exactRootReuse(b *testing.B) {
	b.ReportAllocs()
	for range b.N {
		benchmarkSnapshotSink = benchmarkBuildSnapshot(b, f.authority, f.previous, f.specs)
	}
}

func (f snapshotBenchmark) oneChange(b *testing.B) {
	b.ReportAllocs()
	for range b.N {
		benchmarkSnapshotSink = benchmarkBuildSnapshot(b, f.authority, f.previous, f.changed)
	}
}

func (f snapshotBenchmark) sameRoot(b *testing.B) {
	b.ReportAllocs()
	for range b.N {
		var err error
		benchmarkBoolSink, err = f.previous.SameRoot(f.previous)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func (f snapshotBenchmark) foreignExact(b *testing.B) {
	b.ReportAllocs()
	for range b.N {
		var err error
		benchmarkBoolSink, err = f.previous.ExactEqual(f.foreign)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func benchmarkBuildSnapshot(b *testing.B, authority *Authority, previous *Snapshot, specs []artifactSpec) *Snapshot {
	b.Helper()
	builder, err := NewBuilder(authority, previous)
	if err != nil {
		b.Fatal(err)
	}
	for _, spec := range specs {
		if err = builder.Add(spec.descriptor, spec.content); err != nil {
			b.Fatal(err)
		}
	}
	snapshot, err := builder.Build()
	if err != nil {
		b.Fatal(err)
	}
	return snapshot
}

var (
	benchmarkSnapshotSink *Snapshot
	benchmarkBoolSink     bool
)
