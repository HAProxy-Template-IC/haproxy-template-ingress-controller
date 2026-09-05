// Copyright 2026 Philipp Hossner
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
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/renderplan"
)

// PreparedPlanProfile is a canonical profile declaration safe to cache.
type PreparedPlanProfile struct {
	Name   string   `json:"name"`
	Body   []string `json:"body,omitempty"`
	Text   string   `json:"text"`
	Digest string   `json:"digest"`
}

// PreparedPlanBackend is a canonical backend declaration safe to cache.
type PreparedPlanBackend struct {
	Backend  renderplan.Backend `json:"backend"`
	Body     []string           `json:"body,omitempty"`
	Comments []string           `json:"comments,omitempty"`
	Text     string             `json:"text"`
	Digest   string             `json:"digest"`
}

// PreparePlanProfile validates and detaches a template profile record.
func PreparePlanProfile(record map[string]any) (PreparedPlanProfile, error) {
	dec := newRecordDecoder("planRegistry.Profile", record, profileRecordKeys)
	body := profileBody(
		dec.str("mode"),
		dec.str("balance"),
		dec.str("hashType"),
		dec.keywords("defaultServer"),
		dec.strSlice("profile"),
	)
	if dec.err != nil {
		return PreparedPlanProfile{}, dec.err
	}
	name := profileNamePrefix + renderplan.DigestString(strings.Join(body, "\n"))[:profileNameHexSize]
	prepared := PreparedPlanProfile{Name: name, Body: normalizeStrings(body), Text: profileText(name, body)}
	prepared.Digest = preparedPlanDigest(prepared)
	return prepared, nil
}

// PreparePlanBackend validates and detaches a template backend declaration.
func PreparePlanBackend(record map[string]any, text string) (PreparedPlanBackend, error) {
	dec := newRecordDecoder("planRegistry.Backend", record, backendRecordKeys)
	backend := renderplan.Backend{
		Name:          dec.str("name"),
		Profile:       dec.str("profile"),
		Mode:          dec.str("mode"),
		GUID:          dec.str("guid"),
		Balance:       dec.str("balance"),
		HashType:      dec.str("hashType"),
		Shape:         dec.strOr("shape", renderplan.ShapeStructural),
		ShapeReason:   dec.str("shapeReason"),
		Servers:       dec.servers("servers"),
		DefaultServer: dec.keywords("defaultServer"),
	}
	body := normalizeStrings(dec.strSlice("body"))
	comments := normalizeStrings(dec.strSlice("comments"))
	if dec.err != nil {
		return PreparedPlanBackend{}, dec.err
	}
	if backend.Shape == renderplan.ShapeDynamic && backend.Mode == "" {
		backend.Mode = "http"
	}
	backend = clonePreparedBackendRecord(&backend)
	if err := validateBackend(&backend); err != nil {
		return PreparedPlanBackend{}, err
	}
	backend.BodyDigest = renderplan.DigestString(strings.Join(body, "\n"))
	backend.CommentsDigest = renderplan.DigestString(strings.Join(comments, "\n"))
	backend.Body = normalizeStrings(body)
	backend.Comments = normalizeStrings(comments)
	backend.ContentKnown = true
	backend.RecordDigest = recordDigest(&backend)
	prepared := PreparedPlanBackend{Backend: backend, Body: body, Comments: comments, Text: text}
	prepared.Digest = preparedPlanDigest(prepared)
	return prepared, nil
}

// Clone returns an independently owned declaration.
func (p PreparedPlanProfile) Clone() PreparedPlanProfile {
	p.Body = normalizeStrings(p.Body)
	return p
}

// Clone returns an independently owned declaration.
func (p *PreparedPlanBackend) Clone() PreparedPlanBackend {
	if p == nil {
		return PreparedPlanBackend{}
	}
	cloned := *p
	cloned.Backend = clonePreparedBackendRecord(&p.Backend)
	cloned.Body = normalizeStrings(p.Body)
	cloned.Comments = normalizeStrings(p.Comments)
	return cloned
}

