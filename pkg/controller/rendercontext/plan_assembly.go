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

package rendercontext

import (
	"bytes"
	"context"
	"fmt"
	"slices"
	"strings"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/renderplan"
)

// PostProcessFunc normalises a section body the way the surrounding config was
// normalised — the main template's own post-processor chain.
type PostProcessFunc func(ctx context.Context, text string) (string, error)

// Assemble replaces every token line in the rendered output with the section
// text registered for it and returns the final config plus its ordered
// sections. Text between tokens becomes a core section, so the sections
// partition the config: concatenating them reproduces it byte for byte, which
// Assemble asserts before returning.
//
// With no registered section the scan is a single search for the render's
// nonce and the config is returned unchanged.
func (r *PlanRegistry) Assemble(ctx context.Context, rendered string, post PostProcessFunc) (string, []renderplan.Section, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	scanner := &planAssembler{ctx: ctx, registry: r, post: post, consumed: make(map[sectionKey]bool)}
	config, sections, err := scanner.run(rendered)
	if err != nil {
		return "", nil, err
	}
	r.assembled = sections
	return config, sections, nil
}

type planAssembler struct {
	ctx      context.Context
	registry *PlanRegistry
	post     PostProcessFunc

	out       []byte
	coreStart int
	coreCount int
	sections  []renderplan.Section
	offsets   []int
	consumed  map[sectionKey]bool
}

func (a *planAssembler) run(rendered string) (string, []renderplan.Section, error) {
	if !strings.Contains(rendered, a.registry.marker()) {
		if len(a.registry.sections) > 0 {
			return "", nil, fmt.Errorf("plan assembly: %d registered sections but the render emitted no token",
				len(a.registry.sections))
		}
		return rendered, a.coreOnly(rendered), nil
	}

	a.out = make([]byte, 0, len(rendered))
	for _, line := range strings.SplitAfter(rendered, "\n") {
		if line == "" {
			continue
		}
		token, isToken, err := a.registry.classifyLine(line)
		if err != nil {
			return "", nil, err
		}
		if !isToken {
			a.out = append(a.out, line...)
			continue
		}
		a.flushCore()
		if err := a.splice(token); err != nil {
			return "", nil, err
		}
	}
	a.flushCore()

	if err := a.verify(); err != nil {
		return "", nil, err
	}
	return string(a.out), a.sections, nil
}

// coreOnly is the section list of a config no template split up.
func (a *planAssembler) coreOnly(rendered string) []renderplan.Section {
	if rendered == "" {
		return nil
	}
	return []renderplan.Section{{
		Kind:       renderplan.SectionKindCore,
		Name:       "core#0",
		TextDigest: renderplan.DigestString(rendered),
		Length:     len(rendered),
	}}
}

// flushCore closes the run of untokenised text that ended at the current
// output position.
func (a *planAssembler) flushCore() {
	if len(a.out) == a.coreStart {
		return
	}
	a.record(renderplan.SectionKindCore, fmt.Sprintf("core#%d", a.coreCount))
	a.coreCount++
}

// splice writes the text registered for one token.
func (a *planAssembler) splice(token planToken) error {
	if token.Group {
		return a.spliceProfiles()
	}
	return a.spliceSection(token.Kind, token.Name)
}

func (a *planAssembler) spliceProfiles() error {
	names := make([]string, 0, len(a.registry.sections))
	for key := range a.registry.sections {
		if key.Kind == renderplan.SectionKindProfile {
			names = append(names, key.Name)
		}
	}
	slices.Sort(names)
	for _, name := range names {
		if err := a.spliceSection(renderplan.SectionKindProfile, name); err != nil {
			return err
		}
	}
	return nil
}

func (a *planAssembler) spliceSection(kind, name string) error {
	key := sectionKey{Kind: kind, Name: name}
	text, registered := a.registry.sections[key]
	if !registered {
		return fmt.Errorf("plan assembly: token for unregistered %s %q", kind, name)
	}
	if a.consumed[key] {
		return fmt.Errorf("plan assembly: %s %q spliced more than once", kind, name)
	}
	a.consumed[key] = true

	processed, err := a.postProcess(text)
	if err != nil {
		return fmt.Errorf("plan assembly: post-processing %s %q: %w", kind, name, err)
	}
	a.out = append(a.out, processed...)
	// The token line the section replaces ended the line, so the section must
	// too — otherwise it would fuse with whatever follows.
	if !strings.HasSuffix(processed, "\n") {
		a.out = append(a.out, '\n')
	}
	a.record(kind, name)
	a.recordBackendText(kind, name)
	return nil
}

