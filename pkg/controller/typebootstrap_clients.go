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

package controller

// This file holds the adapters that bridge HAPTIC's concrete K8s
// clients to pkg/k8s/schemafetcher's CRDLister and
// OpenAPIV3Provider interfaces. Putting them in the controller
// package is the natural fit per arch-go Rule 1 ("controller can
// import everything"): they exist solely to compose the iteration's
// type-bootstrap wiring at runtime. Schemafetcher itself stays
// client-agnostic so its tests run without standing up a fake
// cluster.

import (
	"context"
	"fmt"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apiextensionsclientset "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/openapi3"
	"k8s.io/kube-openapi/pkg/spec3"

	"gitlab.com/haproxy-haptic/haptic/pkg/k8s/schemafetcher"
)

// NewClusterSchemaFetcher composes the apiextensions CRD lister and the
// discovery-backed OpenAPI v3 provider into the live-cluster schema
// fetcher. The controller's iteration wiring and the CLI commands that reach
// a live cluster both build their schema access through this entry point.
func NewClusterSchemaFetcher(apiext apiextensionsclientset.Interface, d discovery.DiscoveryInterface) schemafetcher.Fetcher {
	return schemafetcher.NewClusterFetcher(
		newAPIExtensionsCRDLister(apiext),
		newDiscoveryOpenAPIV3Provider(d),
	)
}

// apiextensionsCRDLister implements [schemafetcher.CRDLister] by
// listing CRDs through the apiextensions clientset.
type apiextensionsCRDLister struct {
	clientset apiextensionsclientset.Interface
}

// newAPIExtensionsCRDLister wires the apiextensions clientset to
// schemafetcher's CRDLister interface. The clientset is constructed
// from the controller's rest.Config — see setupReconciliation
// where this is called.
func newAPIExtensionsCRDLister(clientset apiextensionsclientset.Interface) schemafetcher.CRDLister {
	return &apiextensionsCRDLister{clientset: clientset}
}

// ListCRDs returns every CRD in the cluster. We list all (rather
// than filtering by group / name) because schemafetcher caches the
// result for the lifetime of one ClusterFetcher — and the typical
// HAPTIC deployment watches resources spread across half a dozen
// API groups, so the in-cluster list is the right granularity.
// Field-selector filtering wouldn't help; the LIST call's cost is
// dominated by the per-CRD body size, not the count.
func (l *apiextensionsCRDLister) ListCRDs(ctx context.Context) ([]apiextensionsv1.CustomResourceDefinition, error) {
	list, err := l.clientset.ApiextensionsV1().CustomResourceDefinitions().List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("listing CRDs: %w", err)
	}
	return list.Items, nil
}

// discoveryOpenAPIV3Provider implements
// [schemafetcher.OpenAPIV3Provider] by walking the cluster's
// aggregated OpenAPI v3 endpoint through client-go/openapi3.Root.
type discoveryOpenAPIV3Provider struct {
	root openapi3.Root
}

// newDiscoveryOpenAPIV3Provider builds the discovery-backed
// provider. The DiscoveryInterface gives us OpenAPIV3() (returning
// an openapi.Client), and openapi3.NewRoot wraps that in the
// higher-level GVSpec/GroupVersions API.
//
// The Root caches per-GroupVersion specs internally via the
// underlying openapi.Client. schemafetcher's ClusterFetcher adds
// its own per-GV cache on top — that's belt-and-braces and keeps
// the in-process spec object stable across renderer / webhook /
// status-applier callers, none of which currently share fetchers
// but might in the future.
func newDiscoveryOpenAPIV3Provider(d discovery.DiscoveryInterface) schemafetcher.OpenAPIV3Provider {
	return &discoveryOpenAPIV3Provider{
		root: openapi3.NewRoot(d.OpenAPIV3()),
	}
}

// GVSpec proxies through to the openapi3.Root. Client-go's
// openapi3 package issues blocking HTTP calls without context
// plumbing, so a naive `return p.root.GVSpec(gv)` would ignore
// ctx entirely — and on a slow apiserver block its caller for
// however long the cluster took to respond, defeating the
// bootstrap-level deadline set in [runTypeBootstrap]. Run the
// blocking call on a goroutine and select on ctx so the caller's
// timeout / cancellation actually fires.
//
// On ctx cancellation the goroutine is left to complete in the
// background; its result is discarded. That's a one-off heap
// retention, not a leak — the upstream HTTP client closes the
// connection when its response is read or the process exits.
func (p *discoveryOpenAPIV3Provider) GVSpec(ctx context.Context, gv schema.GroupVersion) (*spec3.OpenAPI, error) {
	type result struct {
		sp  *spec3.OpenAPI
		err error
	}
	ch := make(chan result, 1)
	go func() {
		sp, err := p.root.GVSpec(gv)
		ch <- result{sp: sp, err: err}
	}()
	select {
	case r := <-ch:
		return r.sp, r.err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}
