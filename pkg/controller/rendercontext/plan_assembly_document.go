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
	"context"
	"errors"
	"fmt"
	"strings"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/renderplan"
	"gitlab.com/haproxy-haptic/haptic/pkg/rendercontent"
)

// AssemblyReuse reports how much of the previous assembly a render kept. An
// empty FallbackReason means the whole-document reuse path ran; any other value
// names why the render had to rescan and rebuild the assembly from scratch.
type AssemblyReuse struct {
	Reused         int
	Rebuilt        int
	FallbackReason string
}

const (
	assemblyFallbackNoPrevious      = "no-previous-assembly"
	assemblyFallbackSourceChanged   = "source-document-changed"
	assemblyFallbackPreparedChanged = "prepared-plan-changed"
	assemblyFallbackPostProcess     = "post-process-not-identity"
	assemblyFallbackPreviousShape   = "previous-assembly-shape"
	assemblyFallbackUnregistered    = "section-unregistered"
	assemblyFallbackSectionCount    = "section-count-mismatch"
	assemblyFallbackDuplicate       = "section-spliced-twice"
	assemblyFallbackTokenSurvived   = "token-survived-assembly"
)

// AssembleDocument splices registered sections without materializing one contiguous config.
func (r *PlanRegistry) AssembleDocument(
	ctx context.Context,
	document rendercontent.Document,
	post PostProcessFunc,
) (rendercontent.Document, []renderplan.Section, error) {
	assembled, sections, _, err := r.assembleDocument(ctx, document, post, nil, document, false, nil, nil)
	return assembled, sections, err
}

