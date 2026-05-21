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

//go:build e2e

package httpclient

import (
	"bytes"
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"gitlab.com/haproxy-haptic/haptic/tests/testutil"
)

// Request is a fluent HTTP request builder. Build a Request with one of the
// Client GET/HTTPS factory methods, chain With* mutators, then terminate
// with one of the Expect* sinks (which retry under the client's wait budget)
// or Do (which makes a single request).
type Request struct {
	client *Client

	scheme string // "http" or "https"
	host   string
	path   string

	method  string
	body    []byte
	headers http.Header

	basicAuth *basicAuth
	mtls      *mtlsConfig
}

type basicAuth struct {
	user, password string
}

type mtlsConfig struct {
	cert tls.Certificate
	ca   []byte
}

// Response is the result of a single HTTP exchange. Tests typically use the
// Expect* sinks instead, but Do returns Response for assertions that need
// custom logic.
type Response struct {
	// Status is the HTTP status code.
	Status int

	// Header is the response headers, normalized via http.Header.
	Header http.Header

	// Body is the raw response body bytes.
	Body []byte

	// Echo is the parsed echo-server JSON, or nil if the body wasn't
	// recognizable echo-server output.
	Echo *EchoBody
}

// GET builds an HTTP GET against http://<nodeIP>:<httpPort>/<path> with
// Host: <host>.
func (c *Client) GET(host, path string) *Request {
	return &Request{
		client:  c,
		scheme:  "http",
		host:    host,
		path:    normalizePath(path),
		method:  http.MethodGet,
		headers: http.Header{},
	}
}

// HTTPS builds an HTTPS GET against https://<host>/<path>. The shared
// transport's DialContext rewrites :443 to the kind HTTPS NodePort, so the
// TLS SNI carries the original host. Equivalent to:
//
//	curl --resolve <host>:443:<nodeIP>:<httpsPort> https://<host><path>
func (c *Client) HTTPS(host, path string) *Request {
	return &Request{
		client:  c,
		scheme:  "https",
		host:    host,
		path:    normalizePath(path),
		method:  http.MethodGet,
		headers: http.Header{},
	}
}

// WithMethod overrides the HTTP method (default GET).
func (r *Request) WithMethod(method string) *Request {
	r.method = method
	return r
}

// WithHeader adds a header. Subsequent calls with the same name append.
func (r *Request) WithHeader(name, value string) *Request {
	r.headers.Add(name, value)
	return r
}

// WithBody sets the request body.
func (r *Request) WithBody(body []byte) *Request {
	r.body = body
	return r
}

// WithBasicAuth attaches HTTP Basic credentials.
func (r *Request) WithBasicAuth(user, password string) *Request {
	r.basicAuth = &basicAuth{user, password}
	return r
}

// WithClientCert configures mTLS for HTTPS requests. The CA is used to verify
// the server certificate (replacing the default insecure-skip-verify). Returns
// the request unchanged if cert parsing fails; the failure surfaces from Do.
func (r *Request) WithClientCert(certPEM, keyPEM, caPEM []byte) *Request {
	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		// Stash the error so Do/Expect* surfaces it.
		r.mtls = &mtlsConfig{ca: caPEM}
		// Use an obviously invalid cert; Do will refuse to send.
		_ = cert
		return r
	}
	r.mtls = &mtlsConfig{cert: cert, ca: caPEM}
	return r
}

