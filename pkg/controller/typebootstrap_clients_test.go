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

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apiextensionsfake "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset/fake"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	restfake "k8s.io/client-go/rest/fake"

	"github.com/haproxy-haptic/HAPTIC/pkg/k8s/schemafetcher"
)

func TestDiscoveryOpenAPIV3ProviderHonorsCancellation(t *testing.T) {
	started := make(chan struct{})
	finished := make(chan struct{})
	client := newOpenAPITestClient(func(req *http.Request) (*http.Response, error) {
		switch req.URL.Path {
		case "/openapi/v3":
			return jsonResponse(`{"paths":{"apis/apps/v1":{"serverRelativeURL":"/openapi/v3/apis/apps/v1?hash=test"}}}`), nil
		case "/openapi/v3/apis/apps/v1":
			close(started)
			<-req.Context().Done()
			close(finished)
			return nil, req.Context().Err()
		default:
			return nil, fmt.Errorf("unexpected request path %q", req.URL.Path)
		}
	})
	provider := &discoveryOpenAPIV3Provider{restClient: client}
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		_, err := provider.GVSpec(ctx, schema.GroupVersion{Group: "apps", Version: "v1"})
		result <- err
	}()

	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("OpenAPI document request did not start")
	}
	cancel()
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("OpenAPI request did not observe cancellation")
	}
	select {
	case err := <-result:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(time.Second):
		t.Fatal("GVSpec did not return after cancellation")
	}
}

func TestDiscoveryOpenAPIV3ProviderFetchesRequestedGroupVersion(t *testing.T) {
	client := newOpenAPITestClient(func(req *http.Request) (*http.Response, error) {
		switch req.URL.Path {
		case "/openapi/v3":
			return jsonResponse(`{"paths":{"api/v1":{"serverRelativeURL":"/openapi/v3/api/v1?hash=test"}}}`), nil
		case "/openapi/v3/api/v1":
			assert.Equal(t, "test", req.URL.Query().Get("hash"))
			assert.Equal(t, runtime.ContentTypeJSON, req.Header.Get("Accept"))
			return jsonResponse(`{"openapi":"3.0.0","info":{"title":"core","version":"v1"},"paths":{}}`), nil
		default:
			return nil, fmt.Errorf("unexpected request path %q", req.URL.Path)
		}
	})
	provider := &discoveryOpenAPIV3Provider{restClient: client}

	spec, err := provider.GVSpec(t.Context(), schema.GroupVersion{Version: "v1"})
	require.NoError(t, err)
	require.NotNil(t, spec)
	assert.Equal(t, "3.0.0", spec.Version)
	assert.Equal(t, "core", spec.Info.Title)
}

func newOpenAPITestClient(roundTrip func(*http.Request) (*http.Response, error)) *restfake.RESTClient {
	return &restfake.RESTClient{
		NegotiatedSerializer: serializer.NewCodecFactory(runtime.NewScheme()).WithoutConversion(),
		GroupVersion:         schema.GroupVersion{Version: "v1"},
		Client:               restfake.CreateHTTPClient(roundTrip),
	}
}

func jsonResponse(body string) *http.Response {
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{runtime.ContentTypeJSON}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func TestAPIExtensionsCRDGetterReadsTheServingCRDByName(t *testing.T) {
	widgets := &apiextensionsv1.CustomResourceDefinition{
		ObjectMeta: metav1.ObjectMeta{Name: "widgets.example.com"},
		Spec: apiextensionsv1.CustomResourceDefinitionSpec{
			Group: "example.com",
			Names: apiextensionsv1.CustomResourceDefinitionNames{Kind: "Widget", Plural: "widgets"},
		},
	}
	discoveryRequests := 0
	client := newOpenAPITestClient(func(req *http.Request) (*http.Response, error) {
		discoveryRequests++
		switch req.URL.Path {
		case "/apis/example.com/v1":
			return jsonResponse(`{"groupVersion":"example.com/v1","resources":[` +
				`{"name":"widgets/status","kind":"Widget"},` +
				`{"name":"widgets","kind":"Widget"},` +
				`{"name":"gadgets","kind":"Gadget"}]}`), nil
		case "/apis/absent.example.com/v1":
			return &http.Response{
				StatusCode: http.StatusNotFound,
				Header:     http.Header{"Content-Type": []string{runtime.ContentTypeJSON}},
				Body:       io.NopCloser(strings.NewReader(`{"kind":"Status","apiVersion":"v1","status":"Failure","reason":"NotFound","code":404}`)),
			}, nil
		default:
			return nil, fmt.Errorf("unexpected request path %q", req.URL.Path)
		}
	})
	getter := &apiextensionsCRDGetter{clientset: apiextensionsfake.NewClientset(widgets), restClient: client}

	got, err := getter.GetCRD(t.Context(), schema.GroupVersionKind{Group: "example.com", Version: "v1", Kind: "Widget"})
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, "widgets.example.com", got.Name)

	for _, gvk := range []schema.GroupVersionKind{
		{Group: "example.com", Version: "v1", Kind: "Gadget"},
		{Group: "example.com", Version: "v1", Kind: "Unknown"},
		{Group: "absent.example.com", Version: "v1", Kind: "Widget"},
	} {
		_, err := getter.GetCRD(t.Context(), gvk)
		require.ErrorIs(t, err, schemafetcher.ErrNoCRD, gvk)
	}

	requests := discoveryRequests
	_, err = getter.GetCRD(t.Context(), schema.GroupVersionKind{Version: "v1", Kind: "Service"})
	require.ErrorIs(t, err, schemafetcher.ErrNoCRD)
	assert.Equal(t, requests, discoveryRequests, "no CRD serves the core group, so nothing is requested")
}
