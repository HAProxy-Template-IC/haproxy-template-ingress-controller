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

package templating

import (
	"context"
	"errors"
	"fmt"
	"io"

	"gitlab.com/haproxy-haptic/scriggo/native"
)

type incrementalRendererContextKey struct{}
type incrementalScopeContextKey struct{}

// TextFragment is immutable, replayable text that can be retained by a text sink.
type TextFragment = native.TextFragment

// IncrementalRenderer evaluates a configured keyed component group.
type IncrementalRenderer interface {
	RenderIncremental(context.Context, string) (string, error)
}

// IncrementalTextFragmentRenderer retains immutable component output until the
// final text consumer needs its bytes.
type IncrementalTextFragmentRenderer interface {
	RenderIncrementalTextFragment(context.Context, string) (TextFragment, error)
}

// IncrementalValueReader resolves immutable keyed publications for a group cell.
type IncrementalValueReader interface {
	IncrementalValues(context.Context, string, string) ([]any, error)
}

// IncrementalCertifiedValueReader resolves reusable values with their immutable guard.
type IncrementalCertifiedValueReader interface {
	IncrementalValuesCertified(
		context.Context,
		string,
		string,
	) (*IncrementalCertifiedValues, error)
}

// IncrementalRankedFragmentReader resolves ranked immutable string publications.
type IncrementalRankedFragmentReader interface {
	IncrementalRankedFragments(context.Context, string, string) (string, error)
}

// IncrementalRankedTextFragmentReader retains ranked immutable string publications.
type IncrementalRankedTextFragmentReader interface {
	IncrementalRankedTextFragment(context.Context, string, string) (TextFragment, error)
}

// IncrementalRankedFragmentJoinReader resolves ranked strings with a delimiter.
type IncrementalRankedFragmentJoinReader interface {
	IncrementalRankedFragmentsJoin(context.Context, string, string, string) (string, error)
}

// IncrementalRankedTextFragmentJoinReader retains ranked strings with a delimiter.
type IncrementalRankedTextFragmentJoinReader interface {
	IncrementalRankedTextFragmentJoin(context.Context, string, string, string) (TextFragment, error)
}

// IncrementalComponentExecutor runs a component entry point without post-processing.
type IncrementalComponentExecutor interface {
	RenderIncrementalComponent(context.Context, string, map[string]any) (string, error)
}

// IncrementalComponentBatchItem is one isolated component execution.
type IncrementalComponentBatchItem struct {
	Context         context.Context
	TemplateContext map[string]any
	Activate        func() error
	Deactivate      func()
}

// IncrementalComponentBatchExecutor reuses execution infrastructure without
// merging component contexts or outputs.
type IncrementalComponentBatchExecutor interface {
	RenderIncrementalComponents(
		context.Context,
		string,
		[]IncrementalComponentBatchItem,
	) ([]string, error)
}

// IncrementalComponentVectorLifecycle owns one logical lifecycle per vector item.
type IncrementalComponentVectorLifecycle interface {
	Begin(index int) error
	End(index int, output string) error
	Abort(activeIndex int, cause error)
}

// IncrementalComponentVectorInput supplies fixed globals and concrete columns.
type IncrementalComponentVectorInput struct {
	Count         int
	SharedContext map[string]any
	Bindings      map[string]any
	Contexts      []context.Context
	Lifecycle     IncrementalComponentVectorLifecycle
}

// IncrementalComponentVectorEligibility is the exact per-item binding universe.
type IncrementalComponentVectorEligibility struct {
	BindingNames []string
}

// IncrementalComponentVectorRenderer executes a certified component once over a vector.
type IncrementalComponentVectorRenderer interface {
	IncrementalComponentVectorEligibility(
		templateName string,
	) (IncrementalComponentVectorEligibility, bool)
	RenderIncrementalComponentVector(
		context.Context,
		string,
		IncrementalComponentVectorInput,
	) error
}

// IncrementalComponentVectorCarrierLane supplies one entry point's ordered items.
type IncrementalComponentVectorCarrierLane struct {
	TemplateName string
	Count        int
	Bindings     map[string]any
	Contexts     []context.Context
}

// IncrementalComponentVectorCarrierEligibility is the carrier's exact static surface.
type IncrementalComponentVectorCarrierEligibility struct {
	TemplateNames []string
	BindingNames  []string
}

// IncrementalComponentVectorCarrierWaveLane fixes one lane's item count before execution.
type IncrementalComponentVectorCarrierWaveLane struct {
	TemplateName string
	Count        int
}

