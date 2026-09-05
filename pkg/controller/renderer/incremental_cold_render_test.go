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

package renderer

import (
	"fmt"
	"testing"
	"time"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/names"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/rendercontext"
	"gitlab.com/haproxy-haptic/haptic/pkg/core/config"
	"gitlab.com/haproxy-haptic/haptic/pkg/stores"
	"gitlab.com/haproxy-haptic/haptic/pkg/templating"
)

func renderServiceStaticCold(
	t *testing.T,
	service *RenderService,
	provider stores.StoreProvider,
) (*RenderResult, error) {
	t.Helper()
	ctx := t.Context()
	bctx, err := service.buildRenderingContext(ctx, provider, rendercontext.RenderModeReconcile)
	if err != nil {
		return nil, err
	}
	coldRender, err := NewColdIncrementalRender(ctx, &ColdIncrementalRenderConfig{
		Config:             service.config,
		Engine:             service.engine,
		StoreProvider:      provider,
		Mode:               rendercontext.RenderModeReconcile,
		TemplateContext:    bctx.Context,
		ResourceErrors:     bctx.ResourceErrors,
		Logger:             service.logger,
		TypedResourceTypes: service.typedResourceTypes,
	})
	if err != nil {
		return nil, err
	}
	ctx = coldRender.Context(ctx)
	main, err := rendercontext.RenderMainDocument(
		templating.WithIncrementalScope(ctx, names.MainTemplateName),
		service.engine,
		bctx.Context,
		bctx.PlanRegistry,
		true,
	)
	if resourceErr := bctx.Err(ctx); resourceErr != nil {
		return nil, resourceErr
	}
	if err != nil {
		return nil, fmt.Errorf("rendering %s: %w", names.MainTemplateName, err)
	}
	staticFiles, err := service.renderAuxiliaryFiles(ctx, bctx.Context)
	if err != nil {
		return nil, err
	}
	if err := service.renderK8sResources(ctx, bctx.Context, bctx.RenderedResourceCollector); err != nil {
		return nil, err
	}
	if err := coldRender.ValidateIncrementalCalls(); err != nil {
		return nil, err
	}
	return service.finishRender(ctx, bctx, rendercontext.RenderModeReconcile, &renderArtifacts{
		main:             main,
		staticFiles:      staticFiles,
		inputTransaction: bctx.inputTransaction,
		startTime:        time.Now(),
	})
}

func renderStaticColdIncremental(
	t *testing.T,
	cfg *config.Config,
	engine templating.Engine,
	provider stores.StoreProvider,
) (*rendercontext.BuildResult, string, error) {
	t.Helper()
	storeMap := make(map[string]stores.Store, len(provider.StoreNames()))
	for _, name := range provider.StoreNames() {
		storeMap[name] = provider.GetStore(name)
	}
	resourceStores, haproxyPodStore := rendercontext.SeparateHAProxyPodStore(storeMap)
	bctx := rendercontext.NewBuilder(
		t.Context(),
		cfg,
		&templating.PathResolver{},
		coldInputTestLogger(),
		rendercontext.WithStores(resourceStores),
		rendercontext.WithHAProxyPodStore(haproxyPodStore),
		rendercontext.WithRenderMode(rendercontext.RenderModeReconcile),
	).Build()
	coldRender, err := NewColdIncrementalRender(t.Context(), &ColdIncrementalRenderConfig{
		Config:          cfg,
		Engine:          engine,
		StoreProvider:   provider,
		Mode:            rendercontext.RenderModeReconcile,
		TemplateContext: bctx.Context,
		ResourceErrors:  bctx.ResourceErrors,
		Logger:          coldInputTestLogger(),
	})
	if err != nil {
		return bctx, "", err
	}
	ctx := coldRender.Context(t.Context())
	main, err := rendercontext.RenderMain(
		templating.WithIncrementalScope(ctx, names.MainTemplateName),
		engine,
		bctx.Context,
		bctx.PlanRegistry,
		false,
	)
	if resourceErr := bctx.Err(ctx); resourceErr != nil {
		return bctx, "", resourceErr
	}
	if err != nil {
		return bctx, "", fmt.Errorf("rendering %s: %w", names.MainTemplateName, err)
	}
	if err := coldRender.ValidateIncrementalCalls(); err != nil {
		return bctx, "", err
	}
	return bctx, main.Config, nil
}
