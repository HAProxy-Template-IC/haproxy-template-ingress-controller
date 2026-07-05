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

package migratecheck

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Output formats accepted by Format.
const (
	FormatText     = "text"
	FormatJSON     = "json"
	FormatMarkdown = "markdown"
)

// Format renders the report in the requested output format
// (text, json, or markdown).
func Format(report *Report, format string) (string, error) {
	switch format {
	case FormatText, "":
		return formatText(report), nil
	case FormatJSON:
		data, err := json.MarshalIndent(report, "", "  ")
		if err != nil {
			return "", fmt.Errorf("marshalling report: %w", err)
		}
		return string(data) + "\n", nil
	case FormatMarkdown:
		return formatMarkdown(report), nil
	default:
		return "", fmt.Errorf("unknown output format %q (want text, json, or markdown)", format)
	}
}

// plural renders "N noun" pluralizing the noun when N != 1: a trailing "es"
// for words ending in "s" (Ingress → Ingresses), otherwise a trailing "s".
// Keeps the operator-facing text grammatical without a dependency.
func plural(n int, noun string) string {
	if n == 1 {
		return fmt.Sprintf("1 %s", noun)
	}
	suffix := "s"
	if strings.HasSuffix(noun, "s") {
		suffix = "es"
	}
	return fmt.Sprintf("%d %s%s", n, noun, suffix)
}

// verb picks the singular or plural verb form to agree with a count.
func verb(n int, singular, plural string) string {
	if n == 1 {
		return singular
	}
	return plural
}

// statusMark returns the one-character marker for a finding status, in the
// house style of the validate CLI (✓/✗/⊘).
func statusMark(s Status) string {
	switch s {
	case StatusSupported:
		return "✓"
	case StatusDifferent:
		return "~"
	case StatusDropped:
		return "⊘"
	case StatusFails:
		return "✗"
	default:
		return "?"
	}
}

// statusHint is the one-line, plain-language meaning of each status, shown
// once in the legend so per-finding notes can stay specific.
func statusHint(s Status) string {
	switch s {
	case StatusSupported:
		return "works the same after migrating"
	case StatusDifferent:
		return "works, but behaves differently — review the note"
	case StatusDropped:
		return "silently ignored after migrating"
	case StatusFails:
		return "not supported — HAPTIC refuses a config carrying it"
	default:
		return "not in the coverage data — verify manually"
	}
}

// verdict returns the one-line verdict for the report's exit code.
func verdict(report *Report) string {
	switch report.ExitCode() {
	case ExitBlockers:
		if report.AggregateRenderError != "" && report.RenderFailures == 0 && report.Counts[StatusFails] == 0 {
			return "BLOCKERS FOUND — the Ingresses render individually but conflict when combined; see the cross-Ingress conflict below."
		}
		return fmt.Sprintf("BLOCKERS FOUND — fix the items marked %s before migrating.", statusMark(StatusFails))
	case ExitDifferences:
		return "REVIEW NEEDED — some annotations behave differently, are dropped, or are unknown."
	default:
		return "READY — every checked annotation is supported and all Ingresses render together."
	}
}

// summaryCounts renders the per-status tally as a compact human-readable
// list, omitting zero statuses.
func summaryCounts(counts map[Status]int) string {
	labels := []struct {
		status Status
		label  string
	}{
		{StatusFails, "blocking"},
		{StatusDifferent, string(StatusDifferent)},
		{StatusDropped, string(StatusDropped)},
		{StatusUnknown, string(StatusUnknown)},
		{StatusSupported, "fully supported"},
	}
	parts := make([]string, 0, len(labels))
	for _, l := range labels {
		if n := counts[l.status]; n > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", n, l.label))
		}
	}
	if len(parts) == 0 {
		return "no source-controller annotations found"
	}
	return strings.Join(parts, ", ")
}

// formatText renders the operator-facing plain-text report: verdict summary
// first, then per-source, per-Ingress findings with notes and doc links.
func formatText(report *Report) string {
	var b strings.Builder

	fmt.Fprintf(&b, "migrate-check: %s across %s",
		summaryCounts(report.Counts), plural(report.TotalIngresses, "Ingress"))
	if report.RenderFailures > 0 {
		fmt.Fprintf(&b, "; %s failed to render", plural(report.RenderFailures, "Ingress"))
	}
	b.WriteString("\n")
	fmt.Fprintf(&b, "Verdict: %s\n", verdict(report))
	if report.AggregateRenderError != "" {
		fmt.Fprintf(&b, "\n✗ Cross-Ingress conflict — the combined configuration was rejected:\n    %s\n",
			report.AggregateRenderError)
	}

	for si := range report.Sources {
		src := &report.Sources[si]
		fmt.Fprintf(&b, "\nSource: %s — %s, %s\n",
			src.Source, plural(len(src.Ingresses), "Ingress"), summaryCounts(src.Counts))
		for ii := range src.Ingresses {
			writeTextIngress(&b, &src.Ingresses[ii])
		}
	}

	writeTextUnattributed(&b, report.Unattributed)
	writeTextLegend(&b, report)

	return b.String()
}

