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
	"maps"
	"reflect"
	"strings"
	"sync"

	"gitlab.com/haproxy-haptic/scriggo"
	"gitlab.com/haproxy-haptic/scriggo/builtin"
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
	compiled  *scriggo.Template
	cacheable bool
}

const postProcessorRegexpCacheCapacity = 64

type postProcessorRegexpCache struct {
	mu      sync.Mutex
	values  map[string]builtin.Regexp
	order   []string
	replace int
}

func newPostProcessorRegexpCache() *postProcessorRegexpCache {
	return &postProcessorRegexpCache{
		values: make(map[string]builtin.Regexp, postProcessorRegexpCacheCapacity),
		order:  make([]string, 0, postProcessorRegexpCacheCapacity),
	}
}

func (c *postProcessorRegexpCache) compile(expression string) builtin.Regexp {
	c.mu.Lock()
	compiled, exists := c.values[expression]
	c.mu.Unlock()
	if exists {
		return compiled
	}

	compiled = builtin.RegExp(expression)
	c.mu.Lock()
	if existing, exists := c.values[expression]; exists {
		c.mu.Unlock()
		return existing
	}
	if len(c.order) < postProcessorRegexpCacheCapacity {
		c.order = append(c.order, expression)
	} else {
		delete(c.values, c.order[c.replace])
		c.order[c.replace] = expression
		c.replace = (c.replace + 1) % postProcessorRegexpCacheCapacity
	}
	c.values[expression] = compiled
	c.mu.Unlock()
	return compiled
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
		return nil, errors.New("template post-processor requires non-empty 'source' parameter")
	}

	// Create a copy of globals and add the `input` runtime variable.
	// The nil pointer pattern tells Scriggo the TYPE at compile time;
	// the actual VALUE is provided at runtime via template.Run(vars).
	ppGlobals := make(native.Declarations, len(globals)+1)
	maps.Copy(ppGlobals, globals)
	ppGlobals[declInput] = (*string)(nil)
	var trustedRegexp any
	if regexpDeclaration, exists := ppGlobals[declRegexp]; exists &&
		samePostProcessorDeclaration(regexpDeclaration, builtin.RegExp) {
		regexpCache := newPostProcessorRegexpCache()
		trustedRegexp = regexpCache.compile
		ppGlobals[declRegexp] = native.Synchronous(trustedRegexp, memberReplaceAll)
	}

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
		compiled:  compiled,
		cacheable: templatePostProcessorCacheable(compiled, ppGlobals, trustedRegexp),
	}, nil
}

func (p *TemplatePostProcessor) postProcessCacheable() bool {
	return p != nil && p.cacheable
}

func templatePostProcessorCacheable(
	compiled *scriggo.Template,
	declarations native.Declarations,
	trustedRegexp any,
) bool {
	if compiled == nil || !compiled.BatchSafe() || compiled.DeterministicSafe() != nil {
		return false
	}
	used := compiled.UsedNativeDeclarations()
	for index := range used {
		if !cacheableTemplatePostProcessorDeclaration(&used[index], trustedRegexp) {
			return false
		}
	}
	if !cacheableTemplatePostProcessorCallables(compiled, trustedRegexp) {
		return false
	}
	return samePostProcessorDeclaration(declarations[declInput], (*string)(nil))
}

func cacheableTemplatePostProcessorDeclaration(
	used *scriggo.UsedNativeDeclaration,
	trustedRegexp any,
) bool {
	if used.Package != "" && used.Package != scriggoMainPackage {
		return false
	}
	switch used.Name {
	case declInput:
		return used.Kind == scriggo.NativeDeclarationVariable &&
			samePostProcessorDeclaration(used.Declaration, (*string)(nil))
	case declRegexp:
		return used.Kind == scriggo.NativeDeclarationFunction && used.Synchronous &&
			samePostProcessorDeclaration(used.Declaration, trustedRegexp)
	default:
		return false
	}
}

