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

//go:build js && wasm

package main

import (
	"strings"

	"gopkg.in/yaml.v3"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/names"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/renderer"
	"gitlab.com/haproxy-haptic/haptic/pkg/templating"
)

// provFrame is one breadcrumb: the template name (label) and the config-editor
// line it maps to (jump target).
type provFrame struct {
	Name string
	Line int
}

// buildProvenance turns per-template source maps into, per output line, the
// include-stack breadcrumb chain (innermost first) of config-editor lines. Keyed
// by output identity ("haproxy.cfg" for the main config, otherwise the
// map/file/cert path). base maps each aux template id to its block line so the
// "# <name>" header shown in the named tabs can jump too.
func buildProvenance(configYAML []byte, sms map[string]renderer.TemplateSourceMap, out *renderer.RenderResult) (map[string][][]provFrame, map[string]int) {
	blockBase := templateBlockBases(configYAML)
	prov := map[string][][]provFrame{}
	base := map[string]int{}

	if sm, ok := sms[names.MainTemplateName]; ok {
		prov[names.MainTemplateName] = alignedChains(sm, out.HAProxyConfig, blockBase)
	}
	if out.AuxiliaryFiles == nil {
		return prov, base
	}
	addAux := func(id, content string) {
		if sm, ok := sms[id]; ok {
			prov[id] = alignedChains(sm, content, blockBase)
		}
		if b, ok := blockBase[id]; ok {
			base[id] = b
		}
	}
	for _, m := range out.AuxiliaryFiles.MapFiles {
		addAux(m.Path, m.Content)
	}
	for _, f := range out.AuxiliaryFiles.GeneralFiles {
		addAux(f.Filename, f.Content)
	}
	for _, c := range out.AuxiliaryFiles.SSLCertificates {
		addAux(c.Path, c.Content)
	}
	return prov, base
}

// templateBlockBases parses the HAProxyTemplateConfig YAML and returns, for each
// engine template name, the config line of its `template:` block scalar. A
// template's source line N lives at config line base+N. The main config template
// is keyed under names.MainTemplateName to match the engine.
func templateBlockBases(configYAML []byte) map[string]int {
	out := map[string]int{}
	var root yaml.Node
	if err := yaml.Unmarshal(configYAML, &root); err != nil || len(root.Content) == 0 {
		return out
	}
	// Resolve the spec node across the same three shapes parseConfigSpec accepts:
	// a kubectl List (items[0].spec), a full HAProxyTemplateConfig object (spec),
	// or a bare spec with haproxyConfig at the document root (starter/crd presets).
	doc := root.Content[0]
	spec := mapChild(doc, "spec")
	if spec == nil {
		if items := mapChild(doc, "items"); items != nil && items.Kind == yaml.SequenceNode && len(items.Content) > 0 {
			spec = mapChild(items.Content[0], "spec")
		}
	}
	if spec == nil {
		spec = doc // bare spec: haproxyConfig at the document root
	}
	if hc := mapChild(spec, "haproxyConfig"); hc != nil {
		if t := mapChild(hc, "template"); t != nil {
			out[names.MainTemplateName] = t.Line
		}
	}
	for _, group := range []string{"templateSnippets", "maps", "files", "sslCertificates", "k8sResources"} {
		g := mapChild(spec, group)
		if g == nil || g.Kind != yaml.MappingNode {
			continue
		}
		for i := 0; i+1 < len(g.Content); i += 2 {
			name := g.Content[i].Value
			if t := mapChild(g.Content[i+1], "template"); t != nil {
				out[name] = t.Line
			}
		}
	}
	return out
}

// watchedResourceLines returns, for each spec.watchedResources entry, the config
// line of its name key — so clicking a resource in the resources tab can jump the
// editor to the watcher declaration that bucketed it. Resource-agnostic: it keys
// on whatever names the config declares, with no knowledge of their kinds.
func watchedResourceLines(configYAML []byte) map[string]int {
	out := map[string]int{}
	var root yaml.Node
	if err := yaml.Unmarshal(configYAML, &root); err != nil || len(root.Content) == 0 {
		return out
	}
	doc := root.Content[0]
	spec := mapChild(doc, "spec")
	if spec == nil {
		if items := mapChild(doc, "items"); items != nil && items.Kind == yaml.SequenceNode && len(items.Content) > 0 {
			spec = mapChild(items.Content[0], "spec")
		}
	}
	if spec == nil {
		spec = doc
	}
	wr := mapChild(spec, "watchedResources")
	if wr == nil || wr.Kind != yaml.MappingNode {
		return out
	}
	for i := 0; i+1 < len(wr.Content); i += 2 {
		out[wr.Content[i].Value] = wr.Content[i].Line
	}
	return out
}