// writeTextIngress renders one Ingress block: render verdict first (a failed
// render trumps everything), then each non-supported finding with note and
// doc link, then a one-line summary of the supported ones.
func writeTextIngress(b *strings.Builder, ing *IngressReport) {
	class := ing.Class
	if class == "" {
		class = "none"
	}
	fmt.Fprintf(b, "\n  %s/%s (class: %s)\n", ing.Namespace, ing.Name, class)

	if ing.RenderError != "" {
		fmt.Fprintf(b, "    ✗ RENDER FAILED: %s\n", ing.RenderError)
		b.WriteString("      HAPTIC could not build a configuration for this Ingress as-is.\n")
	}

	supported := 0
	for fi := range ing.Findings {
		f := &ing.Findings[fi]
		if f.Status == StatusSupported {
			supported++
			continue
		}
		fmt.Fprintf(b, "    %s %s: %s [%s]\n", statusMark(f.Status), f.Annotation, f.Value, f.Status)
		if f.Note != "" {
			fmt.Fprintf(b, "      %s\n", f.Note)
		}
		if f.Doc != "" {
			fmt.Fprintf(b, "      docs: %s\n", f.Doc)
		}
	}
	if supported > 0 {
		fmt.Fprintf(b, "    ✓ %s %s unchanged\n", plural(supported, "annotation"), verb(supported, "migrates", "migrate"))
	}
}

// writeTextUnattributed summarizes Ingresses no source matched. They only
// need detail when their render failed.
func writeTextUnattributed(b *strings.Builder, unattributed []IngressReport) {
	if len(unattributed) == 0 {
		return
	}
	fmt.Fprintf(b, "\n%s %s no annotations from a known source controller.\n",
		plural(len(unattributed), "Ingress"), verb(len(unattributed), "carries", "carry"))
	for i := range unattributed {
		ing := &unattributed[i]
		if ing.RenderError == "" {
			continue
		}
		fmt.Fprintf(b, "\n  %s/%s\n    ✗ RENDER FAILED: %s\n", ing.Namespace, ing.Name, ing.RenderError)
	}
}

// writeTextLegend appends the meaning of every marker that actually appears
// in the report, plus the exit-code contract.
func writeTextLegend(b *strings.Builder, report *Report) {
	b.WriteString("\n")
	for _, s := range []Status{StatusFails, StatusDifferent, StatusDropped, StatusUnknown, StatusSupported} {
		if report.Counts[s] > 0 {
			fmt.Fprintf(b, "%s %-9s %s\n", statusMark(s), s+":", statusHint(s))
		}
	}
	b.WriteString("Exit codes: 0 ready to migrate, 1 review differences, 2 blockers (or the check itself failed).\n")
}

// formatMarkdown renders the report as Markdown with one findings table per
// source.
func formatMarkdown(report *Report) string {
	var b strings.Builder

	b.WriteString("# HAPTIC migration check\n\n")
	fmt.Fprintf(&b, "**Verdict: %s**\n\n", verdict(report))
	fmt.Fprintf(&b, "%s across %s", summaryCounts(report.Counts), plural(report.TotalIngresses, "Ingress"))
	if report.RenderFailures > 0 {
		fmt.Fprintf(&b, "; %s failed to render", plural(report.RenderFailures, "Ingress"))
	}
	b.WriteString(".\n")
	if report.AggregateRenderError != "" {
		// Indented code block, not a ``` fence: the render error is
		// arbitrary text and a line containing ``` would break a fence.
		b.WriteString("\n**Cross-Ingress conflict** — the combined configuration was rejected:\n\n")
		for _, line := range strings.Split(report.AggregateRenderError, "\n") {
			fmt.Fprintf(&b, "    %s\n", line)
		}
	}

	for si := range report.Sources {
		src := &report.Sources[si]
		fmt.Fprintf(&b, "\n## Source: %s\n\n", src.Source)
		b.WriteString("| Ingress | Annotation | Status | Note |\n")
		b.WriteString("|---|---|---|---|\n")
		for ii := range src.Ingresses {
			ing := &src.Ingresses[ii]
			name := ing.Namespace + "/" + ing.Name
			if ing.RenderError != "" {
				fmt.Fprintf(&b, "| %s | *(whole Ingress)* | **render failed** | %s |\n",
					name, markdownEscape(ing.RenderError))
			}
			for fi := range ing.Findings {
				f := &ing.Findings[fi]
				note := markdownEscape(f.Note)
				if f.Doc != "" {
					note += " ([docs](" + f.Doc + "))"
				}
				fmt.Fprintf(&b, "| %s | `%s` | %s | %s |\n", name, f.Annotation, f.Status, note)
			}
		}
	}

	for i := range report.Unattributed {
		ing := &report.Unattributed[i]
		if ing.RenderError != "" {
			fmt.Fprintf(&b, "\n**Render failed** for `%s/%s` (no known source): %s\n",
				ing.Namespace, ing.Name, markdownEscape(ing.RenderError))
		}
	}

	return b.String()
}

// markdownEscape keeps multi-line notes/errors from breaking table rows.
func markdownEscape(s string) string {
	s = strings.ReplaceAll(s, "|", "\\|")
	return strings.ReplaceAll(s, "\n", " ")
}
