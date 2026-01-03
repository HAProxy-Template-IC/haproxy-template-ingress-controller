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
	"context"
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"gitlab.com/haproxy-haptic/haptic/pkg/compression"
	"gitlab.com/haproxy-haptic/haptic/pkg/generated/clientset/versioned"
	"gitlab.com/haproxy-haptic/haptic/pkg/k8s/client"
	"gitlab.com/haproxy-haptic/haptic/pkg/k8s/configpublisher"
)

var (
	configCRDName    string
	configKubeconfig string
	configNamespace  string
)

// configCmd is the parent command for config operations.
var configCmd = &cobra.Command{
	Use:   "config",
	Short: "HAProxy configuration operations",
	Long: `Operations for viewing and managing HAProxy configurations.

The 'config' command group provides utilities for working with HAProxy
configurations stored in Kubernetes CRDs.

Available subcommands:
  view    View the current HAProxy configuration`,
}

// configViewCmd displays the current HAProxy configuration.
var configViewCmd = &cobra.Command{
	Use:   "view",
	Short: "View the current HAProxy configuration",
	Long: `View the current HAProxy configuration stored in the HAProxyCfg CRD.

This command fetches the HAProxyCfg resource that corresponds to the
HAProxyTemplateConfig CRD and displays its content. If the configuration
is compressed (for large configs exceeding the storage threshold), it is
automatically decompressed before display.

The CRD name and namespace are discovered using the same mechanism as the
'run' command:
  1. --crd-name flag (highest priority)
  2. CRD_NAME environment variable
  3. Default: "haproxy-config"

The namespace is auto-detected from the current kubeconfig context or
service account, or can be overridden with --namespace.

Examples:
  # View config using defaults
  haptic-controller config view

  # View config for a specific CRD
  haptic-controller config view --crd-name my-haproxy-config

  # View config from a specific namespace
  haptic-controller config view --namespace production

  # Pipe to a pager or syntax highlighter
  haptic-controller config view | less
  haptic-controller config view | bat -l haproxy`,
	RunE: runConfigView,
}

func init() {
	configViewCmd.Flags().StringVar(&configCRDName, "crd-name", "",
		"Name of the HAProxyTemplateConfig CRD (env: CRD_NAME)")
	configViewCmd.Flags().StringVar(&configKubeconfig, "kubeconfig", "",
		"Path to kubeconfig file (for out-of-cluster usage)")
	configViewCmd.Flags().StringVar(&configNamespace, "namespace", "",
		"Namespace to look for the HAProxyCfg resource (default: auto-detect)")

	configCmd.AddCommand(configViewCmd)
	rootCmd.AddCommand(configCmd)
}

func runConfigView(_ *cobra.Command, _ []string) error {
	// Configuration priority: CLI flags > Environment variables > Defaults
	if configCRDName == "" {
		configCRDName = os.Getenv("CRD_NAME")
	}
	if configCRDName == "" {
		configCRDName = DefaultCRDName
	}

	// Create Kubernetes client
	k8sClient, err := client.New(client.Config{
		Kubeconfig: configKubeconfig,
	})
	if err != nil {
		return fmt.Errorf("failed to create Kubernetes client: %w", err)
	}

	// Create CRD client using the REST config
	crdClient, err := versioned.NewForConfig(k8sClient.RestConfig())
	if err != nil {
		return fmt.Errorf("failed to create CRD client: %w", err)
	}

	// Determine namespace
	namespace := configNamespace
	if namespace == "" {
		namespace = k8sClient.Namespace()
	}
	if namespace == "" {
		return fmt.Errorf("namespace not specified and could not be auto-detected (use --namespace flag)")
	}

	// Compute the HAProxyCfg resource name from the template config name
	runtimeConfigName := configpublisher.GenerateRuntimeConfigName(configCRDName)

	// Create a context with timeout
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Fetch the HAProxyCfg resource
	haproxyCfg, err := crdClient.HaproxyTemplateICV1alpha1().
		HAProxyCfgs(namespace).
		Get(ctx, runtimeConfigName, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("failed to get HAProxyCfg %s/%s: %w", namespace, runtimeConfigName, err)
	}

	// Get content, decompressing if necessary
	content := haproxyCfg.Spec.Content
	if haproxyCfg.Spec.Compressed {
		decompressed, err := compression.Decompress(content)
		if err != nil {
			return fmt.Errorf("failed to decompress config: %w", err)
		}
		content = decompressed
	}

	// Output to stdout
	fmt.Print(content)

	return nil
}