// contentChainIndex maps each non-blank raw source line's trimmed text to its
// breadcrumb chain. Used to attribute a re-marshaled ("applied") YAML back to the
// template line that produced the same text, tolerating the key reordering that
// yaml.Marshal introduces (order changes, per-line content does not).
func contentChainIndex(sm renderer.TemplateSourceMap, base map[string]int) map[string][]provFrame {
	chains := rawLineChains(sm.Raw, sm.Spans, base)
	lines := strings.Split(sm.Raw, "\n")
	idx := make(map[string][]provFrame, len(lines))
	for i, l := range lines {
		t := strings.TrimSpace(l)
		if t == "" {
			continue
		}
		if _, seen := idx[t]; !seen && i < len(chains) {
			idx[t] = chains[i]
		}
	}
	return idx
}

// appliedProvKey / statusProvKey namespace the prov/base entries by tab: a target
// (e.g. "GatewayClass haptic") can appear in BOTH tabs — as an emitted resource and
// as a status patch — with different content and different source. A bare target
// key would collide in the shared prov/base maps, so each tab prefixes its own.
// The NUL is safe: target keys are "<Kind> <ns>/<name>" and never contain it.
const appliedProvKey = "applied\x00"
const statusProvKey = "status\x00"

// appliedStatusChains builds provenance for the "applied" and "status" tabs,
// which the main buildProvenance can't source-map: applied objects are
// re-marshaled (yaml.Marshal reorders keys) and status payloads are computed Go
// values with no template text at all.
//
//   - applied: per-line, by content-matching each displayed line against the
//     k8sResources template that produced the object (found by parsing each k8s
//     template's raw render for its apiVersion/kind/name).
//   - status: block-level, every line of a target's status pointing at the
//     template that called statusPatch() (from StatusPatch.SourceTemplate).
//
// Returns entries to merge into the prov/base maps, keyed by the same
// "<Kind> <ns>/<name>" target strings the applied/status tab objects use.
func appliedStatusChains(
	sms map[string]renderer.TemplateSourceMap,
	blockBase map[string]int,
	k8sNames []string,
	applied, status map[string]string,
	patches []templating.StatusPatch,
) (map[string][][]provFrame, map[string]int) {
	prov := map[string][][]provFrame{}
	base := map[string]int{}

	// Which k8sResources template produced each applied target.
	smByTarget := map[string]renderer.TemplateSourceMap{}
	srcByTarget := map[string]string{}
	for _, name := range k8sNames {
		sm, ok := sms[name]
		if !ok {
			continue
		}
		dec := yaml.NewDecoder(strings.NewReader(sm.Raw))
		for {
			var d map[string]any
			if dec.Decode(&d) != nil {
				break
			}
			kind, _ := d["kind"].(string)
			meta, _ := d["metadata"].(map[string]any)
			if kind == "" || meta == nil {
				continue
			}
			rn, _ := meta["name"].(string)
			rns, _ := meta["namespace"].(string)
			tk := targetKey(kind, rns, rn)
			smByTarget[tk] = sm
			srcByTarget[tk] = name
		}
	}
	for tk, text := range applied {
		sm, ok := smByTarget[tk]
		if !ok {
			continue
		}
		idx := contentChainIndex(sm, blockBase)
		lines := strings.Split(text, "\n")
		chains := make([][]provFrame, len(lines))
		for i, l := range lines {
			chains[i] = idx[strings.TrimSpace(l)]
		}
		prov[appliedProvKey+tk] = chains
		if b, ok := blockBase[srcByTarget[tk]]; ok {
			base[appliedProvKey+tk] = b
		}
	}

	// Status: attribute every line of a target's status to the exact statusPatch()
	// call that registered it (the template block line plus the recorded call line).
	type statusSource struct {
		tmpl string
		line int
	}
	statusSrc := map[string]statusSource{}
	for _, p := range patches {
		statusSrc[targetKey(p.Kind, p.Namespace, p.Name)] = statusSource{p.SourceTemplate, p.SourceLine}
	}
	for tk, text := range status {
		s := statusSrc[tk]
		if s.tmpl == "" {
			continue
		}
		b, ok := blockBase[s.tmpl]
		if !ok {
			continue
		}
		line := b + s.line // exact statusPatch() call line in the config editor
		base[statusProvKey+tk] = line
		lines := strings.Split(text, "\n")
		chains := make([][]provFrame, len(lines))
		for i, l := range lines {
			if strings.TrimSpace(l) == "" {
				continue
			}
			chains[i] = []provFrame{{Name: s.tmpl, Line: line}}
		}
		prov[statusProvKey+tk] = chains
	}
	return prov, base
}

