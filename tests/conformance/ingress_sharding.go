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

// Ingress-conformance feature-file sharder. Lives in an untagged file
// (rather than alongside the rest of the conformance harness, which is
// gated on `ingress_conformance`) so the round-trip coverage tests run
// as part of the standard `make test` — a regression in the splitter
// would silently drop scenarios from CI while every shard still passed,
// and that has to fail fast on the developer's machine, not in the
// integration job 17 minutes later.

package conformance

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// shardEnv reads $SHARD_ID and $SHARD_COUNT (populated by GitLab CI's
// `parallel: N` keyword: CI_NODE_INDEX→SHARD_ID, CI_NODE_TOTAL→SHARD_COUNT).
// Returns ok=false, err=nil when neither is set (the unsharded path —
// full suite, what `make test-ingress-conformance` does locally).
// Returns a non-nil err for any present-but-malformed combination
// (one var set without the other, non-numeric, out of range). The
// caller must surface the error — a misconfigured shard is worse than
// no shard because it would silently drop scenarios while still
// reporting green.
func shardEnv() (id, total int, ok bool, err error) {
	idStr := os.Getenv("SHARD_ID")
	totalStr := os.Getenv("SHARD_COUNT")
	if idStr == "" && totalStr == "" {
		return 0, 0, false, nil
	}
	if idStr == "" {
		return 0, 0, false, fmt.Errorf("SHARD_ID must be set when SHARD_COUNT is")
	}
	if totalStr == "" {
		return 0, 0, false, fmt.Errorf("SHARD_COUNT must be set when SHARD_ID is")
	}
	id, err = strconv.Atoi(idStr)
	if err != nil {
		return 0, 0, false, fmt.Errorf("SHARD_ID %q is not an integer: %w", idStr, err)
	}
	total, err = strconv.Atoi(totalStr)
	if err != nil {
		return 0, 0, false, fmt.Errorf("SHARD_COUNT %q is not an integer: %w", totalStr, err)
	}
	if id < 1 {
		return 0, 0, false, fmt.Errorf("SHARD_ID must be >= 1 (got %d)", id)
	}
	if total < 1 {
		return 0, 0, false, fmt.Errorf("SHARD_COUNT must be >= 1 (got %d)", total)
	}
	if id > total {
		return 0, 0, false, fmt.Errorf("SHARD_ID (%d) must be <= SHARD_COUNT (%d)", id, total)
	}
	return id, total, true, nil
}

// prepareShardedFeatures reads `<srcDir>/features/*.feature` and writes a
// per-shard subset into `<destDir>/features/`. shardID is 1-based.
//
// Assignment:
//
//   - path_rules.feature is split scenario-wise: scenarios are round-robin
//     distributed by index, so shard k gets scenarios at positions
//     k-1, k-1+shardCount, k-1+2*shardCount, … (1-based shard, 0-based
//     scenario index). Every shard inherits the file's preamble — the
//     feature-level `@tags`, `Feature:` line, and the `Background:` block
//     that godog runs before each scenario.
//
//   - Other .feature files are bin-packed whole onto a single shard via
//     round-robin on alphabetical filename order. With shardCount=4 and
//     today's 4 non-path_rules files the assignment lands one per shard.
//
// Aggregating output across all shards in [1..shardCount] reproduces the
// original feature set exactly: every scenario appears exactly once.
//
// Tuned for shardCount=4 against the upstream pin at SHA
// d920ed36a0076e169a9a329a850844ab3a695ae8 — TestPrepareShardedFeaturesCoverage
// asserts the no-drop / no-duplicate invariant against synthesized input.
func prepareShardedFeatures(srcDir, destDir string, shardID, shardCount int) error {
	if shardID < 1 || shardID > shardCount {
		return fmt.Errorf("shardID %d out of range [1,%d]", shardID, shardCount)
	}
	srcFeatures := filepath.Join(srcDir, "features")
	destFeatures := filepath.Join(destDir, "features")
	if err := os.MkdirAll(destFeatures, 0o750); err != nil {
		return fmt.Errorf("mkdir %s: %w", destFeatures, err)
	}
	files, err := listFeatureFiles(srcFeatures)
	if err != nil {
		return err
	}
	nonPathIdx := 0
	for _, name := range files {
		src := filepath.Join(srcFeatures, name)
		dst := filepath.Join(destFeatures, name)
		if name == "path_rules.feature" {
			if err := writePathRulesShard(src, dst, shardID, shardCount); err != nil {
				return err
			}
			continue
		}
		owner := (nonPathIdx % shardCount) + 1
		nonPathIdx++
		if owner != shardID {
			continue
		}
		if err := copyFile(src, dst); err != nil {
			return err
		}
	}
	return nil
}

