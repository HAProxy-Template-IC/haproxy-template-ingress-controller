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
	"cmp"
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
	"time"

	"github.com/KimMachineGun/automemlimit/memlimit"
	"github.com/spf13/cobra"
	"k8s.io/klog/v2"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller"
	controllerwebhook "gitlab.com/haproxy-haptic/haptic/pkg/controller/webhook"
	"gitlab.com/haproxy-haptic/haptic/pkg/core/logging"
	"gitlab.com/haproxy-haptic/haptic/pkg/k8s/client"
)

var (
	runCRDName                         string
	runSecretName                      string
	runWebhookCertDir                  string
	runWebhookResourceAdmissionTimeout time.Duration
	runWebhookConfigAdmissionTimeout   time.Duration
	runKubeconfig                      string
	runDebugPort                       int
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
	runCmd.Flags().StringVar(&runWebhookCertDir, "webhook-cert-dir", "",
		"Directory holding the webhook TLS cert (tls.crt/tls.key); read per-handshake so a rotated cert is served without restart. Empty disables the webhook (env: WEBHOOK_CERT_DIR)")
	runCmd.Flags().DurationVar(&runWebhookResourceAdmissionTimeout, "webhook-resource-admission-timeout", 0,
		"Controller-side deadline for watched-resource admission; must be shorter than the ValidatingWebhookConfiguration timeout (env: WEBHOOK_RESOURCE_ADMISSION_TIMEOUT, default: 9s)")
	runCmd.Flags().DurationVar(&runWebhookConfigAdmissionTimeout, "webhook-config-admission-timeout", 0,
		"Controller-side deadline for HAProxyTemplateConfig admission; must be shorter than the ValidatingWebhookConfiguration timeout (env: WEBHOOK_CONFIG_ADMISSION_TIMEOUT, default: 29s)")
	runCmd.Flags().StringVar(&runKubeconfig, "kubeconfig", "",
		"Path to kubeconfig file (for out-of-cluster development)")
	runCmd.Flags().IntVar(&runDebugPort, "debug-port", 0,
		"Port for debug HTTP server (0 to disable, env: DEBUG_PORT)")
}