// recordBackendText completes a backend record with the digest of the section
// text it was emitted from, which only exists once the section is spliced.
func (a *planAssembler) recordBackendText(kind, name string) {
	if kind != renderplan.SectionKindBackend {
		return
	}
	backend, ok := a.registry.backends[name]
	if !ok {
		return
	}
	backend.TextDigest = a.sections[len(a.sections)-1].TextDigest
	a.registry.backends[name] = backend
}

func (a *planAssembler) postProcess(text string) (string, error) {
	if a.post == nil || text == "" {
		return text, nil
	}
	return a.post(a.ctx, text)
}

// record closes the output written since the last section boundary.
func (a *planAssembler) record(kind, name string) {
	text := a.out[a.coreStart:]
	a.sections = append(a.sections, renderplan.Section{
		Kind:       kind,
		Name:       name,
		TextDigest: renderplan.Digest(text),
		Length:     len(text),
	})
	a.offsets = append(a.offsets, a.coreStart)
	a.coreStart = len(a.out)
}

// verify asserts the invariants the whole diff pipeline rests on: every
// registered section was spliced exactly once, no token survived, and the
// sections partition the config.
func (a *planAssembler) verify() error {
	if len(a.consumed) != len(a.registry.sections) {
		return fmt.Errorf("plan assembly: %d of %d registered sections have no token in the config: %s",
			len(a.registry.sections)-len(a.consumed), len(a.registry.sections), a.unconsumedNames())
	}
	if index := bytes.Index(a.out, []byte(a.registry.marker())); index >= 0 {
		return fmt.Errorf("plan assembly: a token survived assembly at byte %d", index)
	}
	return a.verifyPartition()
}

func (a *planAssembler) verifyPartition() error {
	expected := 0
	for i, section := range a.sections {
		if a.offsets[i] != expected {
			return fmt.Errorf("plan assembly: section %q starts at byte %d, expected %d",
				section.Name, a.offsets[i], expected)
		}
		text := a.out[a.offsets[i] : a.offsets[i]+section.Length]
		if digest := renderplan.Digest(text); digest != section.TextDigest {
			return fmt.Errorf("plan assembly: section %q digest %s does not match its bytes (%s)",
				section.Name, section.TextDigest, digest)
		}
		expected += section.Length
	}
	if expected != len(a.out) {
		return fmt.Errorf("plan assembly: sections cover %d of %d bytes", expected, len(a.out))
	}
	return nil
}

func (a *planAssembler) unconsumedNames() string {
	var missing []string
	for key := range a.registry.sections {
		if !a.consumed[key] {
			missing = append(missing, key.Kind+" "+key.Name)
		}
	}
	slices.Sort(missing)
	return strings.Join(missing, ", ")
}

// planToken is one recognised placeholder line.
type planToken struct {
	Kind  string
	Name  string
	Group bool
}

// classifyLine decides whether a line is one of this render's tokens. A line
// that carries the nonce but is not a well-formed token is an error rather
// than text: it would otherwise reach HAProxy as a stray comment.
func (r *PlanRegistry) classifyLine(line string) (planToken, bool, error) {
	trimmed := strings.TrimSpace(line)
	if !strings.Contains(trimmed, r.marker()) {
		return planToken{}, false, nil
	}
	prefix := "# " + r.marker() + ":"
	if !strings.HasPrefix(trimmed, prefix) || !strings.HasSuffix(trimmed, "@") {
		return planToken{}, false, fmt.Errorf("plan assembly: malformed token %q", trimmed)
	}

	body := trimmed[len(prefix) : len(trimmed)-1]
	if body == "group:profiles" {
		return planToken{Group: true}, true, nil
	}
	kind, name, found := strings.Cut(strings.TrimPrefix(body, "section:"), ":")
	if !found || !strings.HasPrefix(body, "section:") {
		return planToken{}, false, fmt.Errorf("plan assembly: unknown token %q", trimmed)
	}
	if kind != renderplan.SectionKindProfile && kind != renderplan.SectionKindBackend {
		return planToken{}, false, fmt.Errorf("plan assembly: token %q has unknown kind %q", trimmed, kind)
	}
	if !sectionNamePattern.MatchString(name) {
		return planToken{}, false, fmt.Errorf("plan assembly: token %q has an invalid name %q", trimmed, name)
	}
	return planToken{Kind: kind, Name: name}, true, nil
}
