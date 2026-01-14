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

// Package client provides a wrapper around the Kubernetes client-go library.
//
// This package simplifies Kubernetes client creation and provides utilities
// for common operations like namespace discovery.
package client

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

const (
	// DefaultNamespaceFile is the standard location for the service account namespace.
	DefaultNamespaceFile = "/var/run/secrets/kubernetes.io/serviceaccount/namespace"
)

// Client wraps a Kubernetes clientset with additional utilities.
type Client struct {
	clientset     kubernetes.Interface
	dynamicClient dynamic.Interface
	restConfig    *rest.Config
	namespace     string // Cached current namespace
}

// Config contains configuration options for creating a Kubernetes client.
type Config struct {
	// Kubeconfig path for out-of-cluster configuration.
	// If empty, uses in-cluster configuration.
	Kubeconfig string

	// Namespace is the default namespace for operations.
	// If empty, will be discovered from service account.
	Namespace string
}

// New creates a new Kubernetes client with the provided configuration.
//
// If Config.Kubeconfig is empty, uses in-cluster configuration.
// If Config.Namespace is empty, discovers namespace from service account token.
//
// Example:
//
//	// In-cluster client
//	client, err := client.New(client.Config{})
//
//	// Out-of-cluster client
//	client, err := client.New(client.Config{
//	    Kubeconfig: "/path/to/kubeconfig",
//	    Namespace:  "default",
//	})
func New(cfg Config) (*Client, error) {
	var restConfig *rest.Config
	var err error

	if cfg.Kubeconfig != "" {
		// Out-of-cluster configuration
		restConfig, err = clientcmd.BuildConfigFromFlags("", cfg.Kubeconfig)
		if err != nil {
			return nil, &ClientError{
				Operation: "build kubeconfig",
				Cause:     err,
			}
		}
	} else {
		// In-cluster configuration
		restConfig, err = rest.InClusterConfig()
		if err != nil {
			return nil, &ClientError{
				Operation: "get in-cluster config",
				Cause:     err,
			}
		}
	}

	// Configure rate limiter for high-frequency CRD operations
	// Default values (QPS=5, Burst=10) are too conservative for controllers
	// that update multiple CRDs per reconciliation cycle
	restConfig.QPS = 50
	restConfig.Burst = 100

	// Create clientset
	clientset, err := kubernetes.NewForConfig(restConfig)
	if err != nil {
		return nil, &ClientError{
			Operation: "create clientset",
			Cause:     err,
		}
	}

	// Create dynamic client
	dynamicClient, err := dynamic.NewForConfig(restConfig)
	if err != nil {
		return nil, &ClientError{
			Operation: "create dynamic client",
			Cause:     err,
		}
	}

	client := &Client{
		clientset:     clientset,
		dynamicClient: dynamicClient,
		restConfig:    restConfig,
		namespace:     cfg.Namespace,
	}

	// Discover namespace if not provided
	if client.namespace == "" {
		ns, err := DiscoverNamespace()
		if err != nil {
			// Non-fatal: log but continue with empty namespace
			// Some operations may not require a namespace
			client.namespace = ""
		} else {
			client.namespace = ns
		}
	}

	return client, nil
}

// NewFromClientset creates a Client from an existing Kubernetes clientset.
// This is useful for testing with fake clients.
func NewFromClientset(clientset kubernetes.Interface, dynamicClient dynamic.Interface, namespace string) *Client {
	return &Client{
		clientset:     clientset,
		dynamicClient: dynamicClient,
		namespace:     namespace,
	}
}

// Clientset returns the underlying Kubernetes clientset.
func (c *Client) Clientset() kubernetes.Interface {
	return c.clientset
}

// DynamicClient returns the underlying dynamic client.
func (c *Client) DynamicClient() dynamic.Interface {
	return c.dynamicClient
}

// RestConfig returns the underlying REST configuration.
func (c *Client) RestConfig() *rest.Config {
	return c.restConfig
}

// Namespace returns the default namespace for this client.
func (c *Client) Namespace() string {
	return c.namespace
}

// GetResource fetches a specific Kubernetes resource by name in the client's namespace.
//
// The resource is fetched from the client's default namespace (auto-detected from
// service account or specified during client creation).
//
// Parameters:
//   - ctx: Context for cancellation and timeout
//   - gvr: GroupVersionResource identifying the resource type
//   - name: Name of the specific resource to fetch
//
// Returns:
//   - The resource as an unstructured.Unstructured object
//   - An error if the resource cannot be fetched
//
// Example:
//
//	// Fetch a ConfigMap
//	gvr := schema.GroupVersionResource{
//	    Group:    "",
//	    Version:  "v1",
//	    Resource: "configmaps",
//	}
//	cm, err := client.GetResource(ctx, gvr, "my-config")
func (c *Client) GetResource(ctx context.Context, gvr schema.GroupVersionResource, name string) (*unstructured.Unstructured, error) {
	if c.namespace == "" {
		return nil, &ClientError{
			Operation: "get resource",
			Cause:     fmt.Errorf("no namespace available (not in cluster and not specified)"),
		}
	}

	resource, err := c.dynamicClient.Resource(gvr).Namespace(c.namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil, &ClientError{
			Operation: fmt.Sprintf("get resource %s/%s in namespace %s", gvr.Resource, name, c.namespace),
			Cause:     err,
		}
	}

	return resource, nil
}

// DiscoverNamespace reads the current namespace from the service account token.
//
// Returns:
//   - The namespace string
//   - An error if the namespace cannot be discovered
//
// The namespace is read from /var/run/secrets/kubernetes.io/serviceaccount/namespace
// which is automatically mounted in pods by Kubernetes.
func DiscoverNamespace() (string, error) {
	return DiscoverNamespaceFromFile(DefaultNamespaceFile)
}

// DiscoverNamespaceFromFile reads the namespace from the specified file.
// This is primarily useful for testing.
func DiscoverNamespaceFromFile(path string) (string, error) {
	// Check if file exists
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return "", &NamespaceDiscoveryError{
			Path:  path,
			Cause: err,
		}
	}

	// Read namespace from file
	data, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		return "", &NamespaceDiscoveryError{
			Path:  path,
			Cause: err,
		}
	}

	namespace := string(data)
	if namespace == "" {
		return "", &NamespaceDiscoveryError{
			Path:  path,
			Cause: os.ErrInvalid,
		}
	}

	return namespace, nil
}
