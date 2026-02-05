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

import "strings"

// IndentNormalizerProcessor replaces any leading spaces on each line with exactly
// two spaces. It is functionally equivalent to RegexReplaceProcessor with pattern
// "^[ ]+" and replacement "  ", but avoids regex overhead entirely by using
// byte-level scanning.
//
// Lines with no leading spaces are written through unchanged (zero allocation).
// Lines with exactly two leading spaces are also written through unchanged.
// Only lines with >0 leading spaces that differ from "  " require a new string.
type IndentNormalizerProcessor struct {
	indent string // replacement indent (e.g. "  ")
}

// Process applies indentation normalization to the input.
func (p *IndentNormalizerProcessor) Process(input string) (string, error) {
	if input == "" {
		return input, nil
	}

	var builder strings.Builder
	builder.Grow(len(input))

	i := 0
	for i < len(input) {
		// Find end of current line
		lineEnd := strings.IndexByte(input[i:], '\n')
		var line string
		if lineEnd == -1 {
			line = input[i:]
			i = len(input)
		} else {
			line = input[i : i+lineEnd]
			i += lineEnd + 1
		}

		// Count leading spaces
		spaces := 0
		for spaces < len(line) && line[spaces] == ' ' {
			spaces++
		}

		if spaces == 0 {
			// No leading spaces — write line as-is
			builder.WriteString(line)
		} else {
			// Replace leading spaces with normalized indent
			builder.WriteString(p.indent)
			builder.WriteString(line[spaces:])
		}

		// Write the newline that was consumed (if any)
		if lineEnd != -1 {
			builder.WriteByte('\n')
		}
	}

	return builder.String(), nil
}