// targetKey formats a status/resource target ("<Kind> <ns>/<name>"), the display
// key the status/applied tabs and their provenance entries share. Lives here (an
// untagged file) rather than in the wasm-only main.go so the provenance code that
// uses it stays buildable on native `go build ./...` / `go vet ./...` in CI.
func targetKey(kind, ns, name string) string {
	if ns != "" {
		return kind + " " + ns + "/" + name
	}
	return kind + " " + name
}

// mapChild returns the value node for key in a mapping node, or nil.
func mapChild(n *yaml.Node, key string) *yaml.Node {
	if n == nil || n.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		if n.Content[i].Value == key {
			return n.Content[i+1]
		}
	}
	return nil
}

// framesToChain resolves a span's include stack (innermost first) to config-line
// breadcrumbs, keeping only frames whose template block is known (dropping
// internal/non-template frames). For a text span the innermost line is advanced
// by sub (the line offset within the span).
func framesToChain(frames []templating.SourceFrame, isText bool, sub int, base map[string]int) []provFrame {
	chain := make([]provFrame, 0, len(frames))
	for idx, f := range frames {
		ln := f.Line
		if isText && idx == 0 {
			ln += sub
		}
		if b, ok := base[f.Path]; ok && ln > 0 {
			chain = append(chain, provFrame{Name: f.Path, Line: b + ln})
		}
	}
	return chain
}

// rawLineChains attributes each line of the raw (pre-post-processing) render to
// its breadcrumb chain: the first span contributing non-whitespace content to the
// line. A whitespace-only span is kept only provisionally, so a line whose real
// content follows leading literal indentation — e.g. `    {{ render "x" }}`, where
// the 4-space indent is a literal text span and the rendered value follows on the
// same output line — resolves into the value's snippet, not the indent's line.
func rawLineChains(raw string, spans []templating.SourceSpan, base map[string]int) [][]provFrame {
	n := strings.Count(raw, "\n") + 1
	out := make([][]provFrame, n)
	locked := make([]bool, n) // true once a non-whitespace span fixed the line
	line, pos := 0, 0
	for _, sp := range spans {
		end := pos + sp.Length
		if end > len(raw) {
			end = len(raw)
		}
		sub := 0
		for i := pos; i < end; i++ {
			c := raw[i]
			if line < n && !locked[line] {
				if c != '\n' && c != ' ' && c != '\t' && c != '\r' {
					out[line] = framesToChain(sp.Frames, sp.IsText, sub, base)
					locked[line] = true
				} else if out[line] == nil {
					out[line] = framesToChain(sp.Frames, sp.IsText, sub, base)
				}
			}
			if c == '\n' {
				line++
				if sp.IsText {
					sub++
				}
			}
		}
		pos = end
	}
	return out
}

// alignedChains maps each line of the displayed (post-processed) output to its
// breadcrumb chain, aligning raw→final by walking non-blank lines in lockstep
// (post-processors are whitespace-only).
func alignedChains(sm renderer.TemplateSourceMap, final string, base map[string]int) [][]provFrame {
	rawChains := rawLineChains(sm.Raw, sm.Spans, base)
	rawLines := strings.Split(sm.Raw, "\n")
	finalLines := strings.Split(final, "\n")
	out := make([][]provFrame, len(finalLines))
	rj := 0
	for fi, fl := range finalLines {
		if strings.TrimSpace(fl) == "" {
			continue
		}
		for rj < len(rawLines) && strings.TrimSpace(rawLines[rj]) == "" {
			rj++
		}
		if rj >= len(rawLines) {
			continue
		}
		if rj < len(rawChains) {
			out[fi] = rawChains[rj]
		}
		rj++
	}
	return out
}

// chainsToJS converts the per-line breadcrumb chains into a js.ValueOf-compatible
// value: object of arrays (per line) of {n: name, l: configLine} objects.
func chainsToJS(m map[string][][]provFrame) any {
	out := make(map[string]any, len(m))
	for k, lines := range m {
		arr := make([]any, len(lines))
		for i, chain := range lines {
			c := make([]any, len(chain))
			for j, f := range chain {
				c[j] = map[string]any{"n": f.Name, "l": f.Line}
			}
			arr[i] = c
		}
		out[k] = arr
	}
	return out
}

// intMapToJSFlat converts name -> config line into a js.ValueOf-compatible object.
func intMapToJSFlat(m map[string]int) any {
	out := make(map[string]any, len(m))
	for k, n := range m {
		out[k] = n
	}
	return out
}