// Do executes the request once and returns the response. Tests typically use
// the Expect* sinks instead; Do is the escape hatch for assertions that need
// to inspect Response directly (e.g., counting backend hits across requests).
func (r *Request) Do(ctx context.Context) (*Response, error) {
	url := r.url()
	var bodyReader io.Reader
	if len(r.body) > 0 {
		bodyReader = bytes.NewReader(r.body)
	}
	req, err := http.NewRequestWithContext(ctx, r.method, url, bodyReader)
	if err != nil {
		return nil, fmt.Errorf("new request: %w", err)
	}
	req.Host = r.host
	for k, vs := range r.headers {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}
	if r.basicAuth != nil {
		req.SetBasicAuth(r.basicAuth.user, r.basicAuth.password)
	}

	transport := r.client.transport
	if r.mtls != nil {
		t, err := transportForClientCert(r.client.nodeIP, r.client.httpsPort, r.mtls.cert, r.mtls.ca)
		if err != nil {
			return nil, fmt.Errorf("build mTLS transport: %w", err)
		}
		transport = t
	}

	httpClient := &http.Client{
		Transport: transport,
		// Don't follow redirects automatically — tests assert on Location.
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
		Timeout: 10 * time.Second,
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}

	return &Response{
		Status: resp.StatusCode,
		Header: resp.Header.Clone(),
		Body:   body,
		Echo:   parseEchoBody(body),
	}, nil
}

// url renders the URL the request will fetch. For HTTP we hit the NodePort
// directly; for HTTPS we use the user-supplied host (the transport's
// DialContext rewrites the wire address to the NodePort).
func (r *Request) url() string {
	switch r.scheme {
	case "https":
		return "https://" + r.host + r.path
	default:
		return "http://" + r.client.nodeIP + ":" + strconv.Itoa(r.client.httpPort) + r.path
	}
}

// normalizePath ensures the path begins with "/".
func normalizePath(path string) string {
	if path == "" {
		return "/"
	}
	if !strings.HasPrefix(path, "/") {
		return "/" + path
	}
	return path
}

// ExpectOK asserts the request returns HTTP 200, retrying under the client's
// wait budget. Failures call t.Fatalf with the last response (or last error)
// for diagnostic context.
func (r *Request) ExpectOK(t *testing.T) *Response {
	t.Helper()
	return r.ExpectStatus(t, http.StatusOK)
}

// ExpectStatus asserts the response status equals code, retrying under the
// client's wait budget.
func (r *Request) ExpectStatus(t *testing.T, code int) *Response {
	t.Helper()
	resp, err := r.poll(t, fmt.Sprintf("%s %s -> %d", r.method, r.url(), code), func(resp *Response) bool {
		return resp.Status == code
	})
	if err != nil {
		t.Fatalf("ExpectStatus(%d): %v", code, err)
	}
	return resp
}

// ExpectBodyContains asserts the response body contains substr, retrying
// under the client's wait budget. Status is not checked — pair with
// ExpectStatus if the test needs both.
func (r *Request) ExpectBodyContains(t *testing.T, substr string) *Response {
	t.Helper()
	resp, err := r.poll(t, fmt.Sprintf("%s %s body contains %q", r.method, r.url(), substr), func(resp *Response) bool {
		return strings.Contains(string(resp.Body), substr)
	})
	if err != nil {
		t.Fatalf("ExpectBodyContains(%q): %v", substr, err)
	}
	return resp
}

// ExpectHeader asserts a response header contains the given value
// (substring match, not equality). Retries under the client's wait
// budget.
func (r *Request) ExpectHeader(t *testing.T, name, want string) *Response {
	t.Helper()
	resp, err := r.poll(t, fmt.Sprintf("%s %s header %s contains %q", r.method, r.url(), name, want), func(resp *Response) bool {
		return strings.Contains(resp.Header.Get(name), want)
	})
	if err != nil {
		t.Fatalf("ExpectHeader(%s, %q): %v", name, want, err)
	}
	return resp
}

// ExpectEchoHeader asserts that the upstream backend (echo-server) saw a
// request header `name` with value containing `want`. Used to verify
// HAProxy / SPOA forwarded an auth-derived header through to the backend.
//
// Polling on the echo'd header (rather than just polling on a 200 with
// ExpectOK and then asserting) is the correct shape: the controller can
// land a config that returns 200 from the auth flow before the matching
// `http-request set-header` rules that forward auth-response headers to
// the backend are live, so a request that races ahead can see 200 with
// no forwarded header. Polling closes that window.
func (r *Request) ExpectEchoHeader(t *testing.T, name, want string) *Response {
	t.Helper()
	lower := strings.ToLower(name)
	resp, err := r.poll(t, fmt.Sprintf("%s %s echo'd header %s contains %q", r.method, r.url(), name, want),
		func(resp *Response) bool {
			if resp.Status != http.StatusOK || resp.Echo == nil {
				return false
			}
			return strings.Contains(resp.Echo.Headers[lower], want)
		})
	if err != nil {
		t.Fatalf("ExpectEchoHeader(%s, %q): %v", name, want, err)
	}
	return resp
}

