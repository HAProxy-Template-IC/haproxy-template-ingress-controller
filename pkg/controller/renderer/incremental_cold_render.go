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

package renderer

import (
	"context"
	"errors"
	"log/slog"
	"reflect"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/rendercontext"
	"gitlab.com/haproxy-haptic/haptic/pkg/core/config"
	"gitlab.com/haproxy-haptic/haptic/pkg/stores"
	"gitlab.com/haproxy-haptic/haptic/pkg/templating"
)

// ColdIncrementalRenderConfig supplies one non-caching render transaction.
type ColdIncrementalRenderConfig struct {
	Config             *config.Config
	Engine             templating.Engine
	StoreProvider      stores.StoreProvider
	Mode               rendercontext.RenderMode
	TemplateContext    map[string]any
	ResourceErrors     *rendercontext.ResourceErrorCollector
	Logger             *slog.Logger
	TypedResourceTypes map[string]reflect.Type
}

// ColdIncrementalRender runs components for offline tools without retaining a cache.
type ColdIncrementalRender struct {
	renderer *coldIncrementalRenderer
}

// NewColdIncrementalRender prepares one offline render over an existing static context.
func NewColdIncrementalRender(
	ctx context.Context,
	cfg *ColdIncrementalRenderConfig,
) (*ColdIncrementalRender, error) {
	if cfg == nil || cfg.Config == nil {
		return nil, errors.New("cold incremental render requires a config")
	}
	state := newIncrementalRenderState(cfg.Config, cfg.Engine)
	if state == nil {
		return &ColdIncrementalRender{}, nil
	}
	if cfg.StoreProvider == nil {
		return nil, errors.New("cold incremental render requires a store provider")
	}
	runtime, err := newColdIncrementalRenderer(
		ctx,
		state,
		cfg.StoreProvider,
		cfg.Mode,
		cfg.TemplateContext,
		cfg.ResourceErrors,
		incrementalLoggerContext{logger: cfg.Logger, typedResourceTypes: cfg.TypedResourceTypes},
	)
	if err != nil {
		return nil, err
	}
	return &ColdIncrementalRender{renderer: runtime}, nil
}

// Context attaches the cold transaction to a render context.
func (r *ColdIncrementalRender) Context(ctx context.Context) context.Context {
	if r == nil || r.renderer == nil {
		return ctx
	}
	return templating.WithIncrementalRenderer(ctx, r.renderer)
}

// ValidateIncrementalCalls verifies complete canonical group placement.
func (r *ColdIncrementalRender) HasIncrementalCalls() bool {
	return r.renderer != nil && r.renderer.HasIncrementalCalls()
}

func (r *ColdIncrementalRender) ValidateIncrementalCalls() error {
	if r == nil || r.renderer == nil {
		return nil
	}
	return r.renderer.ValidateIncrementalCalls()
}
