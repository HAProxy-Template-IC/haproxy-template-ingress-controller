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
	"io"
	"math"
	"reflect"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/names"
	"gitlab.com/haproxy-haptic/haptic/pkg/rendercontent"
	"gitlab.com/haproxy-haptic/haptic/pkg/templating"
)

// RenderDocumentCache retains the last authenticated root for exact structural reuse.
type RenderDocumentCache struct {
	engine      renderDocumentEngineIdentity
	engineValue templating.Engine
	seal        *RenderDocumentCache
}

type renderCacheGeneration struct {
	owner      *RenderDocumentCache
	occurrence uint64
	document   *renderDocumentGeneration
	assembly   *renderAssemblyGeneration
	plan       *renderPlanGeneration
	seal       *renderCacheGeneration
}

// RenderCacheSession owns one render's private document, assembly, and plan candidate.
type RenderCacheSession struct {
	owner      *RenderDocumentCache
	base       *renderCacheGeneration
	occurrence uint64
	document   *renderDocumentGeneration
	assembly   *renderAssemblyGeneration
	plan       *renderPlanGeneration
	prepared   *PreparedRenderCachePublication
	closed     bool
	seal       *RenderCacheSession
}

// PreparedRenderCachePublication is fully validated and publishes without failure.
type PreparedRenderCachePublication struct {
	owner      *RenderDocumentCache
	candidate  *renderCacheGeneration
	occurrence uint64
	seal       *PreparedRenderCachePublication
}

type renderDocumentEngineIdentity struct {
	typeOf  reflect.Type
	pointer uintptr
}

type renderDocumentGeneration struct {
	owner        *RenderDocumentCache
	templateName string
	document     rendercontent.Document
	processed    string
	proof        *templating.PostProcessReuseProof
	identity     bool
	seal         *renderDocumentGeneration
}

// NewRenderDocumentCache binds structural output reuse to one exact engine.
func NewRenderDocumentCache(engine templating.Engine) (*RenderDocumentCache, error) {
	identity, err := newRenderDocumentEngineIdentity(engine)
	if err != nil {
		return nil, err
	}
	cache := &RenderDocumentCache{engine: identity, engineValue: engine}
	cache.seal = cache
	return cache, nil
}

// Begin captures one exact committed cache occurrence for a render attempt.
// Occurrence zero creates a read-only session, as used by admission.
func (c *RenderDocumentCache) Begin(
	engine templating.Engine,
	occurrence uint64,
	previous *PreparedRenderCachePublication,
) (*RenderCacheSession, error) {
	if c == nil {
		return nil, nil
	}
	if c.seal != c || !c.engine.matches(engine) {
		return nil, errors.New("render document cache authority does not match the engine")
	}
	var base *renderCacheGeneration
	if previous != nil {
		if err := previous.validate(c); err != nil {
			return nil, err
		}
		base = previous.candidate
		if occurrence != 0 && occurrence <= previous.occurrence {
			return nil, errors.New("render cache occurrence does not follow its base")
		}
	}
	session := &RenderCacheSession{
		owner: c, base: base, occurrence: occurrence,
	}
	session.seal = session
	return session, nil
}

func (s *RenderCacheSession) validate() error {
	if s == nil {
		return nil
	}
	if s.seal != s || s.owner == nil || s.owner.seal != s.owner {
		return errors.New("render cache session is invalid")
	}
	if s.base != nil {
		if err := s.base.validate(s.owner); err != nil {
			return errors.New("render cache session has an invalid base")
		}
	}
	return nil
}

func (s *RenderCacheSession) ensureOpen() error {
	if err := s.validate(); err != nil {
		return err
	}
	if s.closed {
		return errors.New("render cache session is already prepared")
	}
	return nil
}

