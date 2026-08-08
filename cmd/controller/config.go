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
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"gitlab.com/haproxy-haptic/haptic/pkg/compression"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/conversion"
	"gitlab.com/haproxy-haptic/haptic/pkg/generated/clientset/versioned"
	"gitlab.com/haproxy-haptic/haptic/pkg/k8s/client"
	"gitlab.com/haproxy-haptic/haptic/pkg/k8s/configpublisher"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/yaml"
)

const hapticAPIVersion = "v1alpha1"

// templateConfigGVR is the input config CRD `config view --input` reads.
var templateConfigGVR = schema.GroupVersionResource{
	Group:    "haproxy-haptic.org",
	Version:  hapticAPIVersion,
	Resource: "haproxytemplateconfigs",
}

// templateLibraryGVR holds the library content the config references.
var templateLibraryGVR = schema.GroupVersionResource{
	Group:    "haproxy-haptic.org",
	Version:  hapticAPIVersion,
	Resource: "haproxytemplatelibraries",
}

var (
	configCRDName    string
	configKubeconfig string
	configNamespace  string
	configShowInput  bool
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
		"Name of a HAProxyTemplateConfig. Repeatable or comma-separated (env: CRD_NAME); the last one is the "+
			"primary, whose name the published HAProxyCfg is derived from.")
	configViewCmd.Flags().BoolVar(&configShowInput, "input", false,
		"Print the merged INPUT config the controller assembles from every --crd-name, instead of the rendered "+
			"HAProxy output. No single object holds the whole input once the chart splits it per template library.")
	configViewCmd.Flags().StringVar(&configKubeconfig, "kubeconfig", "",
		"Path to kubeconfig file (for out-of-cluster usage)")
	configViewCmd.Flags().StringVar(&configNamespace, "namespace", "",
		"Namespace to look for the HAProxyCfg resource (default: auto-detect)")

	configCmd.AddCommand(configViewCmd)
	rootCmd.AddCommand(configCmd)
}

func runConfigView(_ *cobra.Command, _ []string) error {
	// Configuration priority: CLI flags > Environment variables > Defaults
	configCRDName = resolveConfigName(configCRDName)

	// Create Kubernetes client
	k8sClient, err := client.New(client.Config{
		Kubeconfig: configKubeconfig,
	})
	if err != nil {
		return fmt.Errorf("creating Kubernetes client: %w", err)
	}

	// Create CRD client using the REST config
	crdClient, err := versioned.NewForConfig(k8sClient.RestConfig())
	if err != nil {
		return fmt.Errorf("creating CRD client: %w", err)
	}

	// Determine namespace
	namespace := configNamespace
	if namespace == "" {
		namespace = k8sClient.Namespace()
	}
	if namespace == "" {
		return errors.New("namespace not specified and could not be auto-detected (use --namespace flag)")
	}

	runtimeConfigName := configpublisher.GenerateRuntimeConfigName(configCRDName)

	// Create a context with timeout
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if configShowInput {
		return printMergedInputConfig(ctx, k8sClient, namespace, configCRDName)
	}

	// Fetch the HAProxyCfg resource
	haproxyCfg, err := crdClient.HaproxyTemplateICV1alpha1().
		HAProxyCfgs(namespace).
		Get(ctx, runtimeConfigName, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("getting HAProxyCfg %s/%s: %w", namespace, runtimeConfigName, err)
	}

	// Get content, decompressing if necessary
	content := haproxyCfg.Spec.Content
	if haproxyCfg.Spec.Compressed {
		decompressed, err := compression.Decompress(content)
		if err != nil {
			return fmt.Errorf("decompressing config: %w", err)
		}
		content = decompressed
	}

	// Output to stdout
	fmt.Print(content)

	return nil
}

// printMergedInputConfig fetches the config plus every HAProxyTemplateLibrary
// it references and prints the merged spec — the input the controller
// assembles, as opposed to the rendered HAProxy output `config view` shows by
// default.
//
// Template library content lives in separate objects, so no single object shows
// the whole picture; this is how an operator gets it back.
func printMergedInputConfig(ctx context.Context, k8sClient *client.Client, namespace, name string) error {
	config, err := k8sClient.DynamicClient().Resource(templateConfigGVR).Namespace(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("getting HAProxyTemplateConfig %s/%s: %w", namespace, name, err)
	}

	sources, unresolved, err := controller.ResolveLibraryRefs(ctx, k8sClient, templateLibraryGVR, config)
	if err != nil {
		return err
	}
	for _, ref := range unresolved {
		fmt.Fprintf(os.Stderr, "# WARNING: unresolved snippet reference: %s\n", ref)
	}

	merged, overrides, err := conversion.MergeSpecs(sources)
	if err != nil {
		return fmt.Errorf("merging HAProxyTemplateConfigs: %w", err)
	}
	for _, override := range overrides {
		fmt.Fprintf(os.Stderr, "# snippet %q from %s is overridden by %s\n",
			override.Name, override.PreviousSource, override.WinningSource)
	}

	out, err := yaml.Marshal(merged.Object["spec"])
	if err != nil {
		return fmt.Errorf("marshalling merged spec: %w", err)
	}
	fmt.Print(string(out))
	return nil
}
