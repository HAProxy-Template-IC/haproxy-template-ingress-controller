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
	"strings"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/renderplan"
)

// The rule-free parent every profile inherits from. The chart's haproxyConfig
// emits the `defaults haptic-base` section with this exact name, so a change
// here must change the chart layout too (base/library.yaml haproxyConfig).
const (
	baseProfileName    = "haptic-base"
	profileNamePrefix  = "haptic-be-"
	profileNameHexSize = 12
)

// Profile content-addresses one named `defaults haptic-be-<hash> from
// haptic-base` from the shape values a backend shares with every backend of the
// same shape, registers its section text, and returns the name. Identical
// bodies yield the same name and one section by construction, so the sharded
// render produces a stable section set no matter which goroutine emits it first.
func (r *PlanRegistry) Profile(record map[string]any) (string, error) {
	dec := newRecordDecoder("planRegistry.Profile", record, profileRecordKeys)
	body := profileBody(
		dec.str("mode"),
		dec.str("balance"),
		dec.str("hashType"),
		dec.keywords("defaultServer"),
		dec.strSlice("profile"),
	)
	if dec.err != nil {
		return "", dec.err
	}

	name := profileNamePrefix + renderplan.DigestString(strings.Join(body, "\n"))[:profileNameHexSize]
	text := profileText(name, body)

	r.mu.Lock()
	defer r.mu.Unlock()
	if _, err := r.registerSection(renderplan.SectionKindProfile, name, text); err != nil {
		return "", err
	}
	return name, nil
}

// profileBody is the canonical, deterministic list of directive lines a profile
// carries — the same list is hashed for the name and emitted as the section, so
// two backends with the same shape can never disagree on the section text.
// Comment and blank profile lines are dropped (they never change behaviour and
// would defeat dedup); the author's directive order is preserved.
func profileBody(mode, balance, hashType string, defaultServer []renderplan.KeywordArg, profile []string) []string {
	var body []string
	if mode != "" {
		body = append(body, "mode "+mode)
	}
	if balance != "" {
		body = append(body, "balance "+balance)
	}
	if hashType != "" {
		body = append(body, "hash-type "+hashType)
	}
	if line := defaultServerLine(defaultServer); line != "" {
		body = append(body, line)
	}
	for _, line := range profile {
		if trimmed := strings.TrimSpace(line); trimmed != "" && !strings.HasPrefix(trimmed, "#") {
			body = append(body, trimmed)
		}
	}
	return body
}

// defaultServerLine formats the `default-server` keywords, or "" when there are
// none. It matches the chart's own formatting so the profile line and the
// backend record's DefaultServer describe the same bytes.
func defaultServerLine(args []renderplan.KeywordArg) string {
	if len(args) == 0 {
		return ""
	}
	parts := make([]string, 0, len(args))
	for _, arg := range args {
		parts = append(parts, strings.TrimSpace(arg.Name+" "+strings.Join(arg.Args, " ")))
	}
	return "default-server " + strings.Join(parts, " ")
}

// profileText renders one named-defaults section: the header and the body lines
// indented four spaces, the layout the assembler splices at the profile group.
func profileText(name string, body []string) string {
	var b strings.Builder
	b.WriteString("defaults ")
	b.WriteString(name)
	b.WriteString(" from ")
	b.WriteString(baseProfileName)
	b.WriteByte('\n')
	for _, line := range body {
		b.WriteString("    ")
		b.WriteString(line)
		b.WriteByte('\n')
	}
	return b.String()
}