// ReuseBase carries the authenticated document, assembly, and plan roots into
// this occurrence without rebuilding their resource-scaled compatibility data.
func (s *RenderCacheSession) ReuseBase() error {
	if s == nil {
		return errors.New("render cache session is unavailable")
	}
	if err := s.ensureOpen(); err != nil {
		return err
	}
	if s.base == nil {
		return errors.New("render cache session has no committed base")
	}
	if err := s.base.validate(s.owner); err != nil {
		return err
	}
	s.document = s.base.document
	s.assembly = s.base.assembly
	s.plan = s.base.plan
	return nil
}

// Prepare validates the complete private candidate. Admission sessions return nil.
func (s *RenderCacheSession) Prepare(
	ctx context.Context,
) (*PreparedRenderCachePublication, error) {
	if s == nil {
		return nil, nil
	}
	if err := s.ensureOpen(); err != nil {
		return nil, err
	}
	s.closed = true
	if s.occurrence == 0 {
		return nil, nil
	}
	if cause := context.Cause(ctx); cause != nil {
		return nil, cause
	}
	if s.document == nil {
		if s.assembly != nil || s.plan != nil {
			return nil, errors.New("render cache session has descendants without a document")
		}
		return nil, nil
	}
	if err := s.validateCandidateLocked(); err != nil {
		return nil, err
	}
	candidate := &renderCacheGeneration{
		owner:      s.owner,
		occurrence: s.occurrence,
		document:   s.document,
		assembly:   s.assembly,
		plan:       s.plan,
	}
	candidate.seal = candidate
	publication := &PreparedRenderCachePublication{
		owner: s.owner, candidate: candidate, occurrence: s.occurrence,
	}
	publication.seal = publication
	s.prepared = publication
	return publication, nil
}

func (s *RenderCacheSession) validateCandidateLocked() error {
	if err := s.document.validate(s.owner); err != nil {
		return err
	}
	proof, _, err := s.owner.currentPostProcessReuseProof(s.document.templateName)
	if err != nil {
		return err
	}
	if s.document.proof != proof {
		return errors.New("render cache session has a stale post-process proof")
	}
	if s.assembly != nil {
		if err := s.assembly.validate(s.owner); err != nil {
			return err
		}
		if s.assembly.render != s.document {
			return errors.New("render cache assembly belongs to another document")
		}
	}
	if s.plan != nil {
		if err := s.plan.validate(s.owner); err != nil {
			return err
		}
		if s.plan.assembly != s.assembly {
			return errors.New("render cache plan belongs to another assembly")
		}
	}
	return nil
}

// ValidateAuthentication rejects copied, foreign, or structurally invalid publications.
func (p *PreparedRenderCachePublication) ValidateAuthentication() error {
	if p == nil {
		return errors.New("render cache publication is nil")
	}
	return p.validate(p.owner)
}

// Occurrence returns the render occurrence that owns this publication.
func (p *PreparedRenderCachePublication) Occurrence() (uint64, error) {
	if err := p.ValidateAuthentication(); err != nil {
		return 0, err
	}
	return p.occurrence, nil
}

// ValidatePublication verifies that publication belongs to this cache and occurrence.
func (c *RenderDocumentCache) ValidatePublication(
	publication *PreparedRenderCachePublication,
	occurrence uint64,
) error {
	if c == nil || c.seal != c {
		return errors.New("render document cache authority is invalid")
	}
	if err := publication.validate(c); err != nil {
		return err
	}
	if publication.occurrence != occurrence {
		return errors.New("render cache publication belongs to another occurrence")
	}
	return nil
}

func (p *PreparedRenderCachePublication) validate(cache *RenderDocumentCache) error {
	if p == nil || p.seal != p || cache == nil || p.owner != cache || p.candidate == nil ||
		p.occurrence == 0 || p.candidate.occurrence != p.occurrence {
		return errors.New("render cache publication is invalid")
	}
	return p.candidate.validate(cache)
}

