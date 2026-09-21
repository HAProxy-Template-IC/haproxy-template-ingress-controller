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
	"crypto/x509"
	"errors"
	"fmt"
	"time"

	"github.com/spf13/cobra"
	"k8s.io/client-go/kubernetes"

	"gitlab.com/haproxy-haptic/haptic/pkg/k8s/certificates"
)

type certificateOptions struct {
	kubeconfig   string
	namespace    string
	issuerSecret string
	serverSecret string
	clientSecret string
	serverName   string
	clientName   string
	validityDays int
}

func newCertificatesCommand() *cobra.Command {
	command := &cobra.Command{Use: "certificates", Short: "Manage controller-to-agent TLS identities"}
	options := &certificateOptions{}
	renew := &cobra.Command{
		Use: "renew", Short: "Create or renew the chart-managed CA and both TLS identities",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error { return options.run(cmd) },
	}
	flags := renew.Flags()
	flags.StringVar(&options.kubeconfig, "kubeconfig", "", "Kubeconfig path; otherwise use the current context or in-cluster credentials")
	flags.StringVarP(&options.namespace, "namespace", "n", "", "Namespace containing the managed certificate Secrets")
	flags.StringVar(&options.issuerSecret, "issuer-secret", "", "Secret storing the certificate authority and current generation")
	flags.StringVar(&options.serverSecret, "server-secret", "", "Agent server identity Secret")
	flags.StringVar(&options.clientSecret, "client-secret", "", "Controller client identity Secret")
	flags.StringVar(&options.serverName, "server-name", "", "Exact agent DNS identity")
	flags.StringVar(&options.clientName, "client-name", "", "Exact controller DNS identity")
	flags.IntVar(&options.validityDays, "validity-days", 365, "CA and identity lifetime in days, from 1 to 3650")
	command.AddCommand(renew)
	return command
}

func (o *certificateOptions) run(cmd *cobra.Command) error {
	if o.validityDays < 1 || o.validityDays > 3650 {
		return errors.New("certificate validity must be from 1 to 3650 days")
	}
	if o.serverName == o.clientName {
		return errors.New("agent and controller certificate names must differ")
	}
	config, err := buildRestConfig(o.kubeconfig)
	if err != nil {
		return err
	}
	config.Timeout = 30 * time.Second
	client, err := kubernetes.NewForConfig(config)
	if err != nil {
		return fmt.Errorf("create certificate renewal client: %w", err)
	}
	lifetime := time.Duration(o.validityDays) * 24 * time.Hour
	manager, err := certificates.New(&certificates.Config{
		Namespace: o.namespace, IssuerSecret: o.issuerSecret,
		Targets: []certificates.Target{
			{SecretName: o.serverSecret, DNSName: o.serverName, Usage: x509.ExtKeyUsageServerAuth},
			{SecretName: o.clientSecret, DNSName: o.clientName, Usage: x509.ExtKeyUsageClientAuth},
		},
		Lifetime: lifetime, RenewBefore: min(lifetime/3, 30*24*time.Hour), Overlap: time.Hour,
	}, client.CoreV1())
	if err != nil {
		return err
	}
	result, err := manager.Renew(cmd.Context())
	if err != nil {
		return fmt.Errorf("renew agent identities: %w", err)
	}
	_, err = fmt.Fprintf(cmd.OutOrStdout(), "Agent TLS identities valid until %s (created=%t, renewed=%t)\n",
		result.ExpiresAt.UTC().Format(time.RFC3339), result.Created, result.Renewed)
	return err
}
