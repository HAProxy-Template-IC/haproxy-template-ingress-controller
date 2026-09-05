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
	"fmt"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/names"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/renderplan"
	"gitlab.com/haproxy-haptic/haptic/pkg/rendercontent"
	"gitlab.com/haproxy-haptic/haptic/pkg/templating"
)

// MainRender is the result of rendering and assembling haproxy.cfg.
type MainRender struct {
	// Config is the assembled configuration — what HAProxy, the consistency
	// check and every downstream consumer see.
	Config string

	// Document is the authenticated assembled configuration root.
	Document rendercontent.Document

	// Sections partition Config in emission order.
	Sections []renderplan.Section

	// IncludeStats is per-snippet profiling, non-nil only when the engine was
	// built with profiling and profiling was requested.
	IncludeStats []templating.IncludeStats
}

// MainDocumentRender retains the assembled configuration without materializing it.
type MainDocumentRender struct {
	Document     rendercontent.Document
	Sections     []renderplan.Section
	IncludeStats []templating.IncludeStats

	// Reuse reports how much of the previous assembly this render kept, and
	// why it could not keep all of it.
	Reuse AssemblyReuse
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
	cacheSessions ...*RenderCacheSession,
) (MainRender, error) {
	document, err := RenderMainDocument(ctx, engine, renderCtx, registry, profiling, cacheSessions...)
	if err != nil {
		return MainRender{}, err
	}
	config, err := document.Document.String()
	if err != nil {
		return MainRender{}, err
	}
	return MainRender{
		Config:       config,
		Document:     document.Document,
		Sections:     document.Sections,
		IncludeStats: document.IncludeStats,
	}, nil
}

// RenderMainDocument renders and assembles haproxy.cfg as an authenticated document.
func RenderMainDocument(
	ctx context.Context,
	engine templating.Engine,
	renderCtx map[string]any,
	registry *PlanRegistry,
	profiling bool,
	cacheSessions ...*RenderCacheSession,
) (MainDocumentRender, error) {
	if len(cacheSessions) > 1 {
		return MainDocumentRender{}, fmt.Errorf("rendering %s: more than one cache session", names.MainTemplateName)
	}
	var cacheSession *RenderCacheSession
	if len(cacheSessions) == 1 {
		cacheSession = cacheSessions[0]
	}
	var rendered string
	var renderedDocument rendercontent.Document
	var includeStats []templating.IncludeStats
	var rawDocument rendercontent.Document
	var hasRawDocument bool
	var renderGeneration *renderDocumentGeneration
	var identityPostProcess bool
	var err error
	if rawRenderer, ok := engine.(templating.RawTextRenderer); ok && !rawRenderer.RawTextRenderInstrumented() {
		raw, rawErr := renderMainRaw(ctx, engine, rawRenderer, renderCtx, cacheSession)
		if rawErr != nil {
			return MainDocumentRender{}, rawErr
		}
		rendered = raw.rendered
		renderedDocument = raw.renderedDocument
		rawDocument = raw.rawDocument
		hasRawDocument = true
		renderGeneration = raw.renderGeneration
		identityPostProcess = raw.identityPostProcess
		if profiling {
			includeStats = raw.includeStats
		}
	} else if profiling {
		rendered, includeStats, err = engine.RenderWithProfiling(ctx, names.MainTemplateName, renderCtx)
	} else {
		rendered, err = engine.Render(ctx, names.MainTemplateName, renderCtx)
	}
	if err != nil {
		return MainDocumentRender{}, err
	}
	if !identityPostProcess {
		renderedDocument, err = renderDocumentFromString(rendered)
		if err != nil {
			return MainDocumentRender{}, err
		}
	}

	var post PostProcessFunc
	if !identityPostProcess {
		post = func(ctx context.Context, text string) (string, error) {
			return engine.PostProcess(ctx, names.MainTemplateName, text)
		}
	}
	var postBatch PostProcessBatchFunc
	if batcher, ok := engine.(templating.PostProcessBatcher); ok && !identityPostProcess {
		postBatch = func(ctx context.Context, texts []string) ([]string, error) {
			return batcher.PostProcessBatch(ctx, names.MainTemplateName, texts)
		}
	}
	document, sections, reuse, err := registry.assembleDocument(
		ctx,
		renderedDocument,
		post,
		postBatch,
		rawDocument,
		hasRawDocument,
		cacheSession,
		renderGeneration,
	)
	if err != nil {
		return MainDocumentRender{}, err
	}
	if cause := context.Cause(ctx); cause != nil {
		return MainDocumentRender{}, &templating.RenderTimeoutError{
			TemplateName: names.MainTemplateName,
			Cause:        cause,
		}
	}
	return MainDocumentRender{
		Document: document, Sections: sections, IncludeStats: includeStats, Reuse: reuse,
	}, nil
}

