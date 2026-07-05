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

package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	apiextensionsclientset "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	utilyaml "k8s.io/apimachinery/pkg/util/yaml"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller"
	"gitlab.com/haproxy-haptic/haptic/pkg/k8s/schemafetcher"
)

// ingressGVR is the resource migrate-check audits. The Ingress kind is the
// subcommand's fixed operational domain (see findIngressResourceKey); no
// vendor-specific resource knowledge lives here.
var ingressGVR = schema.GroupVersionResource{
	Group:    "networking.k8s.io",
	Version:  "v1",
	Resource: "ingresses",
}

// liveCluster bundles the clients migrate-check needs when any input
// defaults to the live cluster: dynamic (Ingress listing), clientset
// (discovery for GVK resolution and served checks), and the cluster
// schema fetcher (typed-resource bootstrap).
type liveCluster struct {
	clientset kubernetes.Interface
	dyn       dynamic.Interface
	fetcher   schemafetcher.Fetcher
}

// discovery returns the cluster discovery interface.
func (lc *liveCluster) discovery() discovery.DiscoveryInterface {
	return lc.clientset.Discovery()
}

// connectLiveCluster builds the cluster clients from, in order: the
// --kubeconfig flag, the standard kubeconfig loading rules ($KUBECONFIG or
// ~/.kube/config), then in-cluster service-account credentials.
func connectLiveCluster(kubeconfig string) (*liveCluster, error) {
	restConfig, err := buildRestConfig(kubeconfig)
	if err != nil {
		return nil, err
	}

	clientset, err := kubernetes.NewForConfig(restConfig)
	if err != nil {
		return nil, fmt.Errorf("creating Kubernetes client: %w", err)
	}
	dyn, err := dynamic.NewForConfig(restConfig)
	if err != nil {
		return nil, fmt.Errorf("creating dynamic client: %w", err)
	}
	apiext, err := apiextensionsclientset.NewForConfig(restConfig)
	if err != nil {
		return nil, fmt.Errorf("creating apiextensions client: %w", err)
	}

	return &liveCluster{
		clientset: clientset,
		dyn:       dyn,
		fetcher:   controller.NewClusterSchemaFetcher(apiext, clientset.Discovery()),
	}, nil
}

// buildRestConfig resolves the REST config for the cluster connection.
func buildRestConfig(kubeconfig string) (*rest.Config, error) {
	if kubeconfig != "" {
		restConfig, err := clientcmd.BuildConfigFromFlags("", kubeconfig)
		if err != nil {
			return nil, fmt.Errorf("loading kubeconfig %s: %w", kubeconfig, err)
		}
		return restConfig, nil
	}

	// Standard loading rules: $KUBECONFIG, then ~/.kube/config.
	loadingRules := clientcmd.NewDefaultClientConfigLoadingRules()
	restConfig, err := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
		loadingRules, &clientcmd.ConfigOverrides{}).ClientConfig()
	if err == nil {
		return restConfig, nil
	}

	// Fall back to in-cluster credentials (running as a pod).
	inCluster, inErr := rest.InClusterConfig()
	if inErr == nil {
		return inCluster, nil
	}

	return nil, fmt.Errorf("no cluster access: kubeconfig (%v) and in-cluster config (%v) both failed.\n"+
		"Hint: mount a kubeconfig and set KUBECONFIG, pass --kubeconfig, or run offline with --resources <dir> --schema-dir <dir>",
		err, inErr)
}

// listIngresses lists the cluster's networking.k8s.io/v1 Ingresses —
// namespace-scoped when namespace is non-empty, otherwise cluster-wide.
func (lc *liveCluster) listIngresses(ctx context.Context, namespace string) ([]*unstructured.Unstructured, error) {
	var iface dynamic.ResourceInterface = lc.dyn.Resource(ingressGVR)
	if namespace != "" {
		iface = lc.dyn.Resource(ingressGVR).Namespace(namespace)
	}
	list, err := iface.List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("listing Ingresses: %w", err)
	}
	out := make([]*unstructured.Unstructured, 0, len(list.Items))
	for i := range list.Items {
		out = append(out, &list.Items[i])
	}
	return out, nil
}

