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
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apiextensionsclientset "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/rest"
	"k8s.io/kube-openapi/pkg/handler3"
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

type discoveryOpenAPIV3Provider struct {
	restClient rest.Interface
}

func newDiscoveryOpenAPIV3Provider(d discovery.DiscoveryInterface) schemafetcher.OpenAPIV3Provider {
	return &discoveryOpenAPIV3Provider{restClient: d.RESTClient()}
}

func (p *discoveryOpenAPIV3Provider) GVSpec(ctx context.Context, gv schema.GroupVersion) (*spec3.OpenAPI, error) {
	if p.restClient == nil {
		return nil, errors.New("OpenAPI discovery REST client is unavailable")
	}

	discoveryData, err := p.restClient.Get().AbsPath("/openapi/v3").Do(ctx).Raw()
	if err != nil {
		return nil, fmt.Errorf("fetching OpenAPI discovery: %w", err)
	}
	var document handler3.OpenAPIV3Discovery
	if err := json.Unmarshal(discoveryData, &document); err != nil {
		return nil, fmt.Errorf("decoding OpenAPI discovery: %w", err)
	}

	apiPath := "api/" + gv.Version
	if gv.Group != "" {
		apiPath = "apis/" + gv.Group + "/" + gv.Version
	}
	item, ok := document.Paths[apiPath]
	if !ok {
		return nil, fmt.Errorf("group version %s has no OpenAPI v3 document", gv.String())
	}

	rootPrefix := strings.TrimSuffix(p.restClient.Get().AbsPath("/").URL().Path, "/")
	serverRelativeURL := strings.TrimPrefix(item.ServerRelativeURL, rootPrefix)
	request := p.restClient.Get()
	if strings.HasPrefix(serverRelativeURL, "/openapi/v3") {
		locator, err := url.Parse(serverRelativeURL)
		if err != nil {
			return nil, fmt.Errorf("parsing OpenAPI document URL: %w", err)
		}
		request = request.AbsPath(locator.Path)
		for name, values := range locator.Query() {
			for _, value := range values {
				request.Param(name, value)
			}
		}
	} else {
		request = request.RequestURI(serverRelativeURL)
	}

	schemaData, err := request.SetHeader("Accept", runtime.ContentTypeJSON).Do(ctx).Raw()
	if err != nil {
		return nil, fmt.Errorf("fetching OpenAPI document for %s: %w", gv.String(), err)
	}
	var spec spec3.OpenAPI
	if err := json.Unmarshal(schemaData, &spec); err != nil {
		return nil, fmt.Errorf("decoding OpenAPI document for %s: %w", gv.String(), err)
	}
	return &spec, nil
}
