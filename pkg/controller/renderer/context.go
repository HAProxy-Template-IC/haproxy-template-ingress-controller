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

package renderer

import (
	"context"

	"gitlab.com/haproxy-template-ic/haproxy-template-ingress-controller/pkg/controller/httpstore"
	"gitlab.com/haproxy-template-ic/haproxy-template-ingress-controller/pkg/controller/rendercontext"
	"gitlab.com/haproxy-template-ic/haproxy-template-ingress-controller/pkg/templating"
)

// buildRenderingContext wraps stores for template access and builds the template context.
//
// This method delegates to the centralized rendercontext.Builder to ensure consistent
// context creation across all usages (renderer, testrunner, benchmark, dryrunvalidator).
//
// See rendercontext.Builder for the full context structure documentation.
func (c *Component) buildRenderingContext(ctx context.Context, pathResolver *templating.PathResolver, isValidation bool) (map[string]interface{}, *rendercontext.FileRegistry) {
	// Create HTTP fetcher if available
	var httpFetcher templating.HTTPFetcher
	if c.httpStoreComponent != nil {
		httpFetcher = httpstore.NewHTTPStoreWrapper(
			ctx,
			c.httpStoreComponent,
			c.logger,
			isValidation,
		)
	} else {
		c.logger.Warn("httpStoreComponent is nil, http.Fetch() will not be available in templates")
	}

	// Warn if HAProxy pod store is missing
	if c.haproxyPodStore == nil {
		c.logger.Warn("HAProxy pods store is nil, controller.haproxy_pods will not be available")
	}

	// Build context using centralized builder
	builder := rendercontext.NewBuilder(
		c.config,
		pathResolver,
		c.logger,
		rendercontext.WithStores(c.stores),
		rendercontext.WithHAProxyPodStore(c.haproxyPodStore),
		rendercontext.WithHTTPFetcher(httpFetcher),
		rendercontext.WithCapabilities(&c.capabilities),
	)

	return builder.Build()
}
