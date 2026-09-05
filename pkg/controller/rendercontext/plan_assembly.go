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
	"errors"
	"fmt"
	"slices"
	"strings"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/renderplan"
	"gitlab.com/haproxy-haptic/haptic/pkg/rendercontent"
)

// PostProcessFunc normalises a section body the way the surrounding config was
// normalised — the main template's own post-processor chain.
type PostProcessFunc func(ctx context.Context, text string) (string, error)

// PostProcessBatchFunc returns one normalised text per input in the same order.
type PostProcessBatchFunc func(ctx context.Context, texts []string) ([]string, error)

// Assemble replaces every token line in the rendered output with the section
// text registered for it and returns the final config plus its ordered
// sections. Text between tokens becomes a core section, so the sections
// partition the config: concatenating them reproduces it byte for byte, which
// Assemble asserts before returning.
//
// With no registered section the scan is a single search for the render's
// nonce and the config is returned unchanged.
func (r *PlanRegistry) Assemble(ctx context.Context, rendered string, post PostProcessFunc) (string, []renderplan.Section, error) {
	return r.assemble(ctx, rendered, post, nil, rendercontent.Document{}, false, nil, nil)
}

// AssembleWithBatch uses one ordered call to post-process all section bodies.
func (r *PlanRegistry) AssembleWithBatch(
	ctx context.Context,
	rendered string,
	post PostProcessFunc,
	postBatch PostProcessBatchFunc,
) (string, []renderplan.Section, error) {
	return r.assemble(ctx, rendered, post, postBatch, rendercontent.Document{}, false, nil, nil)
}

func (r *PlanRegistry) assemble(
	ctx context.Context,
	rendered string,
	post PostProcessFunc,
	postBatch PostProcessBatchFunc,
	document rendercontent.Document,
	hasDocument bool,
	cacheSession *RenderCacheSession,
	renderGeneration *renderDocumentGeneration,
) (string, []renderplan.Section, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.validateTokenAuthority(); err != nil {
		return "", nil, err
	}
	if r.prepared != nil {
		if err := r.prepared.ValidateAuthentication(); err != nil {
			return "", nil, err
		}
	}
	r.assembly = nil
	if hasDocument && cacheSession != nil && renderGeneration != nil {
		config, sections, generation, hit, err := cacheSession.loadAssembly(document, r, renderGeneration)
		if err != nil {
			return "", nil, err
		}
		if hit {
			r.acceptAssembledSections(sections)
			r.assembly = generation
			cacheSession.assembly = generation
			return config, sections, nil
		}
	}

	sectionCount := r.sectionCount()
	sectionCapacity := sectionCount + 2
	scanner := &planAssembler{
		ctx:       ctx,
		registry:  r,
		post:      post,
		postBatch: postBatch,
		consumed:  make(map[sectionKey]bool, sectionCount),
		sections:  make([]renderplan.Section, 0, sectionCapacity),
		offsets:   make([]int, 0, sectionCapacity),
	}
	config, sections, err := scanner.run(rendered)
	if err != nil {
		return "", nil, err
	}
	r.acceptAssembledSections(sections)
	if hasDocument && cacheSession != nil && renderGeneration != nil {
		generation, _, err := cacheSession.prepareAssembly(document, r, renderGeneration, config, sections)
		if err != nil {
			return "", nil, err
		}
		r.assembly = generation
	}
	return config, sections, nil
}

func (r *PlanRegistry) acceptAssembledSections(sections []renderplan.Section) {
	r.documentAssembly = nil
	r.assembled = slices.Clone(sections)
	for _, section := range sections {
		if section.Kind != renderplan.SectionKindBackend {
			continue
		}
		backend, exists := r.backends[section.Name]
		if !exists {
			continue
		}
		backend.TextDigest = section.TextDigest
		r.backends[section.Name] = backend
	}
}

type planAssembler struct {
	ctx       context.Context
	registry  *PlanRegistry
	post      PostProcessFunc
	postBatch PostProcessBatchFunc
	processed map[sectionKey]string

	out       []byte
	coreStart int
	coreCount int
	sections  []renderplan.Section
	offsets   []int
	consumed  map[sectionKey]bool
}