// IncrementalComponentVectorCarrierWave fixes one publication wave's child order.
type IncrementalComponentVectorCarrierWave struct {
	Lanes []IncrementalComponentVectorCarrierWaveLane
	// EntryPoints optionally fixes per-child dispatch; nil preserves lane order.
	EntryPoints []string
}

// IncrementalComponentVectorCarrierWaveLifecycle loads and seals one wave at a time.
type IncrementalComponentVectorCarrierWaveLifecycle interface {
	IncrementalComponentVectorLifecycle
	LoadWave(context.Context, int) ([]IncrementalComponentVectorCarrierLane, error)
	SealWave(int) error
}

// IncrementalComponentVectorCarrierWavesInput supplies an immutable multi-wave shape.
type IncrementalComponentVectorCarrierWavesInput struct {
	SharedContext map[string]any
	Waves         []IncrementalComponentVectorCarrierWave
	Lifecycle     IncrementalComponentVectorCarrierWaveLifecycle
}

// IncrementalComponentVectorCarrierWavesRenderer executes every wave in one VM.
type IncrementalComponentVectorCarrierWavesRenderer interface {
	IncrementalComponentVectorCarrierEligibility() (
		IncrementalComponentVectorCarrierEligibility,
		bool,
	)
	RenderIncrementalComponentVectorCarrierWaves(
		context.Context,
		IncrementalComponentVectorCarrierWavesInput,
	) error
}

// IncrementalComponentSourceTransactionChild identifies one exact child result.
type IncrementalComponentSourceTransactionChild struct {
	TemplateName string
	Index        int
}

// IncrementalComponentSourceTransaction groups callable children behind one source load.
type IncrementalComponentSourceTransaction struct {
	Children []IncrementalComponentSourceTransactionChild
}

// IncrementalComponentSourceTransactionWave fixes one wave's source transactions.
type IncrementalComponentSourceTransactionWave struct {
	Transactions []IncrementalComponentSourceTransaction
}

// IncrementalComponentSourceTransactionBatch transfers owned source columns without flattening.
type IncrementalComponentSourceTransactionBatch struct {
	Bindings      map[string]any
	Contexts      []context.Context
	ChildContexts []context.Context
}

// IncrementalComponentSourceTransactionLifecycle loads and seals exact source waves.
type IncrementalComponentSourceTransactionLifecycle interface {
	IncrementalComponentVectorLifecycle
	LoadSourceTransactionWave(context.Context, int) (IncrementalComponentSourceTransactionBatch, error)
	SealWave(int) error
}

// IncrementalComponentSourceTransactionsInput supplies immutable source/child topology.
type IncrementalComponentSourceTransactionsInput struct {
	SharedContext map[string]any
	Waves         []IncrementalComponentSourceTransactionWave
	Lifecycle     IncrementalComponentSourceTransactionLifecycle
}

// IncrementalComponentSourceTransactionsRenderer executes children under shared source loads.
type IncrementalComponentSourceTransactionsRenderer interface {
	IncrementalComponentSourceTransactionsEligibility() bool
	RenderIncrementalComponentSourceTransactions(
		context.Context,
		IncrementalComponentSourceTransactionsInput,
	) error
}

// IncrementalResourceInvocationLease authenticates a component-bound resource facade.
type IncrementalResourceInvocationLease interface {
	ValidateIncrementalResourceInvocation(context.Context) error
}

// IncrementalResourceBinder binds the resource surface used by one component entry point.
type IncrementalResourceBinder interface {
	BindIncrementalResources(string, any, IncrementalResourceInvocationLease) (any, error)
}

// IncrementalSourceTransactionChildSelector authenticates the active child.
type IncrementalSourceTransactionChildSelector interface {
	ActiveIncrementalSourceTransactionChild() (int, error)
}

// IncrementalSourceTransactionSelectorAuthenticator binds a selector to its execution lease.
type IncrementalSourceTransactionSelectorAuthenticator interface {
	ValidateIncrementalSourceTransactionSelector(IncrementalSourceTransactionChildSelector) error
}

// IncrementalSourceTransactionResourceBinder binds callables to their exact child owners.
type IncrementalSourceTransactionResourceBinder interface {
	BindIncrementalSourceTransactionResources(
		[]string,
		any,
		IncrementalResourceInvocationLease,
		IncrementalSourceTransactionChildSelector,
	) (any, error)
}

