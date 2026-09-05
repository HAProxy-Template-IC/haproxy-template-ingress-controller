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

package renderoutput

import (
	"strings"
	"testing"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/renderartifact"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/renderplan"
	"gitlab.com/haproxy-haptic/haptic/pkg/rendercontent"
)

func BenchmarkSnapshotOneMiBConfig(b *testing.B) {
	fixture := newConfigOnlyOutputFixture(b, strings.Repeat("global\n", (1<<20)/len("global\n")))
	document := segmentedOutputDocument(b, fixture.config, 4096)
	legacy := mustOutputSnapshot(
		b, fixture.authority, fixture.config, fixture.plan, fixture.artifacts, nil,
	)
	documentPrevious, err := NewSnapshotFromDocument(
		fixture.authority, document, fixture.plan, fixture.artifacts, nil,
	)
	if err != nil {
		b.Fatal(err)
	}
	plan, err := documentPrevious.PlanSnapshot()
	if err != nil {
		b.Fatal(err)
	}
	b.Run("legacy-validated-exact-reuse", func(b *testing.B) {
		b.ReportAllocs()
		for range b.N {
			benchmarkOutputSnapshotSink = mustOutputSnapshot(
				b, fixture.authority, fixture.config, fixture.plan, fixture.artifacts, legacy,
			)
		}
	})
	b.Run("document-validated-exact-reuse", func(b *testing.B) {
		b.ReportAllocs()
		for range b.N {
			benchmarkOutputSnapshotSink, err = NewSnapshotFromDocument(
				fixture.authority, document, fixture.plan, fixture.artifacts, documentPrevious,
			)
			if err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("authenticated-document-root-reuse", func(b *testing.B) {
		b.ReportAllocs()
		for range b.N {
			var hit bool
			benchmarkOutputSnapshotSink, hit, err = ReusePreviousDocument(
				fixture.authority, documentPrevious, document, plan, fixture.artifacts,
			)
			if err != nil || !hit {
				b.Fatalf("hit=%t error=%v", hit, err)
			}
		}
	})
}

func BenchmarkSnapshot3000Artifacts(b *testing.B) {
	fixture := newOutputBenchmark(b, 3000)
	b.Run("cold", fixture.cold)
	b.Run("document-cold", fixture.documentCold)
	b.Run("validated-exact-reuse", fixture.validatedExactReuse)
	b.Run("document-validated-exact-reuse", fixture.documentValidatedExactReuse)
	b.Run("authenticated-root-reuse", fixture.authenticatedRootReuse)
	b.Run("authenticated-document-root-reuse", fixture.authenticatedDocumentRootReuse)
	b.Run("equal-bytes-document-root", fixture.equalBytesDocumentRoot)
	b.Run("one-change", fixture.oneChange)
	b.Run("same-root", fixture.sameRoot)
	b.Run("foreign-exact", fixture.foreignExact)
}

type outputBenchmark struct {
	fixture          outputFixture
	previous         *Snapshot
	previousPlan     *renderplan.Snapshot
	changedPlan      *renderplan.Plan
	changedArtifacts *renderartifact.Snapshot
	foreign          *Snapshot
	document         rendercontent.Document
	documentPrevious *Snapshot
	documentPlan     *renderplan.Snapshot
	equalDocument    rendercontent.Document
}

func newOutputBenchmark(tb testing.TB, count int) *outputBenchmark {
	tb.Helper()
	fixture := newScaleOutputFixture(tb, count)
	previous := mustOutputSnapshot(tb, fixture.authority, fixture.config, fixture.plan, fixture.artifacts, nil)
	previousPlan, err := previous.PlanSnapshot()
	if err != nil {
		tb.Fatal(err)
	}
	changedPlan := fixture.plan.Clone()
	changedSpecs := cloneArtifactSpecs(fixture.specs)
	changedIndex := len(changedSpecs) / 2
	changedSpecs[changedIndex].content = "changed.example changed-backend\n"
	changedPath := changedSpecs[changedIndex].descriptor.RuntimePath
	changedFile := exactPlanFile(changedPath, renderplan.FileKindMap, false, changedSpecs[changedIndex].content)
	for index := range changedPlan.Files {
		if changedPlan.Files[index].Path == changedPath {
			changedPlan.Files[index] = changedFile
			break
		}
	}
	changedPlan.Maps[changedPath] = renderplan.Map{
		Path: changedPath, Ordered: true,
		Entries: renderplan.ParseMapEntries(changedSpecs[changedIndex].content),
	}
	changedPlan.ComputeID()
	changedArtifacts := buildArtifactSnapshot(tb, fixture.artifactAuthority, fixture.artifacts, changedSpecs)
	foreignFixture := newScaleOutputFixture(tb, count)
	foreign := mustOutputSnapshot(
		tb, foreignFixture.authority, foreignFixture.config,
		foreignFixture.plan, foreignFixture.artifacts, nil,
	)
	document := segmentedOutputDocument(tb, fixture.config, 2)
	documentPrevious, err := NewSnapshotFromDocument(
		fixture.authority, document, fixture.plan, fixture.artifacts, nil,
	)
	if err != nil {
		tb.Fatal(err)
	}
	documentPlan, err := documentPrevious.PlanSnapshot()
	if err != nil {
		tb.Fatal(err)
	}
	return &outputBenchmark{
		fixture: fixture, previous: previous, previousPlan: previousPlan,
		changedPlan: changedPlan, changedArtifacts: changedArtifacts, foreign: foreign,
		document: document, documentPrevious: documentPrevious, documentPlan: documentPlan,
		equalDocument: segmentedOutputDocument(tb, fixture.config, 2),
	}
}

func (f *outputBenchmark) cold(b *testing.B) {
	b.ReportAllocs()
	for range b.N {
		planAuthority := renderplan.NewAuthority()
		authority, err := NewAuthority(planAuthority, f.fixture.artifactAuthority)
		if err != nil {
			b.Fatal(err)
		}
		benchmarkOutputSnapshotSink = mustOutputSnapshot(
			b, authority, f.fixture.config, f.fixture.plan, f.fixture.artifacts, nil,
		)
	}
}

func (f *outputBenchmark) validatedExactReuse(b *testing.B) {
	b.ReportAllocs()
	for range b.N {
		benchmarkOutputSnapshotSink = mustOutputSnapshot(
			b, f.fixture.authority, f.fixture.config,
			f.fixture.plan, f.fixture.artifacts, f.previous,
		)
	}
}

func (f *outputBenchmark) documentCold(b *testing.B) {
	b.ReportAllocs()
	for range b.N {
		planAuthority := renderplan.NewAuthority()
		authority, err := NewAuthority(planAuthority, f.fixture.artifactAuthority)
		if err != nil {
			b.Fatal(err)
		}
		benchmarkOutputSnapshotSink, err = NewSnapshotFromDocument(
			authority, f.document, f.fixture.plan, f.fixture.artifacts, nil,
		)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func (f *outputBenchmark) documentValidatedExactReuse(b *testing.B) {
	b.ReportAllocs()
	for range b.N {
		var err error
		benchmarkOutputSnapshotSink, err = NewSnapshotFromDocument(
			f.fixture.authority, f.document, f.fixture.plan,
			f.fixture.artifacts, f.documentPrevious,
		)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func (f *outputBenchmark) authenticatedRootReuse(b *testing.B) {
	b.ReportAllocs()
	for range b.N {
		var hit bool
		var err error
		benchmarkOutputSnapshotSink, hit, err = ReusePrevious(
			f.fixture.authority, f.previous, f.previousPlan, f.fixture.artifacts,
		)
		if err != nil || !hit {
			b.Fatalf("hit=%t error=%v", hit, err)
		}
	}
}

func (f *outputBenchmark) authenticatedDocumentRootReuse(b *testing.B) {
	b.ReportAllocs()
	for range b.N {
		var hit bool
		var err error
		benchmarkOutputSnapshotSink, hit, err = ReusePreviousDocument(
			f.fixture.authority, f.documentPrevious, f.document,
			f.documentPlan, f.fixture.artifacts,
		)
		if err != nil || !hit {
			b.Fatalf("hit=%t error=%v", hit, err)
		}
	}
}

func (f *outputBenchmark) equalBytesDocumentRoot(b *testing.B) {
	b.ReportAllocs()
	for range b.N {
		var err error
		benchmarkOutputSnapshotSink, err = NewSnapshotFromDocument(
			f.fixture.authority, f.equalDocument, f.fixture.plan,
			f.fixture.artifacts, f.documentPrevious,
		)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func (f *outputBenchmark) oneChange(b *testing.B) {
	b.ReportAllocs()
	for range b.N {
		benchmarkOutputSnapshotSink = mustOutputSnapshot(
			b, f.fixture.authority, f.fixture.config,
			f.changedPlan, f.changedArtifacts, f.previous,
		)
	}
}

func (f *outputBenchmark) sameRoot(b *testing.B) {
	b.ReportAllocs()
	for range b.N {
		var err error
		benchmarkOutputBoolSink, err = f.previous.SameRoot(f.previous)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func (f *outputBenchmark) foreignExact(b *testing.B) {
	b.ReportAllocs()
	for range b.N {
		var err error
		benchmarkOutputBoolSink, err = f.previous.ExactEqual(f.foreign)
		if err != nil {
			b.Fatal(err)
		}
	}
}

var (
	benchmarkOutputSnapshotSink *Snapshot
	benchmarkOutputBoolSink     bool
)

func newConfigOnlyOutputFixture(tb testing.TB, config string) outputFixture {
	tb.Helper()
	plan := &renderplan.Plan{
		SchemaVersion: renderplan.SchemaVersion,
		Sections: []renderplan.Section{
			exactSection(renderplan.SectionKindCore, "core#0", config),
		},
		Files: []renderplan.File{
			exactPlanFile(renderplan.ConfigFilePath, renderplan.FileKindConfig, true, config),
		},
	}
	plan.ComputeID()
	planAuthority := renderplan.NewAuthority()
	artifactAuthority := renderartifact.NewAuthority()
	authority, err := NewAuthority(planAuthority, artifactAuthority)
	if err != nil {
		tb.Fatal(err)
	}
	return outputFixture{
		config: config, plan: plan, planAuthority: planAuthority,
		artifactAuthority: artifactAuthority, authority: authority,
		artifacts: buildArtifactSnapshot(tb, artifactAuthority, nil, nil),
	}
}