func (g *renderCacheGeneration) validate(cache *RenderDocumentCache) error {
	if g == nil || g.owner != cache || g.seal != g || g.occurrence == 0 || g.document == nil {
		return errors.New("render cache generation is invalid")
	}
	if err := g.document.validate(cache); err != nil {
		return err
	}
	if g.assembly != nil {
		if err := g.assembly.validate(cache); err != nil || g.assembly.render != g.document {
			return errors.New("render cache generation has an invalid assembly")
		}
	}
	if g.plan != nil {
		if err := g.plan.validate(cache); err != nil || g.plan.assembly != g.assembly {
			return errors.New("render cache generation has an invalid plan")
		}
	}
	return nil
}

func newRenderDocumentEngineIdentity(engine templating.Engine) (renderDocumentEngineIdentity, error) {
	if engine == nil {
		return renderDocumentEngineIdentity{}, errors.New("render document cache engine is nil")
	}
	value := reflect.ValueOf(engine)
	if value.Kind() != reflect.Pointer || value.IsNil() {
		return renderDocumentEngineIdentity{}, fmt.Errorf("render document cache engine must be a non-nil pointer, got %T", engine)
	}
	return renderDocumentEngineIdentity{typeOf: value.Type(), pointer: value.Pointer()}, nil
}

func (i renderDocumentEngineIdentity) matches(engine templating.Engine) bool {
	value := reflect.ValueOf(engine)
	return value.IsValid() && value.Kind() == reflect.Pointer && !value.IsNil() &&
		i.typeOf == value.Type() && i.pointer == value.Pointer()
}

func (s *RenderCacheSession) load() (rendercontent.Document, bool, error) {
	generation, found, err := s.loadGeneration()
	if err != nil || !found {
		return rendercontent.Document{}, false, err
	}
	return generation.document, true, nil
}

func (s *RenderCacheSession) loadGeneration() (*renderDocumentGeneration, bool, error) {
	if s == nil {
		return nil, false, nil
	}
	if err := s.validate(); err != nil {
		return nil, false, err
	}
	if s.base == nil || s.base.document == nil {
		return nil, false, nil
	}
	generation := s.base.document
	if err := generation.validate(s.owner); err != nil {
		return nil, false, err
	}
	return generation, true, nil
}

func (s *RenderCacheSession) processed(
	ctx context.Context,
	templateName string,
	document rendercontent.Document,
) (processed string, generation *renderDocumentGeneration, hit bool, err error) {
	if s == nil {
		return "", nil, false, nil
	}
	if cause := context.Cause(ctx); cause != nil {
		return "", nil, false, &templating.RenderTimeoutError{TemplateName: templateName, Cause: cause}
	}
	if err := document.ValidateAuthentication(); err != nil {
		return "", nil, false, err
	}
	generation, found, err := s.loadGeneration()
	if err != nil || !found || generation.document != document {
		return "", nil, false, err
	}
	proof, reusable, err := s.owner.currentPostProcessReuseProof(templateName)
	if err != nil {
		return "", nil, false, err
	}
	if !reusable || generation.templateName != templateName || generation.proof != proof {
		return "", nil, false, nil
	}
	if cause := context.Cause(ctx); cause != nil {
		return "", nil, false, &templating.RenderTimeoutError{TemplateName: templateName, Cause: cause}
	}
	if generation.identity {
		processed, err := document.String()
		return processed, generation, err == nil, err
	}
	return generation.processed, generation, true, nil
}

func (s *RenderCacheSession) prepareDocument(
	templateName string,
	document rendercontent.Document,
	processed string,
	reused *renderDocumentGeneration,
) (*renderDocumentGeneration, error) {
	if s == nil {
		return nil, nil
	}
	if err := s.ensureOpen(); err != nil {
		return nil, err
	}
	if err := document.ValidateAuthentication(); err != nil {
		return nil, err
	}
	proof, reusable, err := s.owner.currentPostProcessReuseProof(templateName)
	if err != nil {
		return nil, err
	}
	if reused != nil {
		if err := reused.validate(s.owner); err != nil {
			return nil, err
		}
		if reused.document != document || reused.templateName != templateName ||
			reused.proof != proof || reused.processed != processed {
			return nil, errors.New("render document reuse generation does not match the render")
		}
		s.document = reused
		return reused, nil
	}
	if !reusable {
		processed = ""
	}
	generation := &renderDocumentGeneration{
		owner:        s.owner,
		templateName: templateName,
		document:     document,
		processed:    processed,
		proof:        proof,
	}
	generation.seal = generation
	s.document = generation
	return generation, nil
}