// IncrementalComponentBatchError identifies the first failed batch item.
type IncrementalComponentBatchError struct {
	Index int
	Err   error
}

func (e *IncrementalComponentBatchError) Error() string {
	return fmt.Sprintf("incremental component batch item %d failed: %v", e.Index, e.Err)
}

func (e *IncrementalComponentBatchError) Unwrap() error {
	return e.Err
}

// WithIncrementalRenderer attaches one render transaction to Scriggo execution.
func WithIncrementalRenderer(ctx context.Context, renderer IncrementalRenderer) context.Context {
	return context.WithValue(ctx, incrementalRendererContextKey{}, renderer)
}

// WithIncrementalScope identifies the root template that owns component calls.
func WithIncrementalScope(ctx context.Context, scope string) context.Context {
	return context.WithValue(ctx, incrementalScopeContextKey{}, scope)
}

// IncrementalScope returns the root template attached to ctx.
func IncrementalScope(ctx context.Context) (string, bool) {
	scope, ok := ctx.Value(incrementalScopeContextKey{}).(string)
	return scope, ok && scope != ""
}

func scriggoIncrementalRender(env native.Env, name string) TextFragment {
	renderer, ok := env.Context().Value(incrementalRendererContextKey{}).(IncrementalRenderer)
	if !ok || renderer == nil {
		env.Stop(fmt.Errorf("incremental component %q has no render transaction", name))
		return textFragmentString("")
	}
	if fragmentRenderer, ok := renderer.(IncrementalTextFragmentRenderer); ok {
		result, err := fragmentRenderer.RenderIncrementalTextFragment(env.Context(), name)
		if err != nil {
			env.Stop(err)
			return textFragmentString("")
		}
		if result == nil {
			env.Stop(fmt.Errorf("incremental component %q returned a nil text fragment", name))
			return textFragmentString("")
		}
		return result
	}
	result, err := renderer.RenderIncremental(env.Context(), name)
	if err != nil {
		env.Stop(err)
		return textFragmentString("")
	}
	return textFragmentString(result)
}

type textFragmentString string

func (f textFragmentString) WriteTo(writer io.Writer) (int64, error) {
	if writer == nil {
		return 0, errors.New("text fragment writer is nil")
	}
	written, err := io.WriteString(writer, string(f))
	if err == nil && written != len(f) {
		err = io.ErrShortWrite
	}
	return int64(written), err
}

func scriggoIncrementalValues(env native.Env, group, cell string) []any {
	renderer, ok := env.Context().Value(incrementalRendererContextKey{}).(IncrementalValueReader)
	if !ok || renderer == nil {
		env.Stop(fmt.Errorf("incremental values %q/%q have no render transaction", group, cell))
		return nil
	}
	if certified, ok := renderer.(IncrementalCertifiedValueReader); ok {
		certifiedValues, err := certified.IncrementalValuesCertified(env.Context(), group, cell)
		if err != nil {
			env.Stop(err)
			return nil
		}
		values, certificate, valid := certifiedValues.unwrap()
		if !valid {
			env.Stop(fmt.Errorf("incremental values %q/%q have an invalid immutable certificate", group, cell))
			return nil
		}
		if err := RegisterIncrementalImmutableCertificate(env.Context(), certificate); err != nil {
			env.Stop(err)
			return nil
		}
		return values
	}
	values, err := renderer.IncrementalValues(env.Context(), group, cell)
	if err != nil {
		env.Stop(err)
		return nil
	}
	if err := RegisterIncrementalImmutableInputs(env.Context(), values); err != nil {
		env.Stop(err)
		return nil
	}
	return values
}

func scriggoIncrementalRankedFragments(env native.Env, group, cell string) string {
	renderer, ok := env.Context().Value(incrementalRendererContextKey{}).(IncrementalRenderer)
	if !ok || renderer == nil {
		env.Stop(fmt.Errorf("incremental ranked fragments %q/%q have no render transaction", group, cell))
		return ""
	}
	reader, ok := renderer.(IncrementalRankedFragmentReader)
	if !ok {
		env.Stop(fmt.Errorf("incremental ranked fragments %q/%q have no render transaction", group, cell))
		return ""
	}
	fragments, err := reader.IncrementalRankedFragments(env.Context(), group, cell)
	if err != nil {
		env.Stop(err)
		return ""
	}
	return fragments
}

