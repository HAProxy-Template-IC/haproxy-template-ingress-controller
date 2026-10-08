// Copyright 2026 Philipp Hossner
// SPDX-License-Identifier: Apache-2.0

//go:build e2e

// Package httpclient selects the E2E suite's endpoint and diagnostic callback.
package httpclient

import (
	"crypto/tls"
	"testing"

	"gitlab.com/haproxy-haptic/haptic/tests/e2e/e2ecluster"
	shared "gitlab.com/haproxy-haptic/haptic/tests/httpclient"
)

type Client = shared.Client
type Request = shared.Request
type Response = shared.Response
type EchoBody = shared.EchoBody
type PollTimeoutSnapshot = shared.PollTimeoutSnapshot

var defaultSnapshot PollTimeoutSnapshot

func SetDefaultPollTimeoutSnapshot(snapshot PollTimeoutSnapshot) { defaultSnapshot = snapshot }

func New(t *testing.T) *Client {
	t.Helper()
	endpoint, err := e2ecluster.ResolveTrafficEndpoint()
	if err != nil {
		t.Fatalf("resolve test traffic endpoint: %v", err)
	}
	t.Logf("httpclient: host=%s HTTP=%d HTTPS=%d", endpoint.Host, endpoint.HTTPPort, endpoint.HTTPSPort)
	return forEndpoint(endpoint.Host, endpoint.HTTPPort, endpoint.HTTPSPort)
}

func ForForwarded(t *testing.T, httpPort, httpsPort int) *Client {
	t.Helper()
	return forEndpoint("127.0.0.1", httpPort, httpsPort)
}

func forEndpoint(host string, httpPort, httpsPort int) *Client {
	return shared.New(&shared.Config{Host: host, HTTPPort: httpPort, HTTPSPort: httpsPort, OnPollTimeout: defaultSnapshot, TLS: &tls.Config{
		InsecureSkipVerify: true, // #nosec G402 — E2E fixtures use self-signed certificates; mTLS requests pin their CA.
		MinVersion:         tls.VersionTLS12,
	}})
}