// listFeatureFiles returns the alphabetically-sorted set of `*.feature`
// files in dir. Sorted to make non-path_rules shard assignment deterministic
// across filesystems.
func listFeatureFiles(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read source features %s: %w", dir, err)
	}
	var files []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".feature") {
			files = append(files, e.Name())
		}
	}
	sort.Strings(files)
	return files, nil
}

// writePathRulesShard splits path_rules.feature scenario-wise and writes
// this shard's slice to dst. If the shard owns no scenarios (shardCount
// > scenario count), dst is not created — godog errors out on a feature
// file with zero scenarios.
func writePathRulesShard(src, dst string, shardID, shardCount int) error {
	out, err := shardPathRulesScenarios(src, shardID, shardCount)
	if err != nil {
		return fmt.Errorf("shard path_rules: %w", err)
	}
	if out == "" {
		return nil
	}
	if err := os.WriteFile(dst, []byte(out), 0o600); err != nil {
		return fmt.Errorf("write %s: %w", dst, err)
	}
	return nil
}

func copyFile(src, dst string) error {
	raw, err := os.ReadFile(src)
	if err != nil {
		return fmt.Errorf("read %s: %w", src, err)
	}
	if err := os.WriteFile(dst, raw, 0o600); err != nil {
		return fmt.Errorf("write %s: %w", dst, err)
	}
	return nil
}

// shardPathRulesScenarios reads a Gherkin .feature file and returns a
// feature-file-shaped string containing only the scenarios assigned to
// (shardID, shardCount) by round-robin on scenario index (0-based) modulo
// shardCount, +1 to match the 1-based shardID.
//
// The header (everything before the first `Scenario:` / `Scenario Outline:`
// line — feature-level tags, `Feature:`, description, `Background:` block)
// is emitted verbatim. godog reruns Background before each scenario, so
// every shard's output is a syntactically-valid standalone feature file.
//
// Returns "" when the resulting shard has no scenarios (shardCount
// exceeds the scenario count).
//
// Relies on the upstream pin's invariant that no scenario in
// path_rules.feature has preceding `@tag` lines (verified by inspection
// at the pinned SHA — see prepareShardedFeatures docstring).
func shardPathRulesScenarios(path string, shardID, shardCount int) (string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	lines := strings.Split(string(raw), "\n")

	isScenarioStart := func(line string) bool {
		trimmed := strings.TrimSpace(line)
		return strings.HasPrefix(trimmed, "Scenario:") ||
			strings.HasPrefix(trimmed, "Scenario Outline:")
	}

	headerEnd := len(lines)
	for i, line := range lines {
		if isScenarioStart(line) {
			headerEnd = i
			break
		}
	}
	header := lines[:headerEnd]

	var blocks [][]string
	var current []string
	for _, line := range lines[headerEnd:] {
		if isScenarioStart(line) && len(current) > 0 {
			blocks = append(blocks, current)
			current = nil
		}
		current = append(current, line)
	}
	if len(current) > 0 {
		blocks = append(blocks, current)
	}

	var kept [][]string
	for i, b := range blocks {
		if (i%shardCount)+1 == shardID {
			kept = append(kept, b)
		}
	}
	if len(kept) == 0 {
		return "", nil
	}

	var out strings.Builder
	out.WriteString(strings.Join(header, "\n"))
	if len(header) > 0 {
		out.WriteString("\n")
	}
	for _, b := range kept {
		out.WriteString(strings.Join(b, "\n"))
		out.WriteString("\n")
	}
	return out.String(), nil
}
