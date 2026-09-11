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

package rendercontext

import (
	"fmt"
	"slices"
	"testing"

	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/renderplan"
	"gitlab.com/haproxy-haptic/haptic/pkg/rendercontent"
)

var planAssemblyPublicationSink *PreparedRenderCachePublication

type assemblyMembershipFixture struct {
	registry       *PlanRegistry
	oracleRegistry *PlanRegistry
	source         rendercontent.Document
	oracle         rendercontent.Document
	sections       []renderplan.Section
}

func BenchmarkPlanAssemblyMembership(b *testing.B) {
	for _, count := range []int{300, 1000, 3000} {
		base := assemblyMembershipSections(count)
		added := assemblySection{name: "be_added", text: "backend be_added\n    server s1 127.0.0.1:80\n"}
		for _, scenario := range []struct {
			name string
			next []assemblySection
		}{
			{name: "unchanged", next: base},
			{name: "insert-first", next: slices.Insert(slices.Clone(base), 0, added)},
			{name: "insert-middle", next: slices.Insert(slices.Clone(base), count/2, added)},
			{name: "append", next: slices.Insert(slices.Clone(base), count, added)},
			{name: "delete-first", next: slices.Delete(slices.Clone(base), 0, 1)},
			{name: "delete-middle", next: slices.Delete(slices.Clone(base), count/2, count/2+1)},
			{name: "delete-last", next: slices.Delete(slices.Clone(base), count-1, count)},
			{name: "rotate", next: slices.Concat(base[count-1:], base[:count-1])},
		} {
			b.Run(fmt.Sprintf("sections=%d/%s", count, scenario.name), func(b *testing.B) {
				benchmarkAssemblyMembership(b, base, scenario.next)
			})
		}
		b.Run(fmt.Sprintf("sections=%d/cold", count), func(b *testing.B) {
			benchmarkAssemblyCold(b, base)
		})
	}
}

func assemblyMembershipSections(count int) []assemblySection {
	sections := make([]assemblySection, count)
	for index := range sections {
		name := fmt.Sprintf("be_%06d", index)
		sections[index] = assemblySection{
			name: name,
			text: "backend " + name + "\n    server s1 127.0.0.1:80\n",
		}
	}
	return sections
}

func newAssemblyMembershipFixture(
	tb testing.TB,
	harness *assemblyDifferentialHarness,
	sections []assemblySection,
) assemblyMembershipFixture {
	tb.Helper()
	step := &assemblyStep{backends: sections}
	oracleRegistry := harness.registry(tb)
	rendered := harness.declare(tb, oracleRegistry, step)
	registry := harness.registry(tb)
	require.Equal(tb, rendered, harness.declare(tb, registry, step))
	source := harness.sourceDocument(tb, rendered)
	oracle, oracleSections, err := oracleRegistry.AssembleDocument(tb.Context(), source, nil)
	require.NoError(tb, err)
	return assemblyMembershipFixture{
		registry: registry, oracleRegistry: oracleRegistry,
		source: source, oracle: oracle, sections: oracleSections,
	}
}

func benchmarkAssemblyMembership(b *testing.B, base, changed []assemblySection) {
	b.Helper()
	harness := newAssemblyDifferentialHarness(b)
	fixtures := []assemblyMembershipFixture{
		newAssemblyMembershipFixture(b, harness, changed),
		newAssemblyMembershipFixture(b, harness, base),
	}
	assembleMembershipFixture(b, harness, &fixtures[1])
	var reused, rebuilt int
	b.ReportAllocs()
	b.ResetTimer()
	for iteration := range b.N {
		fixture := &fixtures[iteration%len(fixtures)]
		document, sections, reuse := assembleMembershipFixture(b, harness, fixture)
		reused += reuse.Reused
		rebuilt += reuse.Rebuilt
		b.StopTimer()
		assertSameAssembly(b, fixture.oracle, fixture.sections, document, sections)
		harness.assertSamePlan(b, fixture.oracleRegistry, fixture.oracle, fixture.sections,
			fixture.registry, document, sections, nil)
		b.StartTimer()
	}
	b.StopTimer()
	planAssemblyPublicationSink = harness.state.publication
	b.ReportMetric(float64(reused)/float64(b.N), "parts-reused/op")
	b.ReportMetric(float64(rebuilt)/float64(b.N), "parts-rebuilt/op")
}

func assembleMembershipFixture(
	tb testing.TB,
	harness *assemblyDifferentialHarness,
	fixture *assemblyMembershipFixture,
) (rendercontent.Document, []renderplan.Section, AssemblyReuse) {
	tb.Helper()
	session := harness.state.begin(tb)
	generation, err := session.prepareIdentityDocument(fixture.source, harness.proof)
	require.NoError(tb, err)
	document, sections, reuse, err := fixture.registry.assembleDocument(
		tb.Context(), fixture.source, nil, nil, fixture.source, true, session, generation,
	)
	require.NoError(tb, err)
	harness.state.retain(tb, tb.Context(), session)
	return document, sections, reuse
}

func benchmarkAssemblyCold(b *testing.B, sections []assemblySection) {
	b.Helper()
	harness := newAssemblyDifferentialHarness(b)
	fixture := newAssemblyMembershipFixture(b, harness, sections)
	planAssemblyPublicationSink = nil
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		document, assembledSections, err := fixture.registry.AssembleDocument(b.Context(), fixture.source, nil)
		require.NoError(b, err)
		b.StopTimer()
		assertSameAssembly(b, fixture.oracle, fixture.sections, document, assembledSections)
		harness.assertSamePlan(b, fixture.oracleRegistry, fixture.oracle, fixture.sections,
			fixture.registry, document, assembledSections, nil)
		b.StartTimer()
	}
	planAssemblySectionsSink = fixture.sections
}