// ExpectMatching asserts the response satisfies a caller-supplied predicate,
// retrying under the client's wait budget. Use this when a single assertion
// depends on multiple response signals being simultaneously consistent (e.g.
// "status=401 AND has WWW-Authenticate header" — polling on either signal
// alone leaves a race window where one rule has landed but the other has
// not). The description is used for diagnostic messages on timeout.
func (r *Request) ExpectMatching(t *testing.T, description string, predicate func(*Response) bool) *Response {
	t.Helper()
	resp, err := r.poll(t, fmt.Sprintf("%s %s: %s", r.method, r.url(), description), predicate)
	if err != nil {
		t.Fatalf("ExpectMatching(%s): %v", description, err)
	}
	return resp
}

// ExpectRedirect asserts the response is a redirect (3xx) with a Location
// header containing want.
func (r *Request) ExpectRedirect(t *testing.T, want string) *Response {
	t.Helper()
	resp, err := r.poll(t, fmt.Sprintf("%s %s redirects to %q", r.method, r.url(), want), func(resp *Response) bool {
		if resp.Status < 300 || resp.Status >= 400 {
			return false
		}
		return strings.Contains(resp.Header.Get("Location"), want)
	})
	if err != nil {
		t.Fatalf("ExpectRedirect(%q): %v", want, err)
	}
	return resp
}

// poll executes the request repeatedly until predicate returns true or the
// client's wait budget is exhausted. The last seen response/error is included
// in any timeout error.
func (r *Request) poll(t *testing.T, description string, predicate func(*Response) bool) (*Response, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), r.client.waitCfg.Timeout)
	defer cancel()

	var lastResp *Response
	var lastErr error

	err := testutil.WaitForConditionWithDescription(ctx, r.client.waitCfg, description,
		func(ctx context.Context) (bool, error) {
			resp, err := r.Do(ctx)
			if err != nil {
				lastErr = err
				return false, err
			}
			lastResp = resp
			lastErr = nil
			if predicate(resp) {
				return true, nil
			}
			return false, fmt.Errorf("status=%d, body=%s", resp.Status, truncate(resp.Body, 200))
		})

	if err == nil {
		return lastResp, nil
	}

	// Timeout. Invoke the configured snapshot callback BEFORE
	// returning the error — at this moment the test's fixtures are
	// still alive (no t.Cleanup has fired), so a snapshot of the
	// chart-rendered HAProxyCfg + on-disk /etc/haproxy reflects the
	// failing state instead of the post-cleanup empty defaults that
	// the standard DumpLogsOnFailure captures. The callback is
	// best-effort; failures inside it must not influence the
	// timeout error we're about to return.
	if r.client.onPollTimeout != nil {
		r.client.onPollTimeout(t, description, lastResp, lastErr)
	}

	// Timeout. Build a diagnostic message with whatever we last saw.
	if lastResp != nil {
		return lastResp, fmt.Errorf("%s: last status=%d, last body=%s, err=%w",
			description, lastResp.Status, truncate(lastResp.Body, 500), err)
	}
	if lastErr != nil {
		return nil, fmt.Errorf("%s: never got a response (last err: %v): %w", description, lastErr, err)
	}
	return nil, fmt.Errorf("%s: %w", description, err)
}

// truncate returns the first n bytes of b as a string with an ellipsis if
// truncation happened. Used to keep error messages readable.
func truncate(b []byte, n int) string {
	if len(b) <= n {
		return string(b)
	}
	return string(b[:n]) + "...(truncated)"
}