func (s *RenderCacheSession) prepareIdentityDocument(
	document rendercontent.Document,
	proof *templating.PostProcessReuseProof,
) (*renderDocumentGeneration, error) {
	templateName := names.MainTemplateName
	if s == nil {
		return nil, nil
	}
	if err := s.ensureOpen(); err != nil {
		return nil, err
	}
	if err := document.ValidateAuthentication(); err != nil {
		return nil, err
	}
	identity, err := proof.CertifiesIdentity(s.owner.engineValue, templateName)
	if err != nil {
		return nil, err
	}
	if !identity {
		return nil, errors.New("render document identity proof contains post-processors")
	}
	if previous, found, err := s.loadGeneration(); err != nil {
		return nil, err
	} else if found && previous.document == document && previous.templateName == templateName &&
		previous.proof == proof && previous.identity {
		s.document = previous
		return previous, nil
	}
	generation := &renderDocumentGeneration{
		owner:        s.owner,
		templateName: templateName,
		document:     document,
		proof:        proof,
		identity:     true,
	}
	generation.seal = generation
	s.document = generation
	return generation, nil
}

func (c *RenderDocumentCache) currentPostProcessReuseProof(
	templateName string,
) (*templating.PostProcessReuseProof, bool, error) {
	if !c.engine.matches(c.engineValue) {
		return nil, false, errors.New("render document cache proof authority does not match the engine")
	}
	prover, ok := c.engineValue.(templating.PostProcessReuseProver)
	if !ok {
		return nil, false, nil
	}
	proof, err := prover.PostProcessReuseProof(templateName)
	if err != nil {
		return nil, false, err
	}
	if proof == nil {
		return nil, false, nil
	}
	if err := proof.ValidateAuthentication(); err != nil {
		return nil, false, err
	}
	// A wrapper that promotes this proof but overrides PostProcess owns none: refuse reuse, don't fail.
	_, certErr := proof.CertifiesIdentity(c.engineValue, templateName)
	if certErr != nil {
		proof = nil
	}
	return proof, certErr == nil, nil
}

func (g *renderDocumentGeneration) validate(cache *RenderDocumentCache) error {
	if g == nil || g.owner != cache || g.seal != g || g.templateName == "" {
		return errors.New("render document cache generation is invalid")
	}
	if err := g.document.ValidateAuthentication(); err != nil {
		return errors.New("render document cache contains an invalid root")
	}
	if g.proof != nil {
		if err := g.proof.ValidateAuthentication(); err != nil {
			return errors.New("render document cache contains an invalid post-process proof")
		}
		identity, err := g.proof.CertifiesIdentity(cache.engineValue, g.templateName)
		if err != nil {
			return fmt.Errorf("render document cache post-process proof does not match its engine: %w", err)
		}
		if g.identity && !identity {
			return errors.New("render document cache post-process proof does not match its engine")
		}
	} else if g.identity {
		return errors.New("render document cache identity is missing its proof")
	}
	return nil
}

type renderDocumentWriter struct {
	builder rendercontent.DocumentBuilder
	bytes   int64
	last    byte
}

func (w *renderDocumentWriter) Write(value []byte) (int, error) {
	if int64(len(value)) > math.MaxInt64-w.bytes {
		return 0, fmt.Errorf("render document exceeds the platform limit")
	}
	written, err := w.builder.Write(value)
	if written < 0 || written > len(value) {
		return written, fmt.Errorf("render document writer returned invalid count %d", written)
	}
	if err == nil && written != len(value) {
		err = io.ErrShortWrite
	}
	if written > 0 {
		w.last = value[len(value)-1]
		w.bytes += int64(written)
	}
	return written, err
}

