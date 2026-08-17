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

package rendercontext

import (
	"context"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/names"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/renderplan"
	"gitlab.com/haproxy-haptic/haptic/pkg/templating"
)

// MainRender is the result of rendering and assembling haproxy.cfg.
type MainRender struct {
	// Config is the assembled configuration — what HAProxy, the consistency
	// check and every downstream consumer see.
	Config string

	// Sections partition Config in emission order.
	Sections []renderplan.Section

	// IncludeStats is per-snippet profiling, non-nil only when the engine was
	// built with profiling and profiling was requested.
	IncludeStats []templating.IncludeStats
}

// RenderMain renders haproxy.cfg and assembles the sections templates declared
// while rendering it. Every render site goes through it, so a config that
// reaches HAProxy always has a plan behind it and the token invariants are
// checked the same way everywhere.
func RenderMain(
	ctx context.Context,
	engine templating.Engine,
	renderCtx map[string]any,
	registry *PlanRegistry,
	profiling bool,
) (MainRender, error) {
	var rendered string
	var includeStats []templating.IncludeStats
	var err error
	if profiling {
		rendered, includeStats, err = engine.RenderWithProfiling(ctx, names.MainTemplateName, renderCtx)
	} else {
		rendered, err = engine.Render(ctx, names.MainTemplateName, renderCtx)
	}
	if err != nil {
		return MainRender{}, err
	}

	post := func(ctx context.Context, text string) (string, error) {
		return engine.PostProcess(ctx, names.MainTemplateName, text)
	}
	config, sections, err := registry.Assemble(ctx, rendered, post)
	if err != nil {
		return MainRender{}, err
	}
	return MainRender{Config: config, Sections: sections, IncludeStats: includeStats}, nil
}
