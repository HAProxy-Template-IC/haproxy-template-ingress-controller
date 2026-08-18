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
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strings"
	"sync"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/renderplan"
	"gitlab.com/haproxy-haptic/haptic/pkg/templating"
)

// sectionNamePattern is the character set a section name may use. It excludes
// whitespace and '@' so a name can never close or forge a token.
var sectionNamePattern = regexp.MustCompile(`^[A-Za-z0-9_.:-]+$`)

// PlanRegistry collects the structure templates declare about the config they
// emit and assembles the final config from the placeholder tokens they got
// back. One per render; every method is safe for the sharded render's
// goroutines.
//
// Templates reach it as the `planRegistry` global (templating.PlanRegistrar).
type PlanRegistry struct {
	mu sync.Mutex

	// nonce makes the tokens unforgeable and unique per render: tenant values
	// that pass ConfigInjectionKind cannot guess it, and a leftover token from
	// another render fails assembly instead of being spliced.
	nonce string

	// paths turns a map/cert/crt-list name into the base-relative path the
	// config references it by; nil (tests) keeps names as they are.
	paths *templating.PathResolver

	sections  map[sectionKey]string
	backends  map[string]renderplan.Backend
	mapsMeta  map[string]bool
	assembled []renderplan.Section
}

type sectionKey struct {
	Kind string
	Name string
}

var _ templating.PlanRegistrar = (*PlanRegistry)(nil)

// NewPlanRegistry creates a registry with a fresh random nonce. paths may be
// nil, in which case file names are used unresolved.
func NewPlanRegistry(paths *templating.PathResolver) *PlanRegistry {
	var raw [8]byte
	// crypto/rand.Read fills the buffer or crashes the process; it cannot
	// return a partial read (see crypto/rand docs, Go 1.24+).
	_, _ = rand.Read(raw[:])
	return &PlanRegistry{
		nonce:    hex.EncodeToString(raw[:]),
		paths:    paths,
		sections: make(map[sectionKey]string),
		backends: make(map[string]renderplan.Backend),
		mapsMeta: make(map[string]bool),
	}
}

