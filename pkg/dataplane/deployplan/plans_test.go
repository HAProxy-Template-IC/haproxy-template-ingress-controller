// Copyright 2025 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package deployplan_test

import (
	"encoding/json"
	"maps"
	"slices"
	"strings"
	"testing"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/agent/api"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/deployplan"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/renderplan"
)

// The profile every helper-built dynamic backend inherits from.
const testProfile = "haptic-be"

type planOpt func(*renderplan.Plan)

// planWith builds a plan the way a render would: sections for every profile
// and backend, a file per map and crt-list, and a config digest that follows
// the sections. Digests a test sets itself are kept, so a case can describe a
// change the records do not explain.
func planWith(opts ...planOpt) *renderplan.Plan {
	p := &renderplan.Plan{
		SchemaVersion: renderplan.SchemaVersion,
		Backends:      map[string]renderplan.Backend{},
		Profiles:      map[string]renderplan.Profile{},
		Maps:          map[string]renderplan.Map{},
		CRTLists:      map[string]renderplan.CRTList{},
	}
	for _, opt := range opts {
		opt(p)
	}
	deriveSections(p)
	deriveFiles(p)
	p.ComputeID()
	return p
}

// basePlan is planWith plus the core section and profile every case shares.
func basePlan(opts ...planOpt) *renderplan.Plan {
	return planWith(append([]planOpt{withCore("global", "global\n"), withProfile(testProfile, "body-1")}, opts...)...)
}

func withCore(name, text string) planOpt {
	return func(p *renderplan.Plan) {
		p.Sections = append(p.Sections, renderplan.Section{
			Kind: renderplan.SectionKindCore, Name: name, TextDigest: renderplan.DigestString(text), Length: len(text),
			Text: text, TextKnown: true,
		})
	}
}

func withProfile(name, body string) planOpt {
	return func(p *renderplan.Plan) {
		p.Profiles[name] = renderplan.Profile{Name: name, BodyDigest: renderplan.DigestString(body)}
	}
}

func withBackend(be *renderplan.Backend) planOpt {
	return func(p *renderplan.Plan) {
		cloned := *be
		if !cloned.ContentKnown {
			cloned.Body = []string{cloned.BodyDigest}
			cloned.Comments = []string{cloned.CommentsDigest}
			cloned.ContentKnown = true
		}
		p.Backends[be.Name] = cloned
	}
}

func withMap(m renderplan.Map) planOpt {
	return func(p *renderplan.Plan) { p.Maps[m.Path] = m }
}

func withCRTList(list renderplan.CRTList) planOpt {
	return func(p *renderplan.Plan) { p.CRTLists[list.Path] = list }
}

func withFile(f *renderplan.File) planOpt {
	file := *f
	return func(p *renderplan.Plan) {
		if !file.ContentKnown {
			file.Content, file.ContentKnown = file.Digest, true
		}
		p.Files = append(p.Files, file)
	}
}

func deriveSections(p *renderplan.Plan) {
	for _, name := range slices.Sorted(maps.Keys(p.Profiles)) {
		p.Sections = append(p.Sections, renderplan.Section{
			Kind: renderplan.SectionKindProfile, Name: name, TextDigest: p.Profiles[name].BodyDigest,
			Text: p.Profiles[name].BodyDigest, TextKnown: true,
		})
	}
	for _, name := range slices.Sorted(maps.Keys(p.Backends)) {
		be := p.Backends[name]
		if be.RecordDigest == "" {
			be.RecordDigest = recordDigest(&be)
		}
		if be.TextDigest == "" {
			be.TextDigest = renderplan.DigestString(be.RecordDigest + "|" + be.BodyDigest + "|" + be.CommentsDigest)
		}
		p.Backends[name] = be
		p.Sections = append(p.Sections, renderplan.Section{
			Kind: renderplan.SectionKindBackend, Name: name, TextDigest: be.TextDigest,
			Text: be.TextDigest, TextKnown: true,
		})
	}
}

