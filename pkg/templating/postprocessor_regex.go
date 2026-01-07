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

package templating

import (
	"bufio"
	"fmt"
	"regexp"
	"strings"
)

// RegexReplaceProcessor applies regex-based find/replace to template output.
//
// The processor operates line-by-line, applying the regex pattern to each line
// independently. This enables efficient processing of large outputs and supports
// line-anchored patterns like ^[ ]+ for indentation normalization.
//
// Example usage for indentation normalization:
//
//	processor, err := NewRegexReplaceProcessor("^[ ]+", "  ")
//	normalized, err := processor.Process(haproxyConfig)
//
// This replaces any leading spaces with exactly 2 spaces per line.
type RegexReplaceProcessor struct {
	pattern *regexp.Regexp
	replace string
}

// NewRegexReplaceProcessor creates a new regex replace processor.
//
// Parameters:
//   - pattern: Regular expression pattern to match (e.g., "^[ ]+" for leading spaces)
//   - replace: Replacement string (e.g., "  " for 2-space indentation)
//
// Returns an error if the regex pattern is invalid.
func NewRegexReplaceProcessor(pattern, replace string) (*RegexReplaceProcessor, error) {
	re, err := regexp.Compile(pattern)
	if err != nil {
		return nil, fmt.Errorf("invalid regex pattern %q: %w", pattern, err)
	}

	return &RegexReplaceProcessor{
		pattern: re,
		replace: replace,
	}, nil
}

// Process applies the regex replacement to each line of the input.
//
// The processor streams through input line-by-line using bufio.Scanner,
// avoiding intermediate allocations from strings.Split/Join. This reduces
// peak memory usage from ~2x input size to ~1x input size for large configs.
//
// This line-by-line approach enables:
//   - Efficient processing of large files
//   - Line-anchored patterns (^ and $)
//   - Predictable behavior for indentation normalization
func (p *RegexReplaceProcessor) Process(input string) (string, error) {
	if input == "" {
		return input, nil
	}

	var builder strings.Builder
	builder.Grow(len(input)) // Pre-allocate to avoid reallocations

	scanner := bufio.NewScanner(strings.NewReader(input))
	first := true
	for scanner.Scan() {
		if !first {
			builder.WriteByte('\n')
		}
		first = false
		line := scanner.Text()
		builder.WriteString(p.pattern.ReplaceAllString(line, p.replace))
	}
	if err := scanner.Err(); err != nil {
		return "", fmt.Errorf("scanning input: %w", err)
	}

	// Preserve trailing newline if input had one
	if input[len(input)-1] == '\n' {
		builder.WriteByte('\n')
	}

	return builder.String(), nil
}
