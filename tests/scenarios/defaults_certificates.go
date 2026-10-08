// Copyright 2026 Philipp Hossner
// SPDX-License-Identifier: Apache-2.0

package scenarios

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	admissionv1 "k8s.io/api/admissionregistration/v1"
	corev1 "k8s.io/api/core/v1"

	"gitlab.com/haproxy-haptic/haptic/tests/testutil"
)

func (s *Session) defaultsCertificates(ctx context.Context) error {
	for kind, pattern := range map[string]string{"issuer": "ssl-selfsigned", "certificate": "default-ssl-cert"} {
		if err := poll(ctx, 20*time.Second, 2*time.Second, kind+" "+pattern, func(ctx context.Context) (testutil.PollResult, error) {
			result, err := s.Kube(ctx, nil, "get", kind, "-o", fieldName)
			if err != nil {
				return testutil.PollPending, err
			}
			if !strings.Contains(result.Stdout, pattern) {
				return testutil.PollPending, fmt.Errorf("%s %s is absent", kind, pattern)
			}
			return testutil.PollSucceeded, nil
		}); err != nil {
			return err
		}
	}
	if err := poll(ctx, 150*time.Second, 5*time.Second, "default SSL certificate Ready", func(ctx context.Context) (testutil.PollResult, error) {
		result, err := s.Kube(ctx, nil, "get", "certificate", "default-ssl-cert", "-o", "jsonpath={.status.conditions[?(@.type==\"Ready\")].status}")
		if err != nil {
			return testutil.PollPending, err
		}
		if result.Stdout != conditionTrue {
			return testutil.PollPending, fmt.Errorf("certificate Ready=%q", result.Stdout)
		}
		return testutil.PollSucceeded, nil
	}); err != nil {
		return err
	}
	for _, name := range []string{"default-ssl-cert", s.Release + "-webhook-tls"} {
		secret, err := readJSON[corev1.Secret](ctx, s, "get", "secret", name)
		if err != nil {
			return err
		}
		if secret.Type != corev1.SecretTypeTLS {
			return fmt.Errorf("secret %s has type %s, expected TLS", name, secret.Type)
		}
		if name == s.Release+"-webhook-tls" && len(secret.Data["ca.crt"]) == 0 {
			return fmt.Errorf("secret %s has no ca.crt", name)
		}
	}
	webhook, err := readJSON[admissionv1.ValidatingWebhookConfiguration](ctx, s, "get", "validatingwebhookconfiguration", s.Release+"-"+s.Namespace+"-webhook")
	if err != nil {
		return err
	}
	if len(webhook.Webhooks) == 0 || len(webhook.Webhooks[0].ClientConfig.CABundle) == 0 {
		return errors.New("admission webhook has no CA bundle")
	}
	return nil
}