func (a *planAssembler) run(rendered string) (string, []renderplan.Section, error) {
	if !strings.Contains(rendered, a.registry.marker()) {
		if a.registry.sectionCount() > 0 {
			return "", nil, fmt.Errorf("plan assembly: %d registered sections but the render emitted no token",
				a.registry.sectionCount())
		}
		return rendered, a.coreOnly(rendered), nil
	}
	if err := a.preparePostProcessedSections(rendered); err != nil {
		return "", nil, err
	}

	a.out = make([]byte, 0, len(rendered))
	for start := 0; start < len(rendered); {
		end := len(rendered)
		if newline := strings.IndexByte(rendered[start:], '\n'); newline >= 0 {
			end = start + newline + 1
		}
		line := rendered[start:end]
		start = end
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
	config := string(a.out)
	for index := range a.sections {
		start := a.offsets[index]
		a.sections[index].Text = config[start : start+a.sections[index].Length]
	}
	return config, a.sections, nil
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
		Text:       rendered,
		TextKnown:  true,
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
	for _, name := range a.registry.profileNames() {
		if err := a.spliceSection(renderplan.SectionKindProfile, name); err != nil {
			return err
		}
	}
	return nil
}

func (a *planAssembler) spliceSection(kind, name string) error {
	key := sectionKey{Kind: kind, Name: name}
	text, registered := a.registry.section(kind, name)
	if !registered {
		return fmt.Errorf("plan assembly: token for unregistered %s %q", kind, name)
	}
	if a.consumed[key] {
		return fmt.Errorf("plan assembly: %s %q spliced more than once", kind, name)
	}
	a.consumed[key] = true

	processed, err := a.postProcess(key, text)
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

func (a *planAssembler) postProcess(key sectionKey, text string) (string, error) {
	if a.processed != nil {
		processed, exists := a.processed[key]
		if !exists {
			return "", fmt.Errorf("batch result is missing %s %q", key.Kind, key.Name)
		}
		return processed, nil
	}
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
		TextKnown:  true,
	})
	a.offsets = append(a.offsets, a.coreStart)
	a.coreStart = len(a.out)
}

