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
// clients to pkg/k8s/schemafetcher's CRDGetter and
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
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/rest"
	"k8s.io/kube-openapi/pkg/handler3"
	"k8s.io/kube-openapi/pkg/spec3"

	"github.com/haproxy-haptic/HAPTIC/pkg/k8s/schemafetcher"
)

// NewClusterSchemaFetcher composes the apiextensions CRD getter and the
// discovery-backed OpenAPI v3 provider into the live-cluster schema
// fetcher. The controller's iteration wiring and the CLI commands that reach
// a live cluster both build their schema access through this entry point.
func NewClusterSchemaFetcher(apiext apiextensionsclientset.Interface, d discovery.DiscoveryInterface) schemafetcher.Fetcher {
	return schemafetcher.NewClusterFetcher(
		newAPIExtensionsCRDGetter(apiext, d),
		newDiscoveryOpenAPIV3Provider(d),
	)
}

// apiextensionsCRDGetter implements [schemafetcher.CRDGetter] with a
// discovery lookup of the GVK's plural and a GET of the CRD named
// <plural>.<group>: a consistent LIST of every CRD took 3.5 s on a k3s
// apiserver where the GET took 5 ms (#329).
type apiextensionsCRDGetter struct {
	clientset  apiextensionsclientset.Interface
	restClient rest.Interface
}

func newAPIExtensionsCRDGetter(clientset apiextensionsclientset.Interface, d discovery.DiscoveryInterface) schemafetcher.CRDGetter {
	return &apiextensionsCRDGetter{clientset: clientset, restClient: d.RESTClient()}
}

// GetCRD returns schemafetcher.ErrNoCRD for the core group, a group/version
// discovery does not serve, or a kind with no CRD of that name.
func (g *apiextensionsCRDGetter) GetCRD(ctx context.Context, gvk schema.GroupVersionKind) (*apiextensionsv1.CustomResourceDefinition, error) {
	if gvk.Group == "" {
		return nil, schemafetcher.ErrNoCRD
	}
	if g.restClient == nil {
		return nil, errors.New("discovery REST client is unavailable")
	}
	data, err := g.restClient.Get().AbsPath("/apis", gvk.Group, gvk.Version).Do(ctx).Raw()
	if apierrors.IsNotFound(err) {
		return nil, schemafetcher.ErrNoCRD
	}
	if err != nil {
		return nil, fmt.Errorf("discovering resources of %s: %w", gvk.GroupVersion(), err)
	}
	var resources metav1.APIResourceList
	if err := json.Unmarshal(data, &resources); err != nil {
		return nil, fmt.Errorf("decoding resources of %s: %w", gvk.GroupVersion(), err)
	}
	for i := range resources.APIResources {
		resource := &resources.APIResources[i]
		if resource.Kind != gvk.Kind || strings.Contains(resource.Name, "/") {
			continue
		}
		name := resource.Name + "." + gvk.Group
		crd, err := g.clientset.ApiextensionsV1().CustomResourceDefinitions().Get(ctx, name, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			return nil, schemafetcher.ErrNoCRD
		}
		if err != nil {
			return nil, fmt.Errorf("getting CRD %s: %w", name, err)
		}
		return crd, nil
	}
	return nil, schemafetcher.ErrNoCRD
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