// loadIngressesFromDir reads the audited Ingresses from a manifest
// directory instead of the cluster: every *.yaml / *.yml / *.json file is
// parsed as (multi-document) YAML and the networking.k8s.io/v1 Ingress
// documents are kept. Other kinds are ignored so mixed manifest dumps
// (e.g. `kubectl get all -o yaml` output) work as-is.
func loadIngressesFromDir(dir, namespace string) ([]*unstructured.Unstructured, error) {
	// Collect manifest paths first (the WalkDir callback does no
	// filesystem I/O — it only inspects the fs.DirEntry it's handed), then
	// read them below. Reading inside the callback trips a symlink-TOCTOU
	// lint; splitting the phases avoids it without suppressions.
	var paths []string
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && isManifestFile(path) {
			paths = append(paths, path)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("reading manifests from %s: %w", dir, err)
	}

	var out []*unstructured.Unstructured
	for _, path := range paths {
		data, rerr := os.ReadFile(filepath.Clean(path))
		if rerr != nil {
			return nil, fmt.Errorf("reading %s: %w", path, rerr)
		}
		docs, perr := parseIngressDocs(data, path)
		if perr != nil {
			return nil, perr
		}
		out = append(out, docs...)
	}

	if namespace != "" {
		filtered := out[:0]
		for _, ing := range out {
			if ing.GetNamespace() == namespace {
				filtered = append(filtered, ing)
			}
		}
		out = filtered
	}

	sort.Slice(out, func(i, j int) bool {
		if out[i].GetNamespace() != out[j].GetNamespace() {
			return out[i].GetNamespace() < out[j].GetNamespace()
		}
		return out[i].GetName() < out[j].GetName()
	})
	return out, nil
}

// isManifestFile reports whether the path looks like a manifest file.
func isManifestFile(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".yaml", ".yml", ".json":
		return true
	default:
		return false
	}
}

// parseIngressDocs parses (multi-document) manifest bytes and returns the
// networking.k8s.io/v1 Ingress documents. It accepts both bare Ingress
// documents and `kind: List` wrappers with an `items` array — the latter is
// what `kubectl get ingress -A -o yaml` produces, the most likely way an
// operator exports Ingresses for an offline audit.
func parseIngressDocs(data []byte, path string) ([]*unstructured.Unstructured, error) {
	var out []*unstructured.Unstructured
	// A spec-compliant streaming decoder, not a substring split on "\n---":
	// annotation values routinely carry PEM material ("-----BEGIN
	// CERTIFICATE-----") or block scalars whose lines start with "---", and a
	// naive split would slice a document mid-scalar and fail the whole audit.
	dec := utilyaml.NewYAMLOrJSONDecoder(bytes.NewReader(data), 4096)
	for {
		var obj map[string]any
		if err := dec.Decode(&obj); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return nil, fmt.Errorf("parsing %s: %w", path, err)
		}
		if obj == nil {
			continue
		}
		out = append(out, ingressesFromObject(obj)...)
	}
	return out, nil
}

// ingressesFromObject extracts the Ingress resources from one parsed
// document: the document itself when it is an Ingress, or every Ingress in
// its `items` array when it is a List wrapper.
func ingressesFromObject(obj map[string]any) []*unstructured.Unstructured {
	u := &unstructured.Unstructured{Object: obj}

	if u.GetAPIVersion() == ingressGVR.Group+"/"+ingressGVR.Version && u.GetKind() == "Ingress" {
		return []*unstructured.Unstructured{u}
	}

	if strings.HasSuffix(u.GetKind(), "List") {
		items, ok, _ := unstructured.NestedSlice(obj, "items")
		if !ok {
			return nil
		}
		out := make([]*unstructured.Unstructured, 0, len(items))
		for _, item := range items {
			itemMap, ok := item.(map[string]any)
			if !ok {
				continue
			}
			out = append(out, ingressesFromObject(itemMap)...)
		}
		return out
	}

	return nil
}
