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
	"log/slog"
	"math"
	"os"
	"os/signal"
	"runtime"
	"runtime/debug"
	"strconv"
	"syscall"

	"github.com/spf13/cobra"

	"haptic/pkg/controller"
	"haptic/pkg/core/logging"
	"haptic/pkg/k8s/client"
)

var (
	runCRDName               string
	runSecretName            string
	runWebhookCertSecretName string
	runKubeconfig            string
	runDebugPort             int
)

// runCmd represents the run command (controller main loop).
var runCmd = &cobra.Command{
	Use:   "run",
	Short: "Run the HAProxy Template Ingress Controller",
	Long: `Run the HAProxy Template Ingress Controller.

The controller watches a HAProxyTemplateConfig CRD and Kubernetes resources,
renders HAProxy configurations from templates, and synchronizes them to HAProxy
instances via the Dataplane API.

Configuration is loaded from:
1. Command-line flags (highest priority)
2. Environment variables
3. Default values (lowest priority)

Example usage:
  # Run with default configuration
  controller run

  # Run with custom CRD name
  controller run --crd-name my-haproxy-config

  # Run with kubeconfig (out-of-cluster development)
  controller run --kubeconfig ~/.kube/config

  # Enable debug server
  controller run --debug-port 6060`,
	RunE: runController,
}

func init() {
	runCmd.Flags().StringVar(&runCRDName, "crd-name", "",
		"Name of the HAProxyTemplateConfig CRD containing controller configuration (env: CRD_NAME)")
	runCmd.Flags().StringVar(&runSecretName, "secret-name", "",
		"Name of the Secret containing HAProxy Dataplane API credentials (env: SECRET_NAME)")
	runCmd.Flags().StringVar(&runWebhookCertSecretName, "webhook-cert-secret-name", "",
		"Name of the Secret containing webhook TLS certificates (env: WEBHOOK_CERT_SECRET_NAME)")
	runCmd.Flags().StringVar(&runKubeconfig, "kubeconfig", "",
		"Path to kubeconfig file (for out-of-cluster development)")
	runCmd.Flags().IntVar(&runDebugPort, "debug-port", 0,
		"Port for debug HTTP server (0 to disable, env: DEBUG_PORT)")
}

func runController(cmd *cobra.Command, args []string) error {
	// Configuration priority: CLI flags > Environment variables > Defaults

	// CRD name
	if runCRDName == "" {
		runCRDName = os.Getenv("CRD_NAME")
	}
	if runCRDName == "" {
		runCRDName = DefaultCRDName
	}

	// Secret name
	if runSecretName == "" {
		runSecretName = os.Getenv("SECRET_NAME")
	}
	if runSecretName == "" {
		runSecretName = DefaultSecretName
	}

	// Webhook certificate Secret name
	if runWebhookCertSecretName == "" {
		runWebhookCertSecretName = os.Getenv("WEBHOOK_CERT_SECRET_NAME")
	}
	if runWebhookCertSecretName == "" {
		runWebhookCertSecretName = DefaultWebhookCertSecretName
	}

	// Debug port
	if runDebugPort == 0 {
		if envDebugPort := os.Getenv("DEBUG_PORT"); envDebugPort != "" {
			if port, err := strconv.Atoi(envDebugPort); err == nil {
				runDebugPort = port
			}
		}
	}
	if runDebugPort == 0 {
		runDebugPort = DefaultDebugPort
	}

	// Set up structured logging with dynamic level support
	// LOG_LEVEL env var: TRACE, DEBUG, INFO (default), WARN, ERROR (case-insensitive)
	// The level can be overridden at runtime via ConfigMap's logging.level field
	logLevelEnv := os.Getenv("LOG_LEVEL")
	logger := logging.NewDynamicLogger(logLevelEnv)
	slog.SetDefault(logger)

	// Log detected resource limits for observability
	gomaxprocs := runtime.GOMAXPROCS(0)
	var gomemlimit string
	if limit := debug.SetMemoryLimit(-1); limit != math.MaxInt64 {
		gomemlimit = fmt.Sprintf("%d bytes (%.2f MiB)", limit, float64(limit)/(1024*1024))
	} else {
		gomemlimit = "unlimited"
	}

	logger.Info("HAProxy Template Ingress Controller starting",
		"version", GetVersion(),
		"crd_name", runCRDName,
		"secret", runSecretName,
		"webhook_cert_secret", runWebhookCertSecretName,
		"debug_port", runDebugPort,
		"log_level", logging.GetLevel(),
		"gomaxprocs", gomaxprocs,
		"gomemlimit", gomemlimit)

	// Create Kubernetes client
	k8sClient, err := client.New(client.Config{
		Kubeconfig: runKubeconfig,
	})
	if err != nil {
		return fmt.Errorf("failed to create Kubernetes client: %w", err)
	}

	logger.Info("Kubernetes client created successfully",
		"namespace", k8sClient.Namespace(),
		"in_cluster", runKubeconfig == "")

	// Set up signal handling for graceful shutdown
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer cancel()

	// Run the controller
	if err := controller.Run(ctx, k8sClient, runCRDName, runSecretName, runWebhookCertSecretName, runDebugPort); err != nil {
		// Only return error if it's not a graceful shutdown
		if ctx.Err() == nil {
			return fmt.Errorf("controller failed: %w", err)
		}
	}

	logger.Info("Controller shutdown complete")
	return nil
}