func (w *renderDocumentWriter) WriteTextFragment(fragment templating.TextFragment) error {
	if nilTextFragment(fragment) {
		return fmt.Errorf("render text fragment is nil")
	}
	switch value := fragment.(type) {
	case rendercontent.Output:
		return w.appendOutput(value)
	case *rendercontent.Output:
		return w.appendOutput(*value)
	case rendercontent.Document:
		return w.appendDocument(value)
	case *rendercontent.Document:
		return w.appendDocument(*value)
	case rendercontent.TextFragment:
		return w.appendTextFragment(value)
	case *rendercontent.TextFragment:
		return w.appendTextFragment(*value)
	default:
		return w.writeUnknownFragment(fragment)
	}
}

func (w *renderDocumentWriter) appendOutput(output rendercontent.Output) error {
	length, err := output.Bytes()
	if err != nil {
		return err
	}
	if int64(length) > math.MaxInt64-w.bytes {
		return fmt.Errorf("render document exceeds the platform limit")
	}
	last, exists, err := output.LastByte()
	if err != nil {
		return err
	}
	if err := w.builder.AppendOutput(output); err != nil {
		return err
	}
	w.bytes += int64(length)
	if exists {
		w.last = last
	}
	return nil
}

func (w *renderDocumentWriter) appendDocument(document rendercontent.Document) error {
	length, err := document.Bytes()
	if err != nil {
		return err
	}
	if int64(length) > math.MaxInt64-w.bytes {
		return fmt.Errorf("render document exceeds the platform limit")
	}
	last, exists, err := document.LastByte()
	if err != nil {
		return err
	}
	if err := w.builder.AppendDocument(document); err != nil {
		return err
	}
	w.bytes += int64(length)
	if exists {
		w.last = last
	}
	return nil
}

func (w *renderDocumentWriter) appendTextFragment(fragment rendercontent.TextFragment) error {
	length, err := fragment.Bytes()
	if err != nil {
		return err
	}
	if int64(length) > math.MaxInt64-w.bytes {
		return fmt.Errorf("render document exceeds the platform limit")
	}
	last, exists, err := fragment.LastByte()
	if err != nil {
		return err
	}
	if err := w.builder.AppendTextFragment(fragment); err != nil {
		return err
	}
	w.bytes += int64(length)
	if exists {
		w.last = last
	}
	return nil
}

func (w *renderDocumentWriter) writeUnknownFragment(fragment templating.TextFragment) error {
	literal := &renderLiteralWriter{target: w}
	reported, fragmentErr := fragment.WriteTo(literal)
	if reported < 0 || reported != literal.written {
		return fmt.Errorf("render text fragment reported %d for %d bytes", reported, literal.written)
	}
	if literal.err != nil {
		return literal.err
	}
	return fragmentErr
}

func (w *renderDocumentWriter) ensureTrailingNewline() error {
	if w.bytes > 0 && w.last == '\n' {
		return nil
	}
	_, err := w.Write([]byte{'\n'})
	return err
}

type renderLiteralWriter struct {
	target  *renderDocumentWriter
	written int64
	err     error
}

func (w *renderLiteralWriter) Write(value []byte) (int, error) {
	if w.err != nil {
		return 0, w.err
	}
	written, err := w.target.Write(value)
	if written < 0 || int64(written) > math.MaxInt64-w.written {
		err = fmt.Errorf("render text fragment writer returned invalid count %d", written)
		written = 0
	} else {
		w.written += int64(written)
	}
	if err == nil && written != len(value) {
		err = io.ErrShortWrite
	}
	if err != nil {
		w.err = err
	}
	return written, err
}

func nilTextFragment(fragment templating.TextFragment) bool {
	if fragment == nil {
		return true
	}
	value := reflect.ValueOf(fragment)
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return value.IsNil()
	default:
		return false
	}
}