// deriveFiles adds the files a render writes for the structures in the plan,
// leaving any file the test declared itself untouched.
func deriveFiles(p *renderplan.Plan) {
	declared := map[string]bool{}
	for i := range p.Files {
		declared[p.Files[i].Path] = true
	}
	for _, name := range slices.Sorted(maps.Keys(p.Maps)) {
		if !declared[name] {
			p.Files = append(p.Files, renderplan.File{
				Path: name, Kind: renderplan.FileKindMap, Digest: entriesDigest(p.Maps[name].Entries),
				Content: entriesContent(p.Maps[name].Entries), ContentKnown: true,
			})
		}
	}
	for _, name := range slices.Sorted(maps.Keys(p.CRTLists)) {
		if !declared[name] {
			p.Files = append(p.Files, renderplan.File{
				Path: name, Kind: renderplan.FileKindCRTList, Digest: jsonDigest(p.CRTLists[name].Entries),
				Content: string(jsonBytes(p.CRTLists[name].Entries)), ContentKnown: true,
			})
		}
	}
	if !declared["haproxy.cfg"] {
		p.Files = append(p.Files, renderplan.File{
			Path: "haproxy.cfg", Kind: renderplan.FileKindConfig, Digest: sectionsDigest(p.Sections),
			Content: sectionsContent(p.Sections), ContentKnown: true,
		})
	}
}

func recordDigest(be *renderplan.Backend) string {
	record := *be
	record.RecordDigest, record.TextDigest, record.BodyDigest, record.CommentsDigest = "", "", "", ""
	return jsonDigest(record)
}

// jsonDigest digests a value the way the generator digests a record: through
// its canonical JSON, which sorts map keys and keeps field order.
func jsonDigest(value any) string {
	return renderplan.Digest(jsonBytes(value))
}

func jsonBytes(value any) []byte {
	blob, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return blob
}

func sectionsDigest(sections []renderplan.Section) string {
	parts := make([]string, 0, len(sections))
	for i := range sections {
		parts = append(parts, sections[i].Kind+":"+sections[i].Name+":"+sections[i].TextDigest)
	}
	return renderplan.DigestString(strings.Join(parts, "\n"))
}

func entriesDigest(entries []renderplan.Entry) string {
	return renderplan.DigestString(entriesContent(entries))
}

func entriesContent(entries []renderplan.Entry) string {
	parts := make([]string, 0, len(entries))
	for _, e := range entries {
		parts = append(parts, e.Key+" "+e.Value)
	}
	return strings.Join(parts, "\n")
}

func sectionsContent(sections []renderplan.Section) string {
	var content strings.Builder
	for i := range sections {
		content.WriteString(sections[i].Text)
	}
	return content.String()
}

// dynBackend is a backend the generator declared runtime-eligible.
func dynBackend(name string, servers ...renderplan.Server) *renderplan.Backend {
	return &renderplan.Backend{
		Name:    name,
		Profile: testProfile,
		Mode:    "http",
		Balance: "roundrobin",
		Shape:   renderplan.ShapeDynamic,
		Servers: servers,
	}
}

func structuralBackend(name string, servers ...renderplan.Server) *renderplan.Backend {
	be := dynBackend(name, servers...)
	be.Shape = renderplan.ShapeStructural
	be.ShapeReason = "stick-table in the body"
	be.BodyDigest = renderplan.DigestString("stick-table")
	return be
}

func srv(name, address string, port int) renderplan.Server {
	return renderplan.Server{Name: name, Address: address, Port: port}
}

func entry(key, value string) renderplan.Entry {
	return renderplan.Entry{Key: key, Value: value}
}

// on34 is a pod running the version that has dynamic backends.
func on34(applied *renderplan.Plan) *deployplan.Baseline {
	return &deployplan.Baseline{Applied: applied, Caps: deployplan.CapsFor("3.4.3", nil)}
}

func on33(applied *renderplan.Plan) *deployplan.Baseline {
	return &deployplan.Baseline{Applied: applied, Caps: deployplan.CapsFor("3.3.13", nil)}
}

func withMapsLoaded(b *deployplan.Baseline, paths ...string) *deployplan.Baseline {
	b.Inventory.Maps = paths
	return b
}

func kinds(ops []api.Op) []string {
	if len(ops) == 0 {
		return nil
	}
	list := make([]string, 0, len(ops))
	for i := range ops {
		list = append(list, ops[i].Kind)
	}
	return list
}

func reasonsContain(t *testing.T, reasons []string, want string) {
	t.Helper()
	for _, reason := range reasons {
		if strings.Contains(reason, want) {
			return
		}
	}
	t.Fatalf("no reason contains %q, got %q", want, reasons)
}

func ptr[T any](v T) *T { return &v }

func fileDigest(p *renderplan.Plan, path string) string {
	for i := range p.Files {
		if p.Files[i].Path == path {
			return p.Files[i].Digest
		}
	}
	return ""
}
