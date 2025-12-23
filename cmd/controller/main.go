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

// Package main provides the CLI entrypoint for the HAProxy template ingress controller.
//
// The controller accepts configuration via CLI flags, environment variables, or defaults:
//
//   - ConfigMap name: --configmap-name flag, CONFIGMAP_NAME env var, or "haproxy-config" default
//   - Secret name: --secret-name flag, SECRET_NAME env var, or "haproxy-credentials" default
//   - Webhook cert Secret: --webhook-cert-secret-name flag, WEBHOOK_CERT_SECRET_NAME env var, or "haproxy-webhook-certs" default
//   - Kubeconfig: --kubeconfig flag (for out-of-cluster development)
//
// The controller runs until receiving SIGTERM or SIGINT, at which point it performs
// graceful shutdown.
package main

import (
	"fmt"
	"os"

	_ "github.com/KimMachineGun/automemlimit"
	"github.com/spf13/cobra"
)

// rootCmd represents the base command when called without any subcommands.
var rootCmd = &cobra.Command{
	Use:   "haptic-controller",
	Short: "HAProxy Template Ingress Controller",
	Long: `HAProxy Template Ingress Controller - Template-driven HAProxy configuration management.

The controller provides two main commands:

  run      - Run the controller (watches CRDs and manages HAProxy)
  validate - Validate a HAProxyTemplateConfig with embedded tests

Use "haptic-controller [command] --help" for more information about a command.`,
}

const (
	// DefaultCRDName is the default name for the HAProxyTemplateConfig CRD resource.
	DefaultCRDName = "haproxy-config"

	// DefaultSecretName is the default name for the credentials Secret.
	// #nosec G101 -- This is a Kubernetes resource name, not an actual credential
	DefaultSecretName = "haproxy-credentials"

	// DefaultWebhookCertSecretName is the default name for the webhook certificate Secret.
	// #nosec G101 -- This is a Kubernetes resource name, not an actual credential
	DefaultWebhookCertSecretName = "haproxy-webhook-certs"

	// DefaultDebugPort is the default port for the debug HTTP server (0 = disabled).
	DefaultDebugPort = 0
)

func init() {
	// Add subcommands
	rootCmd.AddCommand(runCmd)
	rootCmd.AddCommand(validateCmd)
	rootCmd.AddCommand(benchmarkCmd)
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