// Section registers section text and returns the token line that stands in for
// it until assembly. Re-registering identical text is a no-op — sharded
// rendering legitimately emits the same profile from several goroutines —
// while different text under the same name is an error.
func (r *PlanRegistry) Section(kind, name, text string) (string, error) {
	if kind != renderplan.SectionKindProfile && kind != renderplan.SectionKindBackend {
		return "", fmt.Errorf("planRegistry.Section: kind must be %q or %q, got %q",
			renderplan.SectionKindProfile, renderplan.SectionKindBackend, kind)
	}
	if !sectionNamePattern.MatchString(name) {
		return "", fmt.Errorf("planRegistry.Section: %s name %q must match %s", kind, name, sectionNamePattern)
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	return r.registerSection(kind, name, text)
}

// registerSection stores the text under (kind, name). Caller holds the lock.
func (r *PlanRegistry) registerSection(kind, name, text string) (string, error) {
	key := sectionKey{Kind: kind, Name: name}
	if existing, ok := r.sections[key]; ok && existing != text {
		return "", fmt.Errorf("planRegistry: %s %q registered twice with different text (%d and %d bytes)",
			kind, name, len(existing), len(text))
	}
	r.sections[key] = text
	return r.sectionToken(kind, name), nil
}

// Backend records a backend as data and registers the section text emitted
// from it, so a consumer never has to read the text back to know what changed.
func (r *PlanRegistry) Backend(record map[string]any, text string) (string, error) {
	backend, err := backendFromRecord(record)
	if err != nil {
		return "", err
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if existing, ok := r.backends[backend.Name]; ok && existing.RecordDigest != backend.RecordDigest {
		return "", fmt.Errorf("planRegistry.Backend: backend %q declared twice with different values", backend.Name)
	}
	token, err := r.registerSection(renderplan.SectionKindBackend, backend.Name, text)
	if err != nil {
		return "", err
	}
	r.backends[backend.Name] = backend
	return token, nil
}

// ProfileGroup returns the token line the assembler replaces with every
// registered profile section, sorted by name.
func (r *PlanRegistry) ProfileGroup() string {
	return "# @haptic:" + r.nonce + ":group:profiles@\n"
}

// MapMeta declares whether entry order matters for a map file. Maps are
// ordered unless a template says otherwise, so a third-party map file gets the
// safe treatment by default.
func (r *PlanRegistry) MapMeta(path string, ordered bool) error {
	if path == "" {
		return fmt.Errorf("planRegistry.MapMeta: path must not be empty")
	}

	resolved, err := r.MapPath(path)
	if err != nil {
		return fmt.Errorf("planRegistry.MapMeta: %w", err)
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if existing, ok := r.mapsMeta[resolved]; ok && existing != ordered {
		return fmt.Errorf("planRegistry.MapMeta: map %q declared both ordered and unordered", resolved)
	}
	r.mapsMeta[resolved] = ordered
	return nil
}

// MapPath is the base-relative path a map file is referenced by in the config
// (`maps/host.map`), which is also its runtime name.
func (r *PlanRegistry) MapPath(name string) (string, error) {
	return r.filePath(name, "map")
}

// filePath resolves a static name or an already-resolved registry path to the
// same base-relative string GetPath hands templates (cert names sanitised).
// It is idempotent: resolving a resolved path returns it unchanged.
func (r *PlanRegistry) filePath(name, kind string) (string, error) {
	if r.paths == nil {
		return name, nil
	}
	dir, err := r.paths.GetPath("", kind)
	if err != nil {
		return "", fmt.Errorf("resolve %s %q: %w", kind, name, err)
	}
	resolved, err := r.paths.GetPath(strings.TrimPrefix(name, dir.(string)+"/"), kind)
	if err != nil {
		return "", fmt.Errorf("resolve %s %q: %w", kind, name, err)
	}
	return resolved.(string), nil
}

// Plan bundles the assembled sections with the recorded backends, the profiles,
// the entries of every map file and the file set, and computes the plan ID.
// Call it after Assemble; without it the plan carries no sections. Every path
// in the plan is the base-relative string the config references the file by.
func (r *PlanRegistry) Plan(config string, aux *dataplane.AuxiliaryFiles) (*renderplan.Plan, error) {
	files, mapContents, err := r.planFiles(config, aux)
	if err != nil {
		return nil, err
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	plan := &renderplan.Plan{
		SchemaVersion: renderplan.SchemaVersion,
		Sections:      slices.Clone(r.assembled),
		Backends:      maps.Clone(r.backends),
		Profiles:      r.profiles(),
		Maps:          r.mapFiles(mapContents),
		Files:         sortedFiles(files),
	}
	plan.ComputeID()
	return plan, nil
}

// profiles derives the profile records from the assembled profile sections.
// Caller holds the lock.
func (r *PlanRegistry) profiles() map[string]renderplan.Profile {
	profiles := make(map[string]renderplan.Profile)
	for _, section := range r.assembled {
		if section.Kind != renderplan.SectionKindProfile {
			continue
		}
		profiles[section.Name] = renderplan.Profile{Name: section.Name, BodyDigest: section.TextDigest}
	}
	return profiles
}

// mapFiles derives the entry list of every map file from its rendered content.
// Caller holds the lock.
func (r *PlanRegistry) mapFiles(contents map[string]string) map[string]renderplan.Map {
	files := make(map[string]renderplan.Map, len(contents))
	for path, content := range contents {
		ordered, declared := r.mapsMeta[path]
		files[path] = renderplan.Map{
			Path:    path,
			Ordered: !declared || ordered,
			Entries: renderplan.ParseMapEntries(content),
		}
	}
	return files
}

func sortedFiles(files []renderplan.File) []renderplan.File {
	sorted := slices.Clone(files)
	slices.SortFunc(sorted, func(a, b renderplan.File) int { return strings.Compare(a.Path, b.Path) })
	return sorted
}

func (r *PlanRegistry) sectionToken(kind, name string) string {
	return "# @haptic:" + r.nonce + ":section:" + kind + ":" + name + "@\n"
}

// marker is the nonce-carrying prefix every token of this render shares.
func (r *PlanRegistry) marker() string {
	return "@haptic:" + r.nonce
}