func runController(_ *cobra.Command, _ []string) error {
	// Configuration priority: CLI flags > Environment variables > Defaults

	// CRD name
	if runCRDName == "" {
		runCRDName = os.Getenv("CRD_NAME")
	}
	if runCRDName == "" {
		runCRDName = defaultCRDName
	}

	// Secret name
	if runSecretName == "" {
		runSecretName = os.Getenv("SECRET_NAME")
	}
	if runSecretName == "" {
		runSecretName = defaultSecretName
	}

	// Webhook certificate directory (optional - empty means webhooks disabled)
	if runWebhookCertDir == "" {
		runWebhookCertDir = os.Getenv("WEBHOOK_CERT_DIR")
	}
	var err error
	runWebhookResourceAdmissionTimeout, err = resolveDurationOption(
		runWebhookResourceAdmissionTimeout,
		"WEBHOOK_RESOURCE_ADMISSION_TIMEOUT",
		controllerwebhook.DefaultResourceAdmissionTimeout,
	)
	if err != nil {
		return err
	}
	runWebhookConfigAdmissionTimeout, err = resolveDurationOption(
		runWebhookConfigAdmissionTimeout,
		"WEBHOOK_CONFIG_ADMISSION_TIMEOUT",
		controllerwebhook.DefaultConfigAdmissionTimeout,
	)
	if err != nil {
		return err
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
		runDebugPort = defaultDebugPort
	}

	// Set up structured logging with dynamic level support
	// LOG_LEVEL env var: TRACE, DEBUG, INFO (default), WARN, ERROR (case-insensitive)
	// The level can be overridden at runtime via ConfigMap's logging.level field
	logLevelEnv := os.Getenv("LOG_LEVEL")
	logger := logging.NewDynamicLogger(logLevelEnv)
	slog.SetDefault(logger)

	// Route client-go's klog output (leader election, informers) through slog
	// so it shares the same logfmt format and dynamic level as everything else.
	klog.SetSlogLogger(logger)

	// Set GOMEMLIMIT from the cgroup limit. Done here (not via automemlimit's
	// blank-import init) so its "GOMEMLIMIT is updated" line goes through our
	// slog handler instead of the stdlib default. Mirrors automemlimit's
	// default options (FromCgroup provider, 0.9 ratio).
	if _, err := memlimit.SetGoMemLimitWithOpts(memlimit.WithLogger(logger)); err != nil {
		logger.Warn("Failed to set GOMEMLIMIT from cgroup", "error", err)
	}

	// Log detected resource limits for observability.
	// GOGC: report the env override if set, otherwise "default" (Go 1.26's Green Tea
	// GC reduces per-cycle overhead, making manual GOGC tuning unnecessary;
	// automemlimit's GOMEMLIMIT provides the OOM safety net).
	gomaxprocs := runtime.GOMAXPROCS(0)
	gomemlimit := "unlimited"
	if limit := debug.SetMemoryLimit(-1); limit != math.MaxInt64 {
		gomemlimit = fmt.Sprintf("%d bytes (%.2f MiB)", limit, float64(limit)/(1024*1024))
	}

	logger.Info("HAProxy Template Ingress Controller starting",
		"version", version,
		"source_hash", sourceHash,
		"crd_name", runCRDName,
		"secret", runSecretName,
		"webhook_cert_dir", runWebhookCertDir,
		"webhook_resource_admission_timeout", runWebhookResourceAdmissionTimeout,
		"webhook_config_admission_timeout", runWebhookConfigAdmissionTimeout,
		"debug_port", runDebugPort,
		"log_level", logging.GetLevel(),
		"gomaxprocs", gomaxprocs,
		"gomemlimit", gomemlimit,
		"gogc", cmp.Or(os.Getenv("GOGC"), "default"))

	// Configure build info for metrics.
	// HAPROXY_MINOR is set by the haproxytech/haproxy-debian base image and contains
	// the full semver of the bundled HAProxy (e.g. "3.2.11").
	controller.SetBuildInfo(version, os.Getenv("HAPROXY_MINOR"))

	// Create Kubernetes client
	k8sClient, err := client.New(client.Config{
		Kubeconfig: runKubeconfig,
	})
	if err != nil {
		return fmt.Errorf("creating Kubernetes client: %w", err)
	}

	logger.Info("Kubernetes client created successfully",
		"namespace", k8sClient.Namespace(),
		"in_cluster", runKubeconfig == "")

	// Set up signal handling for graceful shutdown
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer cancel()

	// Run the controller
	if err := controller.Run(
		ctx,
		k8sClient,
		runCRDName,
		runSecretName,
		runWebhookCertDir,
		controller.WebhookAdmissionTimeouts{
			Resource:              runWebhookResourceAdmissionTimeout,
			HAProxyTemplateConfig: runWebhookConfigAdmissionTimeout,
		},
		runDebugPort,
	); err != nil {
		// Only return error if it's not a graceful shutdown
		if ctx.Err() == nil {
			return fmt.Errorf("controller failed: %w", err)
		}
	}

	logger.Info("Controller shutdown complete")
	return nil
}

func resolveDurationOption(flagValue time.Duration, envName string, defaultValue time.Duration) (time.Duration, error) {
	if flagValue != 0 {
		if flagValue < 0 {
			return 0, fmt.Errorf("admission timeout %s must be positive", envName)
		}
		if flagValue > controllerwebhook.MaximumAdmissionTimeout {
			return 0, fmt.Errorf("admission timeout %s must not exceed %s", envName, controllerwebhook.MaximumAdmissionTimeout)
		}
		return flagValue, nil
	}

	raw := os.Getenv(envName)
	if raw == "" {
		return defaultValue, nil
	}
	d, err := time.ParseDuration(raw)
	if err != nil {
		return 0, fmt.Errorf("parsing %s=%q as a duration: %w", envName, raw, err)
	}
	if d <= 0 {
		return 0, fmt.Errorf("%s must be positive", envName)
	}
	if d > controllerwebhook.MaximumAdmissionTimeout {
		return 0, fmt.Errorf("%s must not exceed %s", envName, controllerwebhook.MaximumAdmissionTimeout)
	}
	return d, nil
}
