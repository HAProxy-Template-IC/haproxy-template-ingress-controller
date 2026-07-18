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
//   - HAProxyTemplateConfig CRD name: --crd-name flag, CRD_NAME env var, or "haproxy-config" default
//   - Secret name: --secret-name flag, SECRET_NAME env var, or "haproxy-credentials" default
//   - Webhook cert directory: --webhook-cert-dir flag or WEBHOOK_CERT_DIR env var (directory holding tls.crt/tls.key; optional, empty disables the webhook)
//   - Webhook HTTPS port: WEBHOOK_PORT env var (default 9443)
//   - Debug HTTP port: --debug-port flag or DEBUG_PORT env var (default 0 = disabled)
//   - Metrics HTTP port: METRICS_PORT env var (default 9090; 0 disables)
//   - Kubeconfig: --kubeconfig flag (for out-of-cluster development)
//
// The controller runs until receiving SIGTERM or SIGINT, at which point it performs
// graceful shutdown.
package main

import (
	"errors"
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

// rootCmd represents the base command when called without any subcommands.
var rootCmd = &cobra.Command{
	Use:   "haptic-controller",
	Short: "HAProxy Template Ingress Controller",
	Long: `HAProxy Template Ingress Controller - Template-driven HAProxy configuration management.

Commands:

  run           - Run the controller (watches CRDs and manages HAProxy)
  validate      - Validate a HAProxyTemplateConfig with embedded tests
  benchmark     - Measure template render performance
  migrate-check - Audit another controller's Ingresses before migrating to HAPTIC

Use "haptic-controller [command] --help" for more information about a command.`,
}

const (
	// defaultCRDName is the default name for the HAProxyTemplateConfig CRD resource.
	defaultCRDName = "haproxy-config"

	// defaultSecretName is the default name for the credentials Secret.
	// #nosec G101 -- This is a Kubernetes resource name, not an actual credential
	defaultSecretName = "haproxy-credentials"

	// defaultDebugPort is the default port for the debug HTTP server (0 = disabled).
	defaultDebugPort = 0
)

func init() {
	// Add subcommands
	rootCmd.AddCommand(runCmd)
	rootCmd.AddCommand(validateCmd)
	rootCmd.AddCommand(benchmarkCmd)
	rootCmd.AddCommand(migrateCheckCmd)
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		// A command may request a specific exit code (e.g. migrate-check's
		// 0/1/2 contract). A nil wrapped error means the command already
		// printed everything the user needs — don't echo an empty line.
		var coded *exitCodeError
		if errors.As(err, &coded) {
			if coded.err != nil {
				fmt.Fprintln(os.Stderr, coded.err)
			}
			os.Exit(coded.code)
		}
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
