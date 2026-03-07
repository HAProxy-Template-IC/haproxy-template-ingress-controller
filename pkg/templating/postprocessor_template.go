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
	"maps"
	"strings"

	"gitlab.com/haproxy-haptic/scriggo"
	"gitlab.com/haproxy-haptic/scriggo/native"
)

// TemplatePostProcessor applies a Scriggo template transformation to the rendered output.
//
// The template receives the rendered output as the `input` variable (string) and has
// access to all standard Scriggo builtins (regexp, replace, len, tostring, etc.).
// The template's output becomes the new rendered content.
//
// This enables arbitrary post-processing logic using familiar template syntax,
// such as counting patterns, replacing placeholders with computed values,
// or validating the rendered output.
type TemplatePostProcessor struct {
	compiled *scriggo.Template
}

// NewTemplatePostProcessor creates a new template post-processor from Scriggo source.
//
// The source is compiled during engine initialization with all standard globals plus
// an `input` variable (string) that receives the rendered output at processing time.
// Compilation errors are caught at init time (fail-fast), not at render time.
//
// Parameters:
//   - source: Scriggo template source code
//   - globals: Engine globals to make available in the post-processor template
//
// Returns an error if:
//   - The source is empty
//   - The template has syntax errors (compilation failure)
func NewTemplatePostProcessor(source string, globals native.Declarations) (*TemplatePostProcessor, error) {
	if source == "" {
		return nil, fmt.Errorf("template post-processor requires non-empty 'source' parameter")
	}

	// Create a copy of globals and add the `input` runtime variable.
	// The nil pointer pattern tells Scriggo the TYPE at compile time;
	// the actual VALUE is provided at runtime via template.Run(vars).
	ppGlobals := make(native.Declarations, len(globals)+1)
	maps.Copy(ppGlobals, globals)
	ppGlobals["input"] = (*string)(nil)

	// Compile the post-processor template using a single-file filesystem.
	fsys := &scriggoTemplateFS{
		templates: map[string]string{
			"__postprocessor__": source,
		},
	}

	opts := &scriggo.BuildOptions{
		Globals: ppGlobals,
	}

	compiled, err := scriggo.BuildTemplate(fsys, "__postprocessor__", opts)
	if err != nil {
		return nil, fmt.Errorf("template post-processor compilation failed: %w", err)
	}

	return &TemplatePostProcessor{
		compiled: compiled,
	}, nil
}

// Process applies the template transformation to the input string.
//
// The input (previously rendered template output) is passed as the `input` variable.
// The template's output becomes the new rendered content.
func (p *TemplatePostProcessor) Process(input string) (string, error) {
	vars := map[string]any{
		"input": input,
	}

	var output strings.Builder
	output.Grow(len(input)) // Pre-allocate to avoid reallocations

	err := p.compiled.Run(&output, vars, nil)
	if err != nil {
		return "", fmt.Errorf("template post-processor execution failed: %w", err)
	}

	return output.String(), nil
}