func cacheableTemplatePostProcessorCallables(compiled *scriggo.Template, trustedRegexp any) bool {
	regexpReceiver := reflect.TypeOf(builtin.Regexp{})
	callables := compiled.UsedNativeCallables()
	for index := range callables {
		callable := &callables[index]
		if callable.Package != scriggoMainPackage || callable.DeclarationName != declRegexp ||
			callable.Kind != scriggo.NativeCallableMethod ||
			callable.MemberPath != memberReplaceAll || callable.Name != memberReplaceAll ||
			callable.Receiver != regexpReceiver || !callable.Synchronous ||
			!samePostProcessorDeclaration(callable.Declaration, trustedRegexp) {
			return false
		}
	}
	return true
}

func samePostProcessorDeclaration(actual, trusted any) bool {
	if actual == nil || trusted == nil {
		return actual == nil && trusted == nil
	}
	actualValue := reflect.ValueOf(actual)
	trustedValue := reflect.ValueOf(trusted)
	if actualValue.Type() != trustedValue.Type() {
		return false
	}
	if actualValue.Kind() == reflect.Func {
		return actualValue.Pointer() == trustedValue.Pointer()
	}
	return actualValue.Type().Comparable() && actualValue.Interface() == trustedValue.Interface()
}

// Process applies the template transformation to the input string.
//
// The input (previously rendered template output) is passed as the `input` variable.
// The template's output becomes the new rendered content.
func (p *TemplatePostProcessor) Process(input string) (string, error) {
	return p.processContext(context.Background(), "__postprocessor__", input)
}

func (p *TemplatePostProcessor) processContext(ctx context.Context, templateName, input string) (string, error) {
	vars := map[string]any{
		declInput: input,
	}

	var output strings.Builder
	output.Grow(len(input)) // Pre-allocate to avoid reallocations

	runOpts := &scriggo.RunOptions{Context: ctx, Deterministic: true}
	if err := runScriggoTemplate(ctx, templateName, p.compiled, &output, vars, runOpts); err != nil {
		return "", fmt.Errorf("template post-processor execution failed: %w", err)
	}

	return output.String(), nil
}

func (p *TemplatePostProcessor) processBatchContext(
	ctx context.Context,
	templateName string,
	inputs []string,
) ([]string, error) {
	builders := make([]strings.Builder, len(inputs))
	runs := make([]scriggo.BatchRun, len(inputs))
	for index, input := range inputs {
		builders[index].Grow(len(input))
		runs[index] = scriggo.BatchRun{
			Out:  &builders[index],
			Vars: map[string]any{declInput: input},
			Options: &scriggo.RunOptions{
				Context:       ctx,
				Deterministic: true,
			},
		}
	}
	err := runTemplatePostProcessorBatch(ctx, templateName, p.compiled, runs)
	if err != nil {
		var batchErr *scriggo.BatchRunError
		if errors.As(err, &batchErr) {
			return nil, &PostProcessBatchError{
				Index: batchErr.Index,
				Err:   fmt.Errorf("template post-processor execution failed: %w", batchErr.Err),
			}
		}
		return nil, fmt.Errorf("template post-processor execution failed: %w", err)
	}
	outputs := make([]string, len(builders))
	for index := range builders {
		outputs[index] = builders[index].String()
	}
	return outputs, nil
}

func runTemplatePostProcessorBatch(
	ctx context.Context,
	templateName string,
	template *scriggo.Template,
	runs []scriggo.BatchRun,
) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			if cause := context.Cause(ctx); cause != nil && isScriggoCancellationPanic(recovered, ctx.Err(), cause) {
				err = &RenderTimeoutError{TemplateName: templateName, Cause: cause}
				return
			}
			panic(recovered)
		}
	}()
	err = template.RunBatch(runs)
	if cause := context.Cause(ctx); cause != nil {
		return &RenderTimeoutError{TemplateName: templateName, Cause: cause}
	}
	return err
}