func (r *PlanRegistry) assembleDocument(
	ctx context.Context,
	source rendercontent.Document,
	post PostProcessFunc,
	postBatch PostProcessBatchFunc,
	rawDocument rendercontent.Document,
	hasRawDocument bool,
	cacheSession *RenderCacheSession,
	renderGeneration *renderDocumentGeneration,
) (rendercontent.Document, []renderplan.Section, AssemblyReuse, error) {
	if err := source.ValidateAuthentication(); err != nil {
		return rendercontent.Document{}, nil, AssemblyReuse{}, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.validateTokenAuthority(); err != nil {
		return rendercontent.Document{}, nil, AssemblyReuse{}, err
	}
	if r.prepared != nil {
		if err := r.prepared.ValidateAuthentication(); err != nil {
			return rendercontent.Document{}, nil, AssemblyReuse{}, err
		}
	}
	r.assembly = nil
	r.documentAssembly = nil
	if hasRawDocument && cacheSession != nil && renderGeneration != nil {
		document, sections, hit, err := r.adoptCachedAssemblyLocked(cacheSession, renderGeneration, rawDocument)
		if err != nil {
			return rendercontent.Document{}, nil, AssemblyReuse{}, err
		}
		if hit {
			return document, sections, AssemblyReuse{Reused: len(sections)}, nil
		}
	}

	var previous *renderAssemblyGeneration
	if candidate, ok, err := cacheSession.previousAssembly(r); err != nil {
		return rendercontent.Document{}, nil, AssemblyReuse{}, err
	} else if ok {
		previous = candidate
	}
	assembler := &documentPlanAssembler{
		ctx:       ctx,
		registry:  r,
		post:      post,
		postBatch: postBatch,
		consumed:  make(map[sectionKey]bool, r.sectionCount()),
		sections:  make([]renderplan.Section, 0, r.sectionCount()+2),
		previous:  previous,
	}
	document, sections, parts, err := assembler.run(source)
	if err != nil {
		return rendercontent.Document{}, nil, AssemblyReuse{}, err
	}
	if err := r.acceptAssembledDocument(document, sections); err != nil {
		return rendercontent.Document{}, nil, AssemblyReuse{}, err
	}
	if hasRawDocument && cacheSession != nil && renderGeneration != nil {
		generation, _, err := cacheSession.prepareAssemblyDocument(
			rawDocument,
			r,
			renderGeneration,
			document,
			parts,
			sections,
		)
		if err != nil {
			return rendercontent.Document{}, nil, AssemblyReuse{}, err
		}
		r.assembly = generation
	}
	return document, sections, assembler.reuse(), nil
}

func (r *PlanRegistry) adoptCachedAssemblyLocked(
	cacheSession *RenderCacheSession,
	renderGeneration *renderDocumentGeneration,
	rawDocument rendercontent.Document,
) (rendercontent.Document, []renderplan.Section, bool, error) {
	document, sections, generation, hit, err := cacheSession.loadAssemblyDocument(
		rawDocument,
		r,
		renderGeneration,
	)
	if err != nil || !hit {
		return rendercontent.Document{}, nil, false, err
	}
	if err := r.acceptAssembledDocument(document, sections); err != nil {
		return rendercontent.Document{}, nil, false, err
	}
	r.assembly = generation
	cacheSession.assembly = generation
	return document, sections, true, nil
}

type documentPlanAssembler struct {
	ctx       context.Context
	registry  *PlanRegistry
	post      PostProcessFunc
	postBatch PostProcessBatchFunc
	processed map[sectionKey]string
	previous  *renderAssemblyGeneration

	output     rendercontent.DocumentBuilder
	parts      []rendercontent.Document
	sections   []renderplan.Section
	consumed   map[sectionKey]bool
	core       strings.Builder
	coreCount  int
	totalBytes int
	sawToken   bool
	reused     int
	rebuilt    int
	fallback   string
}

func (a *documentPlanAssembler) reuse() AssemblyReuse {
	return AssemblyReuse{Reused: a.reused, Rebuilt: a.rebuilt, FallbackReason: a.fallback}
}

func (a *documentPlanAssembler) run(
	source rendercontent.Document,
) (
	assembled rendercontent.Document,
	sections []renderplan.Section,
	parts []rendercontent.Document,
	err error,
) {
	carried, carriedSections, carriedParts, ok, err := a.carryPreviousAssembly(source)
	if err != nil || ok {
		return carried, carriedSections, carriedParts, err
	}
	// One pass over the document: the token order is needed before the
	// post-processing batch, and the parts after it, so the pass records
	// what it saw and the parts are built from that record.
	script, keys, err := a.scanDocument(source)
	if err != nil {
		return rendercontent.Document{}, nil, nil, err
	}
	if !a.sawToken {
		return a.runWithoutTokens(source)
	}
	if err := a.preparePostProcessedSections(keys); err != nil {
		return rendercontent.Document{}, nil, nil, err
	}
	for i := range script {
		if err := a.consumeStep(&script[i]); err != nil {
			return rendercontent.Document{}, nil, nil, err
		}
	}
	if err := a.flushCore(); err != nil {
		return rendercontent.Document{}, nil, nil, err
	}
	if len(a.consumed) != a.registry.sectionCount() {
		return rendercontent.Document{}, nil, nil, fmt.Errorf(
			"plan assembly: %d of %d registered sections have no token in the config: %s",
			a.registry.sectionCount()-len(a.consumed),
			a.registry.sectionCount(),
			a.registry.unconsumedNames(a.consumed),
		)
	}
	var previous *rendercontent.Document
	if a.previous != nil && len(a.previous.parts.values) == len(a.parts) {
		previous = &a.previous.assembled
	}
	document, err := a.output.Build(previous)
	if err != nil {
		return rendercontent.Document{}, nil, nil, err
	}
	length, err := document.Bytes()
	if err != nil {
		return rendercontent.Document{}, nil, nil, err
	}
	if length != a.totalBytes {
		return rendercontent.Document{}, nil, nil, fmt.Errorf(
			"plan assembly: sections cover %d of %d bytes",
			a.totalBytes,
			length,
		)
	}
	return document, a.sections, a.parts, nil
}

func (a *documentPlanAssembler) runWithoutTokens(
	source rendercontent.Document,
) (
	assembled rendercontent.Document,
	sections []renderplan.Section,
	parts []rendercontent.Document,
	err error,
) {
	if a.registry.sectionCount() > 0 {
		return rendercontent.Document{}, nil, nil, fmt.Errorf(
			"plan assembly: %d registered sections but the render emitted no token",
			a.registry.sectionCount(),
		)
	}
	text, err := source.String()
	if err != nil {
		return rendercontent.Document{}, nil, nil, err
	}
	sections = (&planAssembler{}).coreOnly(text)
	if text == "" {
		return rendercontent.EmptyDocument(), sections, nil, nil
	}
	var output rendercontent.DocumentBuilder
	if err := output.AppendDocument(source); err != nil {
		return rendercontent.Document{}, nil, nil, err
	}
	var previous *rendercontent.Document
	if a.previous != nil && len(a.previous.parts.values) == 1 {
		previous = &a.previous.assembled
	}
	document, err := output.Build(previous)
	if err != nil {
		return rendercontent.Document{}, nil, nil, err
	}
	a.rebuilt = 1
	return document, sections, []rendercontent.Document{source}, nil
}

// carryPreviousAssembly rebuilds only the sections whose emitted bytes changed.
// It reads the previous emission order instead of rescanning the source, so it
// is sound only while the source root, the token authority and the prepared
// declarations are all the ones that produced that emission.
func (a *documentPlanAssembler) carryPreviousAssembly(source rendercontent.Document) (
	assembled rendercontent.Document,
	sections []renderplan.Section,
	parts []rendercontent.Document,
	ok bool,
	err error,
) {
	if reason := a.carryPrecondition(source); reason != "" {
		a.fallback = reason
		return rendercontent.Document{}, nil, nil, false, nil
	}
	previous := a.previous
	sections = make([]renderplan.Section, len(previous.sections.values))
	parts = make([]rendercontent.Document, len(previous.sections.values))
	consumed := make(map[sectionKey]bool, a.registry.sectionCount())
	replaced := make([]int, 0, 8)
	total := 0
	for index := range previous.sections.values {
		section := previous.sections.values[index]
		if section.Kind == renderplan.SectionKindCore {
			sections[index], parts[index] = section, previous.parts.values[index]
			a.reused++
			total += section.Length
			continue
		}
		rebuilt, reason, carryErr := a.carrySection(&section, consumed, sections, parts, index)
		if carryErr != nil {
			return rendercontent.Document{}, nil, nil, false, carryErr
		}
		if reason != "" {
			return a.abandonCarry(reason)
		}
		if rebuilt {
			replaced = append(replaced, index)
		}
		total += sections[index].Length
	}
	if len(consumed) != a.registry.sectionCount() {
		return a.abandonCarry(assemblyFallbackSectionCount)
	}
	assembled, err = carryAssembledDocument(previous.assembled, parts, replaced)
	if err != nil {
		return rendercontent.Document{}, nil, nil, false, err
	}
	length, err := assembled.Bytes()
	if err != nil {
		return rendercontent.Document{}, nil, nil, false, err
	}
	if length != total {
		return rendercontent.Document{}, nil, nil, false, fmt.Errorf(
			"plan assembly: sections cover %d of %d bytes", total, length)
	}
	return assembled, sections, parts, true, nil
}

func (a *documentPlanAssembler) carrySection(
	section *renderplan.Section,
	consumed map[sectionKey]bool,
	sections []renderplan.Section,
	parts []rendercontent.Document,
	index int,
) (rebuilt bool, reason string, err error) {
	key := sectionKey{Kind: section.Kind, Name: section.Name}
	text, registered := a.registry.section(key.Kind, key.Name)
	switch {
	case !registered:
		return false, assemblyFallbackUnregistered, nil
	case consumed[key]:
		return false, assemblyFallbackDuplicate, nil
	}
	consumed[key] = true
	if sameEmittedSectionText(text, section.Text) {
		sections[index], parts[index] = *section, a.previous.parts.values[index]
		a.reused++
		return false, "", nil
	}
	emitted := emittedSectionText(text)
	if strings.Contains(emitted, a.registry.marker()) {
		return false, assemblyFallbackTokenSurvived, nil
	}
	part, err := renderDocumentFromString(emitted)
	if err != nil {
		return false, "", err
	}
	sections[index] = renderplan.Section{
		Kind: key.Kind, Name: key.Name, TextDigest: renderplan.DigestString(emitted),
		Length: len(emitted), Text: emitted, TextKnown: true,
	}
	parts[index] = part
	a.rebuilt++
	return true, "", nil
}

func (a *documentPlanAssembler) carryPrecondition(source rendercontent.Document) string {
	switch {
	case a.previous == nil:
		return assemblyFallbackNoPrevious
	case a.post != nil || a.postBatch != nil:
		return assemblyFallbackPostProcess
	case a.previous.document != source:
		return assemblyFallbackSourceChanged
	case a.previous.prepared != a.registry.prepared:
		return assemblyFallbackPreparedChanged
	}
	count := len(a.previous.sections.values)
	if count == 0 || len(a.previous.parts.values) != count {
		return assemblyFallbackPreviousShape
	}
	if leaves, err := a.previous.assembled.Leaves(); err != nil || leaves != count {
		return assemblyFallbackPreviousShape
	}
	return ""
}

func (a *documentPlanAssembler) abandonCarry(reason string) (
	assembled rendercontent.Document,
	sections []renderplan.Section,
	parts []rendercontent.Document,
	carried bool,
	err error,
) {
	a.reused, a.rebuilt, a.fallback = 0, 0, reason
	return rendercontent.Document{}, nil, nil, false, nil
}

func carryAssembledDocument(
	base rendercontent.Document,
	parts []rendercontent.Document,
	replaced []int,
) (rendercontent.Document, error) {
	if len(replaced) == 0 {
		return base, nil
	}
	transaction, err := base.BeginTransaction()
	if err != nil {
		return rendercontent.Document{}, err
	}
	for _, index := range replaced {
		handle, handleErr := base.LeafHandle(index)
		if handleErr != nil {
			return rendercontent.Document{}, handleErr
		}
		if err := transaction.ReplaceDocument(handle, parts[index]); err != nil {
			return rendercontent.Document{}, err
		}
	}
	next, _, err := transaction.Commit()
	return next, err
}

// emittedSectionText ends a section the way its token line ended, so the
// section cannot fuse with whatever follows it.
func emittedSectionText(text string) string {
	if strings.HasSuffix(text, "\n") {
		return text
	}
	return text + "\n"
}

func sameEmittedSectionText(text, emitted string) bool {
	if strings.HasSuffix(text, "\n") {
		return text == emitted
	}
	return len(emitted) == len(text)+1 && emitted[len(text)] == '\n' && emitted[:len(text)] == text
}

type sectionKeyCollector struct {
	registry *PlanRegistry
	keys     []sectionKey
	consumed map[sectionKey]bool
}

// visitLine classifies one line and records its keys, for the string
// assembler that still walks the config line by line.
func (c *sectionKeyCollector) visitLine(line string) error {
	if _, _, ok := c.registry.cutFragmentToken(line); ok {
		// Not a section: it contributes no key and no post-processing unit.
		return nil
	}
	token, isToken, err := c.registry.classifyLine(line)
	if err != nil || !isToken {
		return err
	}
	return c.collect(token)
}

// collect records a classified token's keys in emission order.
func (c *sectionKeyCollector) collect(token planToken) error {
	if token.Group {
		for _, name := range c.registry.profileNames() {
			if err := c.appendKey(sectionKey{Kind: renderplan.SectionKindProfile, Name: name}); err != nil {
				return err
			}
		}
		return nil
	}
	return c.appendKey(sectionKey{Kind: token.Kind, Name: token.Name})
}

func (c *sectionKeyCollector) appendKey(key sectionKey) error {
	if _, exists := c.registry.section(key.Kind, key.Name); !exists {
		return fmt.Errorf("plan assembly: token for unregistered %s %q", key.Kind, key.Name)
	}
	if c.consumed[key] {
		return fmt.Errorf("plan assembly: %s %q spliced more than once", key.Kind, key.Name)
	}
	c.consumed[key] = true
	c.keys = append(c.keys, key)
	return nil
}

// scanStep is one thing the scan saw, in document order: a run of core
// text, a fragment token with the text before it on its line, or a section
// token, already classified.
type scanStep struct {
	text     string
	fragment string
	token    planToken
	isToken  bool
}

// scanDocument walks the document once and records its steps and the section
// keys in emission order; a group token expands to every profile.
func (a *documentPlanAssembler) scanDocument(source rendercontent.Document) ([]scanStep, []sectionKey, error) {
	script := make([]scanStep, 0, 2*a.registry.sectionCount()+1)
	collector := &sectionKeyCollector{
		registry: a.registry,
		keys:     make([]sectionKey, 0, a.registry.sectionCount()),
		consumed: make(map[sectionKey]bool, a.registry.sectionCount()),
	}
	visitText := func(text string) error {
		script = append(script, scanStep{text: text})
		return nil
	}
	visitLine := func(line string) error {
		a.sawToken = true
		if prefix, name, ok := a.registry.cutFragmentToken(line); ok {
			script = append(script, scanStep{text: prefix, fragment: name})
			return nil
		}
		token, isToken, err := a.registry.classifyLine(line)
		if err != nil {
			return err
		}
		if !isToken {
			return fmt.Errorf("plan assembly: marker line %q is not a token", line)
		}
		if err := collector.collect(token); err != nil {
			return err
		}
		script = append(script, scanStep{token: token, isToken: true})
		return nil
	}
	if err := visitDocumentTokens(source, a.registry.marker(), visitText, visitLine); err != nil {
		return nil, nil, err
	}
	// A registered section without a token is reported once the parts are
	// built: a token inside a fragment is the more specific fault and comes
	// first.
	return script, collector.keys, nil
}

// consumeStep builds the parts from what the scan recorded.
func (a *documentPlanAssembler) consumeStep(step *scanStep) error {
	switch {
	case step.fragment != "":
		if step.text != "" {
			if _, err := a.core.WriteString(step.text); err != nil {
				return err
			}
		}
		// Spliced into the surrounding core text, not flushed as its own
		// section: a fragment must leave the section partition unchanged.
		return a.appendFragment(step.fragment)
	case !step.isToken:
		_, err := a.core.WriteString(step.text)
		return err
	}
	if err := a.flushCore(); err != nil {
		return err
	}
	if step.token.Group {
		for _, name := range a.registry.profileNames() {
			if err := a.spliceSection(renderplan.SectionKindProfile, name); err != nil {
				return err
			}
		}
		return nil
	}
	return a.spliceSection(step.token.Kind, step.token.Name)
}

func (a *documentPlanAssembler) preparePostProcessedSections(keys []sectionKey) error {
	if a.postBatch == nil {
		return nil
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
		return fmt.Errorf(
			"plan assembly: batch post-processing returned %d of %d sections",
			len(processed),
			len(batchKeys),
		)
	}
	for index, key := range batchKeys {
		a.processed[key] = processed[index]
	}
	return nil
}

func (a *documentPlanAssembler) appendFragment(name string) error {
	fragment, registered := a.registry.fragment(name)
	if !registered {
		return fmt.Errorf("plan assembly: token for unregistered fragment %q", name)
	}
	if err := fragment.Walk(func(_, text string) error {
		if strings.Contains(text, a.registry.marker()) {
			return fmt.Errorf("plan assembly: a token survived fragment %q", name)
		}
		_, writeErr := a.core.WriteString(text)
		return writeErr
	}); err != nil {
		return err
	}
	// cutFragmentToken consumed the token line's terminator, so the splice owes
	// one back unless the text already ends with it -- an empty fragment owes it
	// too, or the lines either side of the token fuse and the second is lost.
	if spliced := a.core.String(); spliced != "" && !strings.HasSuffix(spliced, "\n") {
		if _, err := a.core.WriteString("\n"); err != nil {
			return err
		}
	}
	return nil
}

func (a *documentPlanAssembler) flushCore() error {
	if a.core.Len() == 0 {
		return nil
	}
	text := a.core.String()
	a.core.Reset()
	err := a.appendPart(renderplan.SectionKindCore, fmt.Sprintf("core#%d", a.coreCount), text)
	a.coreCount++
	return err
}

func (a *documentPlanAssembler) spliceSection(kind, name string) error {
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
	if err := a.appendPart(kind, name, emittedSectionText(processed)); err != nil {
		return err
	}
	a.recordBackendText(kind, name)
	return nil
}

func (a *documentPlanAssembler) postProcess(key sectionKey, text string) (string, error) {
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

func (a *documentPlanAssembler) appendPart(kind, name, text string) error {
	if len(text) > int(^uint(0)>>1)-a.totalBytes {
		return errors.New("plan assembly: output exceeds the platform limit")
	}
	// Bytes equal to a part the previous render emitted under this same token
	// authority already passed the digest and the surviving-token scan.
	if section, part, ok := a.previousPart(kind, name, text); ok {
		a.reused++
		return a.recordPart(part, &section)
	}
	if marker := strings.Index(text, a.registry.marker()); marker >= 0 {
		return fmt.Errorf("plan assembly: a token survived assembly at byte %d", a.totalBytes+marker)
	}
	part, err := renderDocumentFromString(text)
	if err != nil {
		return err
	}
	a.rebuilt++
	return a.recordPart(part, &renderplan.Section{
		Kind:       kind,
		Name:       name,
		TextDigest: renderplan.DigestString(text),
		Length:     len(text),
		Text:       text,
		TextKnown:  true,
	})
}

func (a *documentPlanAssembler) previousPart(
	kind, name, text string,
) (renderplan.Section, rendercontent.Document, bool) {
	index := len(a.parts)
	if a.previous == nil || index >= len(a.previous.parts.values) ||
		index >= len(a.previous.sections.values) {
		return renderplan.Section{}, rendercontent.Document{}, false
	}
	previous := a.previous.sections.values[index]
	if previous.Kind != kind || previous.Name != name || previous.Text != text {
		return renderplan.Section{}, rendercontent.Document{}, false
	}
	return previous, a.previous.parts.values[index], true
}

func (a *documentPlanAssembler) recordPart(
	part rendercontent.Document,
	section *renderplan.Section,
) error {
	if err := a.output.AppendDocument(part); err != nil {
		return err
	}
	a.parts = append(a.parts, part)
	a.sections = append(a.sections, *section)
	a.totalBytes += section.Length
	return nil
}

func (a *documentPlanAssembler) recordBackendText(kind, name string) {
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

// visitDocumentTokens hands every line that carries marker to visitLine and
// every run of lines between them to visitText (nil to skip them). The marker
// is searched over whole runs rather than line by line: at 3,000 routes the
// root document is 800 KB of which 3,000 lines are tokens, and splitting
// every line cost two 3 ms passes per render.
func visitDocumentTokens(
	document rendercontent.Document,
	marker string,
	visitText func(string) error,
	visitLine func(string) error,
) error {
	if visitLine == nil {
		return errors.New("plan assembly: document token visitor is nil")
	}
	if visitText == nil {
		visitText = func(string) error { return nil }
	}
	visitor := &documentTokenVisitor{marker: marker, visitText: visitText, visitLine: visitLine}
	if _, err := document.WriteTo(visitor); err != nil {
		return err
	}
	if visitor.err != nil {
		return visitor.err
	}
	if visitor.pending.Len() == 0 {
		return nil
	}
	return visitor.visitRun(visitor.pending.String())
}

// documentTokenVisitor splits the document at complete lines: a chunk's tail
// after its last newline waits for the next chunk, so a line or a marker
// that straddles two chunks is seen whole.
type documentTokenVisitor struct {
	marker    string
	visitText func(string) error
	visitLine func(string) error
	pending   strings.Builder
	err       error
}

func (v *documentTokenVisitor) Write(value []byte) (int, error) {
	return v.WriteString(string(value))
}

func (v *documentTokenVisitor) WriteString(value string) (int, error) {
	if v.err != nil {
		return 0, v.err
	}
	written := len(value)
	last := strings.LastIndexByte(value, '\n')
	if last < 0 {
		_, _ = v.pending.WriteString(value)
		return written, nil
	}
	complete := value[:last+1]
	if v.pending.Len() > 0 {
		_, _ = v.pending.WriteString(complete)
		complete = v.pending.String()
		v.pending.Reset()
	}
	if err := v.visitRun(complete); err != nil {
		v.err = err
		return written, err
	}
	if rest := value[last+1:]; rest != "" {
		_, _ = v.pending.WriteString(rest)
	}
	return written, nil
}

// visitRun walks text made of complete lines, except possibly the last.
func (v *documentTokenVisitor) visitRun(text string) error {
	for text != "" {
		at := strings.Index(text, v.marker)
		if at < 0 {
			return v.visitText(text)
		}
		lineStart := strings.LastIndexByte(text[:at], '\n') + 1
		lineEnd := len(text)
		if newline := strings.IndexByte(text[at:], '\n'); newline >= 0 {
			lineEnd = at + newline + 1
		}
		if lineStart > 0 {
			if err := v.visitText(text[:lineStart]); err != nil {
				return err
			}
		}
		if err := v.visitLine(text[lineStart:lineEnd]); err != nil {
			return err
		}
		text = text[lineEnd:]
	}
	return nil
}
