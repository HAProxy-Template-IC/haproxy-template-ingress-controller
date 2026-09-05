// Copyright 2026 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
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
	"slices"

	"gitlab.com/haproxy-haptic/scriggo"
)

var _ PostProcessBatcher = (*ScriggoEngine)(nil)

// PostProcessBatchError identifies the first input that failed.
type PostProcessBatchError struct {
	Index int
	Err   error
}

func (e *PostProcessBatchError) Error() string {
	return fmt.Sprintf("post-processing input %d: %v", e.Index, e.Err)
}

func (e *PostProcessBatchError) Unwrap() error {
	return e.Err
}

func (e *PostProcessBatchError) BatchIndex() int {
	return e.Index
}

// PostProcessBatch returns one post-processed output per input in the same order.
func (e *ScriggoEngine) PostProcessBatch(
	ctx context.Context,
	templateName string,
	inputs []string,
) ([]string, error) {
	if len(inputs) == 0 {
		return []string{}, nil
	}
	identity := e.postProcessCacheIdentities[templateName]
	transaction := e.postProcessTransaction(ctx)
	if identity != nil && transaction != nil {
		return transaction.processBatch(ctx, identity, inputs, func(ctx context.Context, misses []string) ([]string, error) {
			return e.postProcessBatchUncached(ctx, templateName, misses)
		})
	}
	return e.postProcessBatchUncached(ctx, templateName, inputs)
}

func (e *ScriggoEngine) postProcessBatchUncached(
	ctx context.Context,
	templateName string,
	inputs []string,
) ([]string, error) {
	processors := e.postProcessors[templateName]
	if !postProcessorChainBatchable(processors) {
		return e.postProcessBatchSequential(ctx, templateName, inputs)
	}

	results := slices.Clone(inputs)
	for _, processor := range processors {
		if cause := context.Cause(ctx); cause != nil {
			return nil, &RenderTimeoutError{TemplateName: templateName, Cause: cause}
		}
		if batchProcessor, ok := processor.(contextBatchPostProcessor); ok {
			processed, fallback, err := runBatchPostProcessor(ctx, templateName, batchProcessor, results)
			if fallback {
				return e.postProcessBatchSequential(ctx, templateName, inputs)
			}
			if err != nil {
				return nil, err
			}
			results = processed
			continue
		}
		for index, input := range results {
			processed, err := processPostProcessor(ctx, templateName, processor, input)
			if err != nil {
				return nil, &PostProcessBatchError{Index: index, Err: err}
			}
			results[index] = processed
		}
	}
	if cause := context.Cause(ctx); cause != nil {
		return nil, &RenderTimeoutError{TemplateName: templateName, Cause: cause}
	}
	return results, nil
}

func runBatchPostProcessor(
	ctx context.Context,
	templateName string,
	processor contextBatchPostProcessor,
	results []string,
) (processed []string, fallback bool, err error) {
	processed, err = processor.processBatchContext(ctx, templateName, results)
	if err != nil {
		if errors.Is(err, scriggo.ErrBatchDetachedWork) {
			return nil, true, nil
		}
		return nil, false, wrapPostProcessBatchError(ctx, templateName, err)
	}
	if len(processed) != len(results) {
		return nil, false, fmt.Errorf(
			"template %q post-processor returned %d of %d batch outputs",
			templateName,
			len(processed),
			len(results),
		)
	}
	return processed, false, nil
}

func (e *ScriggoEngine) postProcessBatchSequential(
	ctx context.Context,
	templateName string,
	inputs []string,
) ([]string, error) {
	results := make([]string, len(inputs))
	for index, input := range inputs {
		processed, err := e.applyPostProcessorsUncached(ctx, templateName, input)
		if err != nil {
			return nil, &PostProcessBatchError{Index: index, Err: err}
		}
		results[index] = processed
	}
	return results, nil
}

func processPostProcessor(
	ctx context.Context,
	templateName string,
	processor PostProcessor,
	input string,
) (string, error) {
	if cause := context.Cause(ctx); cause != nil {
		return "", &RenderTimeoutError{TemplateName: templateName, Cause: cause}
	}
	var (
		result string
		err    error
	)
	if contextProcessor, ok := processor.(contextPostProcessor); ok {
		result, err = contextProcessor.processContext(ctx, templateName, input)
	} else {
		result, err = processor.Process(input)
	}
	if err != nil {
		if cause := context.Cause(ctx); cause != nil {
			return "", &RenderTimeoutError{TemplateName: templateName, Cause: cause}
		}
		return "", NewRenderError(templateName, err)
	}
	return result, nil
}

func wrapPostProcessBatchError(
	ctx context.Context,
	templateName string,
	err error,
) error {
	if cause := context.Cause(ctx); cause != nil {
		return &RenderTimeoutError{TemplateName: templateName, Cause: cause}
	}
	var batchErr *PostProcessBatchError
	if errors.As(err, &batchErr) {
		return &PostProcessBatchError{
			Index: batchErr.Index,
			Err:   NewRenderError(templateName, batchErr.Err),
		}
	}
	var indexed interface{ BatchIndex() int }
	if errors.As(err, &indexed) {
		return &PostProcessBatchError{
			Index: indexed.BatchIndex(),
			Err:   NewRenderError(templateName, err),
		}
	}
	return NewRenderError(templateName, err)
}