// IncrementalRankedFragmentBytesReader reports a ranked cell's joined length.
type IncrementalRankedFragmentBytesReader interface {
	IncrementalRankedFragmentBytes(ctx context.Context, group, cell string) (int, error)
}

func scriggoIncrementalRankedFragmentBytes(env native.Env, group, cell string) int {
	renderer, ok := env.Context().Value(incrementalRendererContextKey{}).(IncrementalRenderer)
	if !ok || renderer == nil {
		env.Stop(fmt.Errorf("incremental ranked fragment bytes %q/%q have no render transaction", group, cell))
		return 0
	}
	reader, ok := renderer.(IncrementalRankedFragmentBytesReader)
	if !ok {
		env.Stop(fmt.Errorf("incremental ranked fragment bytes %q/%q have no render transaction", group, cell))
		return 0
	}
	length, err := reader.IncrementalRankedFragmentBytes(env.Context(), group, cell)
	if err != nil {
		env.Stop(err)
		return 0
	}
	return length
}

func scriggoIncrementalRankedFragmentsJoin(env native.Env, group, cell, delimiter string) string {
	renderer, ok := env.Context().Value(incrementalRendererContextKey{}).(IncrementalRenderer)
	if !ok || renderer == nil {
		env.Stop(fmt.Errorf("incremental ranked fragment join %q/%q has no render transaction", group, cell))
		return ""
	}
	reader, ok := renderer.(IncrementalRankedFragmentJoinReader)
	if !ok {
		env.Stop(fmt.Errorf("incremental ranked fragment join %q/%q has no render transaction", group, cell))
		return ""
	}
	fragments, err := reader.IncrementalRankedFragmentsJoin(env.Context(), group, cell, delimiter)
	if err != nil {
		env.Stop(err)
		return ""
	}
	return fragments
}

func scriggoIncrementalRankedTextFragment(env native.Env, group, cell string) TextFragment {
	renderer, ok := env.Context().Value(incrementalRendererContextKey{}).(IncrementalRenderer)
	if !ok || renderer == nil {
		env.Stop(fmt.Errorf("incremental ranked fragments %q/%q have no render transaction", group, cell))
		return textFragmentString("")
	}
	if reader, ok := renderer.(IncrementalRankedTextFragmentReader); ok {
		fragment, err := reader.IncrementalRankedTextFragment(env.Context(), group, cell)
		if err != nil {
			env.Stop(err)
			return textFragmentString("")
		}
		if fragment == nil {
			env.Stop(fmt.Errorf("incremental ranked fragments %q/%q returned a nil text fragment", group, cell))
			return textFragmentString("")
		}
		return fragment
	}
	reader, ok := renderer.(IncrementalRankedFragmentReader)
	if !ok {
		env.Stop(fmt.Errorf("incremental ranked fragments %q/%q have no render transaction", group, cell))
		return textFragmentString("")
	}
	fragments, err := reader.IncrementalRankedFragments(env.Context(), group, cell)
	if err != nil {
		env.Stop(err)
		return textFragmentString("")
	}
	return textFragmentString(fragments)
}

func scriggoIncrementalRankedTextFragmentJoin(
	env native.Env,
	group, cell, delimiter string,
) TextFragment {
	renderer, ok := env.Context().Value(incrementalRendererContextKey{}).(IncrementalRenderer)
	if !ok || renderer == nil {
		env.Stop(fmt.Errorf("incremental ranked fragment join %q/%q has no render transaction", group, cell))
		return textFragmentString("")
	}
	if reader, ok := renderer.(IncrementalRankedTextFragmentJoinReader); ok {
		fragment, err := reader.IncrementalRankedTextFragmentJoin(
			env.Context(), group, cell, delimiter,
		)
		if err != nil {
			env.Stop(err)
			return textFragmentString("")
		}
		if fragment == nil {
			env.Stop(fmt.Errorf("incremental ranked fragment join %q/%q returned a nil text fragment", group, cell))
			return textFragmentString("")
		}
		return fragment
	}
	reader, ok := renderer.(IncrementalRankedFragmentJoinReader)
	if !ok {
		env.Stop(fmt.Errorf("incremental ranked fragment join %q/%q has no render transaction", group, cell))
		return textFragmentString("")
	}
	fragments, err := reader.IncrementalRankedFragmentsJoin(env.Context(), group, cell, delimiter)
	if err != nil {
		env.Stop(err)
		return textFragmentString("")
	}
	return textFragmentString(fragments)
}
