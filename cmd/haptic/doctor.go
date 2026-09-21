// Copyright 2026 Philipp Hossner
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
	"time"

	"github.com/spf13/cobra"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/diagnostics"
	"gitlab.com/haproxy-haptic/haptic/pkg/k8s/podclient"
)

type doctorOptions struct {
	collection diagnostics.Options
	kubeconfig string
	output     string
	bundle     string
	timeout    time.Duration
}

func newDoctorCommand() *cobra.Command {
	options := &doctorOptions{}
	command := &cobra.Command{
		Use: "doctor", Short: "Check a live HAPTIC fleet and collect a report without configuration contents",
		Args: cobra.NoArgs, SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error { return options.run(cmd) },
	}
	flags := command.Flags()
	flags.StringVar(&options.kubeconfig, "kubeconfig", "", "Kubeconfig path; defaults to the current context or in-cluster credentials")
	flags.StringVarP(&options.collection.Namespace, "namespace", "n", "", "HAPTIC namespace; defaults to the kubeconfig context or service account namespace")
	flags.StringVar(&options.collection.Release, "release", "haptic", "Helm release name used to select controller and HAProxy pods")
	flags.StringVar(&options.collection.ConfigName, "crd-name", "haptic-config", "HAProxyTemplateConfig name (controller.configName in Helm)")
	flags.IntVar(&options.collection.DebugPort, "debug-port", 0, "Controller debug port; zero discovers the healthz container port")
	flags.IntVar(&options.collection.MaxResources, "max-resources", 10000, "Maximum watched resources to inspect across all watches")
	flags.Int64Var(&options.collection.MaxResponseBytes, "max-response-bytes", 8<<20, "Maximum bytes per controller or agent response")
	flags.DurationVar(&options.timeout, "timeout", 2*time.Minute, "Overall collection deadline")
	flags.StringVarP(&options.output, "output", "o", "text", "Report format: text or json")
	flags.StringVar(&options.bundle, "bundle", "", "Create a private ZIP bundle at this new path, including when unhealthy")
	return command
}

func (o *doctorOptions) run(command *cobra.Command) error {
	if o.output != "text" && o.output != "json" {
		return errors.New("--output must be text or json")
	}
	if o.timeout <= 0 {
		return errors.New("--timeout must be positive")
	}
	config, err := buildRestConfig(o.kubeconfig)
	if err != nil {
		return err
	}
	config.Timeout = min(o.timeout, 30*time.Second)
	client, err := kubernetes.NewForConfig(config)
	if err != nil {
		return fmt.Errorf("create diagnostic Kubernetes client: %w", err)
	}
	dyn, err := dynamic.NewForConfig(config)
	if err != nil {
		return fmt.Errorf("create diagnostic resource client: %w", err)
	}
	if o.collection.Namespace == "" {
		rules := clientcmd.NewDefaultClientConfigLoadingRules()
		rules.ExplicitPath = o.kubeconfig
		namespace, _, err := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(rules, &clientcmd.ConfigOverrides{}).Namespace()
		if err != nil {
			return fmt.Errorf("discover namespace: %w", err)
		}
		o.collection.Namespace = namespace
	}
	collector, err := diagnostics.New(&o.collection, client, dyn, func(port int) diagnostics.PodAccess {
		return podclient.New(config, client, o.collection.Namespace, "", port)
	})
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(command.Context(), o.timeout)
	defer cancel()
	report := collector.Collect(ctx)
	return o.writeReport(command, report)
}

func (o *doctorOptions) writeReport(command *cobra.Command, report *diagnostics.Report) error {
	write := diagnostics.WriteText
	if o.output == "json" {
		write = diagnostics.WriteJSON
	}
	if err := write(command.OutOrStdout(), report); err != nil {
		return fmt.Errorf("write diagnostic report: %w", err)
	}
	if o.bundle != "" {
		if err := diagnostics.WriteBundle(o.bundle, report); err != nil {
			return err
		}
		if _, err := fmt.Fprintf(command.ErrOrStderr(), "Diagnostic bundle: %s\n", o.bundle); err != nil {
			return err
		}
	}
	if !report.Complete {
		return errors.New("diagnostic collection is incomplete; follow the report's findings")
	}
	if !report.Healthy {
		return errors.New("HAPTIC health checks failed; follow the report's findings")
	}
	return nil
}