type rawMainRender struct {
	rendered            string
	renderedDocument    rendercontent.Document
	includeStats        []templating.IncludeStats
	rawDocument         rendercontent.Document
	renderGeneration    *renderDocumentGeneration
	identityPostProcess bool
}

func renderMainRaw(
	ctx context.Context,
	engine templating.Engine,
	rawRenderer templating.RawTextRenderer,
	renderCtx map[string]any,
	cacheSession *RenderCacheSession,
) (rawMainRender, error) {
	identityProof, identityPostProcess, err := certifiedPostProcessIdentity(engine, names.MainTemplateName)
	if err != nil {
		return rawMainRender{}, err
	}
	previous, hasPrevious, err := cacheSession.load()
	if err != nil {
		return rawMainRender{}, err
	}
	writer := &renderDocumentWriter{}
	if hasPrevious {
		if size, sizeErr := previous.Bytes(); sizeErr == nil {
			writer.builder.Grow(size)
		}
	}
	includeStats, err := rawRenderer.RenderRawTo(ctx, names.MainTemplateName, renderCtx, writer)
	if err != nil {
		return rawMainRender{}, err
	}
	if err := writer.ensureTrailingNewline(); err != nil {
		return rawMainRender{}, err
	}
	content, err := buildRenderDocument(&writer.builder, previous, hasPrevious)
	if err != nil {
		return rawMainRender{}, err
	}
	result := rawMainRender{
		includeStats:        includeStats,
		rawDocument:         content,
		identityPostProcess: identityPostProcess,
	}
	if identityPostProcess {
		if cause := context.Cause(ctx); cause != nil {
			return rawMainRender{}, &templating.RenderTimeoutError{
				TemplateName: names.MainTemplateName,
				Cause:        cause,
			}
		}
		result.renderedDocument = content
		result.renderGeneration, err = cacheSession.prepareIdentityDocument(content, identityProof)
		if err != nil {
			return rawMainRender{}, err
		}
		return result, nil
	}
	rendered, reused, hit, err := cacheSession.processed(ctx, names.MainTemplateName, content)
	if err != nil {
		return rawMainRender{}, err
	}
	if !hit {
		document, stringErr := content.String()
		if stringErr != nil {
			return rawMainRender{}, stringErr
		}
		rendered, err = engine.PostProcess(ctx, names.MainTemplateName, document)
		if err != nil {
			return rawMainRender{}, err
		}
	}
	result.rendered = rendered
	result.renderGeneration, err = cacheSession.prepareDocument(
		names.MainTemplateName, content, rendered, reused,
	)
	if err != nil {
		return rawMainRender{}, err
	}
	return result, nil
}

func certifiedPostProcessIdentity(
	engine templating.Engine,
	templateName string,
) (*templating.PostProcessReuseProof, bool, error) {
	prover, ok := engine.(templating.PostProcessReuseProver)
	if !ok {
		return nil, false, nil
	}
	proof, err := prover.PostProcessReuseProof(templateName)
	if err != nil {
		return nil, false, err
	}
	if proof == nil {
		return nil, false, nil
	}
	// A wrapper that promotes this proof but overrides PostProcess owns none: refuse reuse, don't fail.
	identity, certErr := proof.CertifiesIdentity(engine, templateName)
	if certErr != nil {
		proof, identity = nil, false
	}
	return proof, identity, nil
}

func buildRenderDocument(
	builder *rendercontent.DocumentBuilder,
	previous rendercontent.Document,
	hasPrevious bool,
) (rendercontent.Document, error) {
	if hasPrevious {
		return builder.Build(&previous)
	}
	return builder.Build(nil)
}
