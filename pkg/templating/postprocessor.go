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
	"context"
	"errors"
	"fmt"
)

// PostProcessor processes rendered template output before it is returned.
// Post-processors enable generic transformations like normalization, formatting,
// or cleanup that apply to the final rendered content.
//
// Post-processors are applied in sequence after template rendering completes.
// Each processor receives the output of the previous processor (or the
// initial rendered template for the first processor).
type PostProcessor interface {
	// Process applies transformation to the input string.
	// Returns the transformed output or an error if processing fails.
	Process(input string) (string, error)
}

type contextPostProcessor interface {
	processContext(ctx context.Context, templateName, input string) (string, error)
}

type contextBatchPostProcessor interface {
	processBatchContext(ctx context.Context, templateName string, inputs []string) ([]string, error)
}

type cacheablePostProcessor interface {
	postProcessCacheable() bool
}

type totalPostProcessor interface {
	postProcessTotal() bool
}

func postProcessorChainCacheable(processors []PostProcessor) bool {
	for _, processor := range processors {
		cacheable, ok := processor.(cacheablePostProcessor)
		if !ok || !cacheable.postProcessCacheable() {
			return false
		}
	}
	return len(processors) > 0
}

func postProcessorChainBatchable(processors []PostProcessor) bool {
	batchProcessors := 0
	for _, processor := range processors {
		cacheable, ok := processor.(cacheablePostProcessor)
		if !ok || !cacheable.postProcessCacheable() {
			return false
		}
		if _, ok := processor.(contextBatchPostProcessor); ok {
			batchProcessors++
			if batchProcessors > 1 {
				return false
			}
			continue
		}
		total, ok := processor.(totalPostProcessor)
		if !ok || !total.postProcessTotal() {
			return false
		}
	}
	return len(processors) > 0
}

// PostProcessorType identifies the type of post-processor.
type PostProcessorType string

const (
	// PostProcessorTypeRegexReplace applies regex-based find/replace.
	PostProcessorTypeRegexReplace PostProcessorType = "regex_replace"

	// PostProcessorTypeTemplate applies a Scriggo template transformation.
	// The template receives the rendered output as the `input` variable (string)
	// and has access to all standard Scriggo builtins.
	PostProcessorTypeTemplate PostProcessorType = "template"
)

// PostProcessorConfig defines configuration for a post-processor.
// The Type field determines which processor implementation to use,
// and Params contains type-specific configuration.
type PostProcessorConfig struct {
	// Type specifies which post-processor to use.
	Type PostProcessorType `yaml:"type" json:"type"`

	// Params contains type-specific configuration as key-value pairs.
	// For regex_replace:
	//   - pattern: Regular expression pattern to match (required)
	//   - replace: Replacement string (required)
	Params map[string]string `yaml:"params" json:"params"`
}

// NewPostProcessor creates a post-processor instance from configuration.
//
// Returns an error if:
//   - The processor type is unknown
//   - Required parameters are missing
//   - Parameters are invalid (e.g., invalid regex pattern)
func NewPostProcessor(config PostProcessorConfig) (PostProcessor, error) {
	switch config.Type {
	case PostProcessorTypeRegexReplace:
		pattern, ok := config.Params["pattern"]
		if !ok {
			return nil, errors.New("regex_replace processor requires 'pattern' parameter")
		}

		replace, ok := config.Params["replace"]
		if !ok {
			return nil, errors.New("regex_replace processor requires 'replace' parameter")
		}

		return NewRegexReplaceProcessor(pattern, replace)

	default:
		return nil, fmt.Errorf("unknown post-processor type: %s", config.Type)
	}
}
