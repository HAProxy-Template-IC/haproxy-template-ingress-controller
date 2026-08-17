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
	"fmt"

	apiextensionsclientset "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller"
	"gitlab.com/haproxy-haptic/haptic/pkg/k8s/schemafetcher"
)

// liveCluster bundles the clients a command needs when an input defaults to
// the live cluster rather than a local file: dynamic (listing resources),
// clientset (discovery for GVK resolution and served checks), and the schema
// fetcher (typed-resource bootstrap).
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