// verify asserts the invariants the whole diff pipeline rests on: every
// registered section was spliced exactly once, no token survived, and the
// sections partition the config.
func (a *planAssembler) verify() error {
	sectionCount := a.registry.sectionCount()
	if len(a.consumed) != sectionCount {
		return fmt.Errorf("plan assembly: %d of %d registered sections have no token in the config: %s",
			sectionCount-len(a.consumed), sectionCount, a.unconsumedNames())
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
	return a.registry.unconsumedNames(a.consumed)
}

func (r *PlanRegistry) unconsumedNames(consumed map[sectionKey]bool) string {
	unique := make(map[sectionKey]struct{})
	for key := range r.sections {
		if !consumed[key] {
			unique[key] = struct{}{}
		}
	}
	if r.prepared != nil {
		r.prepared.sections.Root().Walk(func(encoded []byte, _ string) bool {
			kind, name, found := strings.Cut(string(encoded), "\x00")
			key := sectionKey{Kind: kind, Name: name}
			if found && !consumed[key] {
				unique[key] = struct{}{}
			}
			return false
		})
	}
	missing := make([]string, 0, len(unique))
	for key := range unique {
		missing = append(missing, key.Kind+" "+key.Name)
	}
	slices.Sort(missing)
	return strings.Join(missing, ", ")
}

func (r *PlanRegistry) profileNames() []string {
	unique := make(map[string]struct{})
	for key := range r.sections {
		if key.Kind == renderplan.SectionKindProfile {
			unique[key.Name] = struct{}{}
		}
	}
	if r.prepared != nil {
		r.prepared.sections.Root().WalkPrefix(preparedSectionKey(renderplan.SectionKindProfile, ""), func(key []byte, _ string) bool {
			unique[string(key[len(renderplan.SectionKindProfile)+1:])] = struct{}{}
			return false
		})
	}
	names := make([]string, 0, len(unique))
	for name := range unique {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}

type indexedPostProcessBatchError interface {
	BatchIndex() int
}

func (a *planAssembler) preparePostProcessedSections(rendered string) error {
	if a.postBatch == nil {
		return nil
	}
	keys, err := a.orderedSectionKeys(rendered)
	if err != nil {
		return err
	}
	a.processed = make(map[sectionKey]string, len(keys))
	batchKeys := make([]sectionKey, 0, len(keys))
	texts := make([]string, 0, len(keys))
	for _, key := range keys {
		text, exists := a.registry.section(key.Kind, key.Name)
		if !exists {
			return fmt.Errorf("plan assembly: token for unregistered %s %q", key.Kind, key.Name)
		}
		if text == "" {
			a.processed[key] = ""
			continue
		}
		batchKeys = append(batchKeys, key)
		texts = append(texts, text)
	}
	if len(texts) == 0 {
		return nil
	}
	processed, err := a.postBatch(a.ctx, texts)
	if err != nil {
		var indexed indexedPostProcessBatchError
		if errors.As(err, &indexed) {
			index := indexed.BatchIndex()
			if index >= 0 && index < len(batchKeys) {
				key := batchKeys[index]
				return fmt.Errorf("plan assembly: post-processing %s %q: %w", key.Kind, key.Name, err)
			}
		}
		return fmt.Errorf("plan assembly: batch post-processing %d sections: %w", len(batchKeys), err)
	}
	if len(processed) != len(batchKeys) {
		return fmt.Errorf("plan assembly: batch post-processing returned %d of %d sections", len(processed), len(batchKeys))
	}
	for index, key := range batchKeys {
		a.processed[key] = processed[index]
	}
	return nil
}

func (a *planAssembler) orderedSectionKeys(rendered string) ([]sectionKey, error) {
	collector := &sectionKeyCollector{
		registry: a.registry,
		keys:     make([]sectionKey, 0, a.registry.sectionCount()),
		consumed: make(map[sectionKey]bool, a.registry.sectionCount()),
	}
	for start := 0; start < len(rendered); {
		end := len(rendered)
		if newline := strings.IndexByte(rendered[start:], '\n'); newline >= 0 {
			end = start + newline + 1
		}
		line := rendered[start:end]
		start = end
		if err := collector.visitLine(line); err != nil {
			return nil, err
		}
	}
	sectionCount := a.registry.sectionCount()
	if len(collector.consumed) != sectionCount {
		return nil, fmt.Errorf("plan assembly: %d of %d registered sections have no token in the config: %s",
			sectionCount-len(collector.consumed), sectionCount, a.registry.unconsumedNames(collector.consumed))
	}
	return collector.keys, nil
}

func (r *PlanRegistry) sectionCount() int {
	count := len(r.sections)
	if r.prepared == nil {
		return count
	}
	count += r.prepared.sections.Len()
	for key := range r.sections {
		if _, exists := r.prepared.section(key.Kind, key.Name); exists {
			count--
		}
	}
	return count
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
	canonical, known := canonicalSectionKind(kind)
	if !known {
		return planToken{}, false, fmt.Errorf("plan assembly: token %q has unknown kind %q", trimmed, kind)
	}
	if !sectionNamePattern.MatchString(name) {
		return planToken{}, false, fmt.Errorf("plan assembly: token %q has an invalid name %q", trimmed, name)
	}
	// The line can be a view into the whole rendered config; a retained token
	// name must not pin it.
	return planToken{Kind: canonical, Name: strings.Clone(name)}, true, nil
}

func canonicalSectionKind(kind string) (string, bool) {
	switch kind {
	case renderplan.SectionKindProfile:
		return renderplan.SectionKindProfile, true
	case renderplan.SectionKindBackend:
		return renderplan.SectionKindBackend, true
	default:
		return "", false
	}
}
