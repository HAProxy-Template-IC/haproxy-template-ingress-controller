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

package main

import (
	"cmp"
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/agent/api"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/deployplan"
)

// diffReport is one printed answer: the decision, plus what only the command
// knows — the render directory, which prefixes every path an offline render
// produced and names nothing on a pod, and whether the two sides came out the
// same, which an empty file_only decision does not say by itself.
type diffReport struct {
	decision   *deployplan.Decision
	renderRoot string
	unchanged  bool
}

// printDiff writes the decision: the verdict first, so a reader who wanted one
// word has it on line one.
func printDiff(w io.Writer, report *diffReport) error {
	if diffOutputFormat == outputJSON {
		encoded, err := json.MarshalIndent(report.decision, "", "  ")
		if err != nil {
			return fmt.Errorf("encoding the decision: %w", err)
		}
		_, err = fmt.Fprintln(w, string(encoded))
		return err
	}
	if diffOutputFormat != outputHuman {
		return fmt.Errorf("unknown output format %q; use human or json", diffOutputFormat)
	}

	fmt.Fprintln(w, string(report.decision.Verdict))
	if report.unchanged {
		fmt.Fprintln(w, "both sides declare the same plan; a deployment writes nothing")
	}
	report.printReasons(w)
	report.printOps(w, "ops", report.decision.Ops)
	if len(report.decision.InPlace) > 0 {
		report.printOps(w, "in-place ops (run even while a reload is pending)", report.decision.InPlace)
	}
	if report.decision.Chunks > 1 {
		fmt.Fprintf(w, "\nsent as %d applies\n", report.decision.Chunks)
	}
	return nil
}

func (r *diffReport) printReasons(w io.Writer) {
	if len(r.decision.Reasons) == 0 {
		return
	}
	fmt.Fprintf(w, "\nreasons (%d):\n", len(r.decision.Reasons))
	for _, reason := range r.decision.Reasons {
		fmt.Fprintln(w, "  "+r.trimRoot(reason))
	}
}

func (r *diffReport) printOps(w io.Writer, label string, ops []api.Op) {
	if len(ops) == 0 {
		fmt.Fprintf(w, "\n%s: none\n", label)
		return
	}
	fmt.Fprintf(w, "\n%s: %d — %s\n", label, len(ops), strings.Join(opKindCounts(ops), ", "))

	listed := len(ops)
	if !diffAll && listed > defaultOpsListed {
		listed = defaultOpsListed
	}
	for i := range ops[:listed] {
		fmt.Fprintln(w, "  "+r.trimRoot(describeOp(&ops[i])))
	}
	if listed < len(ops) {
		fmt.Fprintf(w, "  … %d more, pass --all to list every op\n", len(ops)-listed)
	}
}

// trimRoot drops the render's temporary directory from a path, leaving the
// name the pod knows the file by.
func (r *diffReport) trimRoot(text string) string {
	if r.renderRoot == "" {
		return text
	}
	return strings.ReplaceAll(text, r.renderRoot+"/", "")
}

// opKindCounts is each kind with how often it occurs, commonest first.
func opKindCounts(ops []api.Op) []string {
	counts := map[string]int{}
	for i := range ops {
		counts[ops[i].Kind]++
	}
	kinds := make([]string, 0, len(counts))
	for kind := range counts {
		kinds = append(kinds, kind)
	}
	slices.SortFunc(kinds, func(a, b string) int {
		return cmp.Or(cmp.Compare(counts[b], counts[a]), cmp.Compare(a, b))
	})
	for i, kind := range kinds {
		kinds[i] = kind + " " + strconv.Itoa(counts[kind])
	}
	return kinds
}

// describeOp names what one op acts on: the backend and server, the file path,
// the map key. Everything else is in --output json.
func describeOp(op *api.Op) string {
	parts := []string{op.Kind}
	switch {
	case op.Backend != "" && op.Server != "":
		parts = append(parts, op.Backend+"/"+op.Server)
	case op.Backend != "":
		parts = append(parts, op.Backend)
	}
	if op.Path != "" {
		parts = append(parts, op.Path)
	}
	if op.Key != "" {
		parts = append(parts, op.Key)
	}
	if op.Cert != "" {
		parts = append(parts, op.Cert)
	}
	if op.Address != "" {
		parts = append(parts, op.Address+":"+strconv.Itoa(op.Port))
	}
	if op.State != "" {
		parts = append(parts, op.State)
	}
	if op.Weight != nil {
		parts = append(parts, "weight "+strconv.Itoa(*op.Weight))
	}
	return strings.Join(parts, " ")
}