// Validate rejects noncanonical or corrupted declaration data.
func (p PreparedPlanProfile) Validate() error {
	canonical := p.Clone()
	if !sectionNamePattern.MatchString(canonical.Name) {
		return fmt.Errorf("prepared plan profile name %q must match %s", canonical.Name, sectionNamePattern)
	}
	for _, line := range canonical.Body {
		if line != strings.TrimSpace(line) || line == "" || strings.HasPrefix(line, "#") {
			return fmt.Errorf("prepared plan profile %q has a noncanonical body", canonical.Name)
		}
	}
	wantName := profileNamePrefix + renderplan.DigestString(strings.Join(canonical.Body, "\n"))[:profileNameHexSize]
	if canonical.Name != wantName || canonical.Text != profileText(wantName, canonical.Body) {
		return fmt.Errorf("prepared plan profile %q does not match its body", canonical.Name)
	}
	wantDigest := canonical.Digest
	canonical.Digest = ""
	if wantDigest == "" || wantDigest != preparedPlanDigest(canonical) {
		return fmt.Errorf("prepared plan profile %q has an invalid digest", canonical.Name)
	}
	if !reflect.DeepEqual(p, p.Clone()) {
		return fmt.Errorf("prepared plan profile %q is not canonical", canonical.Name)
	}
	return nil
}

// Validate rejects noncanonical or corrupted declaration data.
func (p *PreparedPlanBackend) Validate() error {
	if p == nil {
		return errors.New("prepared plan backend is nil")
	}
	canonical := p.Clone()
	if err := validateBackend(&canonical.Backend); err != nil {
		return err
	}
	if canonical.Backend.TextDigest != "" {
		return fmt.Errorf("prepared plan backend %q already has an assembled text digest", canonical.Backend.Name)
	}
	if err := validatePreparedKeywords(canonical.Backend.DefaultServer); err != nil {
		return fmt.Errorf("prepared plan backend %q default-server: %w", canonical.Backend.Name, err)
	}
	for _, server := range canonical.Backend.Servers {
		if err := validatePreparedKeywords(server.Extra); err != nil {
			return fmt.Errorf("prepared plan backend %q server %q: %w", canonical.Backend.Name, server.Name, err)
		}
	}
	wantBodyDigest := renderplan.DigestString(strings.Join(canonical.Body, "\n"))
	wantCommentsDigest := renderplan.DigestString(strings.Join(canonical.Comments, "\n"))
	if canonical.Backend.BodyDigest != wantBodyDigest || canonical.Backend.CommentsDigest != wantCommentsDigest {
		return fmt.Errorf("prepared plan backend %q does not match its body or comments", canonical.Backend.Name)
	}
	if canonical.Backend.RecordDigest != recordDigest(&canonical.Backend) {
		return fmt.Errorf("prepared plan backend %q has an invalid record digest", canonical.Backend.Name)
	}
	wantDigest := canonical.Digest
	canonical.Digest = ""
	if wantDigest == "" || wantDigest != preparedPlanDigest(canonical) {
		return fmt.Errorf("prepared plan backend %q has an invalid digest", canonical.Backend.Name)
	}
	if !reflect.DeepEqual(*p, p.Clone()) {
		return fmt.Errorf("prepared plan backend %q is not canonical", canonical.Backend.Name)
	}
	return nil
}

func validatePreparedKeywords(values []renderplan.KeywordArg) error {
	for _, value := range values {
		if value.Name == "" {
			return errors.New("keyword name is empty")
		}
	}
	return nil
}

func preparedPlanDigest(value any) string {
	encoded, err := json.Marshal(value)
	if err != nil {
		panic(fmt.Sprintf("planRegistry: encoding a prepared declaration failed: %v", err))
	}
	return renderplan.Digest(encoded)
}

func normalizeStrings(source []string) []string {
	if len(source) == 0 {
		return nil
	}
	return slices.Clone(source)
}

func clonePreparedBackendRecord(source *renderplan.Backend) renderplan.Backend {
	if source == nil {
		return renderplan.Backend{}
	}
	cloned := *source
	if len(source.Servers) == 0 {
		cloned.Servers = nil
	} else {
		cloned.Servers = slices.Clone(source.Servers)
		for index := range cloned.Servers {
			if source.Servers[index].Weight != nil {
				weight := *source.Servers[index].Weight
				cloned.Servers[index].Weight = &weight
			}
			cloned.Servers[index].Extra = clonePreparedKeywords(source.Servers[index].Extra)
		}
	}
	cloned.DefaultServer = clonePreparedKeywords(source.DefaultServer)
	cloned.Body = normalizeStrings(source.Body)
	cloned.Comments = normalizeStrings(source.Comments)
	return cloned
}

func clonePreparedKeywords(source []renderplan.KeywordArg) []renderplan.KeywordArg {
	if len(source) == 0 {
		return nil
	}
	cloned := slices.Clone(source)
	for index := range cloned {
		cloned[index].Args = normalizeStrings(source[index].Args)
	}
	return cloned
}
