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

import "context"

type postProcessTransactionContextKey struct{}

type postProcessTransactionRuntime struct {
	engine      *ScriggoEngine
	transaction *postProcessCacheTransaction
}

type scriggoPostProcessTransaction struct {
	transaction *postProcessCacheTransaction
}

func (e *ScriggoEngine) BeginPostProcessTransaction(
	ctx context.Context,
) (context.Context, PostProcessTransaction) {
	if e == nil || len(e.postProcessCacheIdentities) == 0 {
		return ctx, nil
	}
	transaction := e.postProcessCache.begin()
	runtime := &postProcessTransactionRuntime{engine: e, transaction: transaction}
	return context.WithValue(ctx, postProcessTransactionContextKey{}, runtime),
		&scriggoPostProcessTransaction{transaction: transaction}
}

func (e *ScriggoEngine) postProcessTransaction(ctx context.Context) *postProcessCacheTransaction {
	runtime, _ := ctx.Value(postProcessTransactionContextKey{}).(*postProcessTransactionRuntime)
	if runtime == nil || runtime.engine != e {
		return nil
	}
	return runtime.transaction
}

func (t *scriggoPostProcessTransaction) Stage(
	ctx context.Context,
) (PostProcessPublication, error) {
	publication, err := t.transaction.stage(ctx)
	if err != nil {
		return nil, err
	}
	return &scriggoPostProcessPublication{publication: publication}, nil
}

func (t *scriggoPostProcessTransaction) Abort() {
	if t != nil && t.transaction != nil {
		t.transaction.abort()
	}
}

type scriggoPostProcessPublication struct {
	publication *postProcessCachePublication
}

func (p *scriggoPostProcessPublication) Publish() {
	if p == nil || p.publication == nil || !p.publication.publish() {
		panic("post-process cache publication was already finalized")
	}
}

func (p *scriggoPostProcessPublication) Abort() {
	if p != nil && p.publication != nil {
		p.publication.abort()
	}
}
