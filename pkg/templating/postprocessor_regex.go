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

func (*RegexReplaceProcessor) postProcessCacheable() bool {
	return true
}

func (*RegexReplaceProcessor) postProcessTotal() bool {
	return true
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
func (p *RegexReplaceProcessor) Process(input string) (string, error) {
	if input == "" {
		return input, nil
	}

	var builder strings.Builder
	builder.Grow(len(input))
	for start := 0; start < len(input); {
		end := len(input)
		hasNewline := false
		if offset := strings.IndexByte(input[start:], '\n'); offset >= 0 {
			end = start + offset
			hasNewline = true
		}
		lineEnd := end
		if lineEnd > start && input[lineEnd-1] == '\r' {
			lineEnd--
		}
		line := input[start:lineEnd]
		builder.WriteString(p.pattern.ReplaceAllString(line, p.replace))
		if !hasNewline {
			break
		}
		builder.WriteByte('\n')
		start = end + 1
	}

	return builder.String(), nil
}
