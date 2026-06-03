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

package e2e

import (
	"bytes"
	"context"
	"fmt"
	"testing"
	"text/template"

	"sigs.k8s.io/e2e-framework/klient"
)

// gatewayClassName is the GatewayClass the chart creates by default
// (charts/haptic/values.yaml gatewayClass.name). Test fixtures attach
// Gateway resources to this class.
const gatewayClassName = "haptic"

// NewGateway creates a Gateway resource in the test namespace with a
// single HTTP listener on port 80, accepting all routes from the same
// namespace. Returns the Gateway's name.
//
// Uses kubectlApplyStdin rather than a typed client because the suite
// doesn't depend on sigs.k8s.io/gateway-api/apis/v1 (the controller side
// reads these via the dynamic client too — keeps the test deps small).
func NewGateway(ctx context.Context, t *testing.T, namespace, name string) string {
	t.Helper()
	manifest := fmt.Sprintf(`apiVersion: gateway.networking.k8s.io/v1
kind: Gateway
metadata:
  name: %s
  namespace: %s
spec:
  gatewayClassName: %s
  listeners:
    - name: http
      protocol: HTTP
      port: 80
      allowedRoutes:
        namespaces:
          from: Same
`, name, namespace, gatewayClassName)
	if err := kubectlApplyStdin(ctx, []byte(manifest)); err != nil {
		t.Fatalf("create Gateway %s/%s: %v", namespace, name, err)
	}
	return name
}

// HTTPRouteRule describes one rule in an HTTPRoute. Each rule's matchers
// are AND-ed together (e.g., method=GET AND path=/api), and an HTTPRoute
// can carry multiple rules whose match precedence the chart computes the
// same way the upstream gateway-api spec does.
type HTTPRouteRule struct {
	// PathType is "Exact", "PathPrefix", or "RegularExpression". Empty
	// means "no path constraint" (matches any path).
	PathType string
	// Path is the value to match against. Required if PathType is set.
	Path string
	// Method, if set, restricts the match to a specific HTTP method
	// (GET, POST, etc.).
	Method string
	// HeaderMatches map header name → value.Type "Exact" by default;
	// pass HeaderType to switch to RegularExpression.
	HeaderMatches []HTTPRouteHeaderMatch
	// QueryMatches map query parameter name → value.
	QueryMatches []HTTPRouteQueryMatch
	// BackendRefs lists the upstream Services this rule routes to,
	// optionally with weights for traffic splitting.
	BackendRefs []HTTPRouteBackendRef
}

// HTTPRouteHeaderMatch describes one header matcher.
type HTTPRouteHeaderMatch struct {
	Name string
	// Type is "Exact" (default) or "RegularExpression".
	Type  string
	Value string
}

// HTTPRouteQueryMatch describes one query-param matcher.
type HTTPRouteQueryMatch struct {
	Name string
	// Type is "Exact" (default) or "RegularExpression".
	Type  string
	Value string
}

// HTTPRouteBackendRef describes one backend service. Weight is optional
// (HTTPRoute defaults to 1 when unspecified). For traffic splitting, set
// Weight on each ref so the relative ratios match what the test expects.
type HTTPRouteBackendRef struct {
	Service string
	Port    int32
	Weight  int32
}

// HTTPRouteSpec captures everything NewHTTPRoute needs to render a
// gateway.networking.k8s.io/v1 HTTPRoute manifest.
type HTTPRouteSpec struct {
	// Name is the HTTPRoute resource name.
	Name string
	// GatewayName is the Gateway in the same namespace this route attaches to.
	GatewayName string
	// Hostnames the route matches against (`spec.hostnames`).
	Hostnames []string
	// Rules in match-precedence order. The chart's library computes
	// effective precedence the same way upstream does — most specific
	// wins regardless of source order, but test rules in spec order are
	// easier to read.
	Rules []HTTPRouteRule
}

// NewHTTPRoute renders a HTTPRoute manifest from spec and applies it.
// Returns the route's name.
func NewHTTPRoute(ctx context.Context, t *testing.T, namespace string, spec HTTPRouteSpec) string {
	t.Helper()

	const tmpl = `apiVersion: gateway.networking.k8s.io/v1
kind: HTTPRoute
metadata:
  name: {{ .Name }}
  namespace: {{ .Namespace }}
spec:
  parentRefs:
    - name: {{ .GatewayName }}
{{- if .Hostnames }}
  hostnames:
{{- range .Hostnames }}
    - "{{ . }}"
{{- end }}
{{- end }}
  rules:
{{- range .Rules }}
    -
{{- if or .PathType .Method .HeaderMatches .QueryMatches }}
      matches:
        -
{{- if .PathType }}
          path:
            type: {{ .PathType }}
            value: "{{ .Path }}"
{{- end }}
{{- if .Method }}
          method: {{ .Method }}
{{- end }}
{{- if .HeaderMatches }}
          headers:
{{- range .HeaderMatches }}
            - name: "{{ .Name }}"
{{- if .Type }}
              type: {{ .Type }}
{{- end }}
              value: "{{ .Value }}"
{{- end }}
{{- end }}
{{- if .QueryMatches }}
          queryParams:
{{- range .QueryMatches }}
            - name: "{{ .Name }}"
{{- if .Type }}
              type: {{ .Type }}
{{- end }}
              value: "{{ .Value }}"
{{- end }}
{{- end }}
{{- end }}
      backendRefs:
{{- range .BackendRefs }}
        - name: {{ .Service }}
          port: {{ .Port }}
{{- if .Weight }}
          weight: {{ .Weight }}
{{- end }}
{{- end }}
{{- end }}
`

	tpl, err := template.New("httproute").Parse(tmpl)
	if err != nil {
		t.Fatalf("parse HTTPRoute template: %v", err)
	}
	var buf bytes.Buffer
	data := struct {
		Namespace string
		HTTPRouteSpec
	}{Namespace: namespace, HTTPRouteSpec: spec}
	if err := tpl.Execute(&buf, data); err != nil {
		t.Fatalf("render HTTPRoute manifest: %v", err)
	}
	if err := kubectlApplyStdin(ctx, buf.Bytes()); err != nil {
		t.Fatalf("create HTTPRoute %s/%s: %v\nmanifest:\n%s", namespace, spec.Name, err, buf.String())
	}
	return spec.Name
}

// NewHTTPSGateway creates a Gateway with an HTTPS listener on port 443
// in TLS Terminate mode, referencing the given TLS Secret. The Secret
// must already exist in the same namespace (use NewTLSSecret to make one).
func NewHTTPSGateway(ctx context.Context, t *testing.T, namespace, name, tlsSecretName string) string {
	t.Helper()
	manifest := fmt.Sprintf(`apiVersion: gateway.networking.k8s.io/v1
kind: Gateway
metadata:
  name: %s
  namespace: %s
spec:
  gatewayClassName: %s
  listeners:
    - name: https
      protocol: HTTPS
      port: 443
      tls:
        mode: Terminate
        certificateRefs:
          - kind: Secret
            name: %s
      allowedRoutes:
        namespaces:
          from: Same
`, name, namespace, gatewayClassName, tlsSecretName)
	if err := kubectlApplyStdin(ctx, []byte(manifest)); err != nil {
		t.Fatalf("create HTTPS Gateway %s/%s: %v", namespace, name, err)
	}
	return name
}

// NewEchoServerV2Backend deploys a second echo-server backend tagged
// with ENVIRONMENT=v2. This lets tests that exercise traffic splitting
// or matcher-based routing (HTTPRoute methods, headers, query params)
// distinguish "v2" from the default backend by checking
// resp.Echo.Environment.
//
// Returns the BackendRef (Service+Port). Service name is "echo-server-v2".
func NewEchoServerV2Backend(ctx context.Context, t *testing.T, client klient.Client, namespace string) BackendRef {
	t.Helper()

	manifest := fmt.Sprintf(`apiVersion: apps/v1
kind: Deployment
metadata:
  name: echo-server-v2
  namespace: %s
  labels:
    app: echo-server-v2
spec:
  replicas: 1
  selector:
    matchLabels:
      app: echo-server-v2
  template:
    metadata:
      labels:
        app: echo-server-v2
    spec:
      containers:
        - name: server
          image: %s
          imagePullPolicy: IfNotPresent
          env:
            - name: ENVIRONMENT
              value: "v2"
          ports:
            - name: http
              containerPort: 80
              protocol: TCP
          # Without a readiness probe, K8s marks the pod Ready the instant the
          # container process starts — before the echo server calls listen() —
          # so the EndpointSlice flips ready=true (and waitForServiceEndpointReady
          # returns) while the kernel still RSTs incoming SYNs. HAProxy then
          # dispatches to a not-yet-listening pod and the warmup's
          # 5-consecutive-200 streak breaks (transient SC-- 503). Mirrors the
          # readiness probe NewEchoServerBackend gained in MR !1019.
          readinessProbe:
            httpGet:
              path: /
              port: http
            periodSeconds: 1
            successThreshold: 1
            failureThreshold: 1
            timeoutSeconds: 1
---
apiVersion: v1
kind: Service
metadata:
  name: echo-server-v2
  namespace: %s
  labels:
    app: echo-server-v2
spec:
  selector:
    app: echo-server-v2
  ports:
    - name: http
      port: 80
      targetPort: http
      protocol: TCP
`, namespace, echoServerImage, namespace)

	if err := kubectlApplyStdin(ctx, []byte(manifest)); err != nil {
		t.Fatalf("create echo-server-v2: %v", err)
	}
	if err := waitForServiceEndpointReady(ctx, client, namespace, "echo-server-v2"); err != nil {
		t.Fatalf("echo-server-v2 endpoint not ready: %v", err)
	}
	return BackendRef{Service: "echo-server-v2", Port: 80}
}

// grpcEchoImage is the upstream Gateway-API echo-basic image; with the
// `GRPC_ECHO_SERVER=1` env var it boots the gRPC echo server on port
// 3000. We use the same image the conformance suite uses so behaviour
// matches the upstream gRPC test surface byte-for-byte.
const grpcEchoImage = "gcr.io/k8s-staging-gateway-api/echo-basic:v20260204-monthly-2026.01-60-g28382302"

// NewGRPCEchoBackend deploys the Gateway-API echo-basic image in
// gRPC-server mode (GRPC_ECHO_SERVER=1) plus a Service exposing port
// 9090 that targets the container's gRPC port (3000). Service port
// declares `appProtocol: kubernetes.io/h2` so the chart annotates the
// backend correctly. Returns the BackendRef the caller passes to
// NewGRPCRoute.
//
// Cleaned up automatically when the test namespace is deleted.
func NewGRPCEchoBackend(ctx context.Context, t *testing.T, client klient.Client, namespace string) BackendRef {
	t.Helper()
	const (
		name = "grpc-echo"
		port = int32(9090)
	)

	manifest := fmt.Sprintf(`apiVersion: apps/v1
kind: Deployment
metadata:
  name: %s
  namespace: %s
  labels:
    app: %s
spec:
  replicas: 1
  selector:
    matchLabels:
      app: %s
  template:
    metadata:
      labels:
        app: %s
    spec:
      containers:
        - name: server
          image: %s
          imagePullPolicy: IfNotPresent
          env:
            - name: GRPC_ECHO_SERVER
              value: "1"
            - name: POD_NAME
              valueFrom:
                fieldRef:
                  fieldPath: metadata.name
            - name: NAMESPACE
              valueFrom:
                fieldRef:
                  fieldPath: metadata.namespace
          ports:
            - name: grpc
              containerPort: 3000
              protocol: TCP
---
apiVersion: v1
kind: Service
metadata:
  name: %s
  namespace: %s
  labels:
    app: %s
spec:
  selector:
    app: %s
  ports:
    - name: grpc
      port: %d
      targetPort: grpc
      protocol: TCP
      appProtocol: kubernetes.io/h2
`, name, namespace, name, name, name, grpcEchoImage,
		name, namespace, name, name, port)

	if err := kubectlApplyStdin(ctx, []byte(manifest)); err != nil {
		t.Fatalf("create grpc-echo backend: %v", err)
	}
	if err := waitForServiceEndpointReady(ctx, client, namespace, name); err != nil {
		t.Fatalf("grpc-echo endpoint not ready: %v", err)
	}
	return BackendRef{Service: name, Port: port}
}

// GRPCRouteSpec captures everything NewGRPCRoute needs to render a
// gateway.networking.k8s.io/v1 GRPCRoute manifest. Mirrors the shape of
// HTTPRouteSpec.
type GRPCRouteSpec struct {
	Name        string
	GatewayName string
	Hostnames   []string
	Rules       []GRPCRouteRule
}

// GRPCRouteRule describes one rule in a GRPCRoute.
type GRPCRouteRule struct {
	// MethodService matches the gRPC service name (e.g.
	// "gateway_api_conformance.echo_basic.grpcecho.GrpcEcho"). Empty
	// means "no service constraint".
	MethodService string
	// MethodName matches the gRPC method (e.g. "Echo"). Empty means
	// "no method constraint".
	MethodName string
	// HeaderMatches map header name → value, exact match.
	HeaderMatches []HTTPRouteHeaderMatch
	// BackendRefs lists the upstream Services this rule routes to.
	BackendRefs []HTTPRouteBackendRef
}

// NewGRPCRoute renders a GRPCRoute manifest from spec and applies it.
// Returns the route's name.
func NewGRPCRoute(ctx context.Context, t *testing.T, namespace string, spec GRPCRouteSpec) string {
	t.Helper()

	const tmpl = `apiVersion: gateway.networking.k8s.io/v1
kind: GRPCRoute
metadata:
  name: {{ .Name }}
  namespace: {{ .Namespace }}
spec:
  parentRefs:
    - name: {{ .GatewayName }}
{{- if .Hostnames }}
  hostnames:
{{- range .Hostnames }}
    - "{{ . }}"
{{- end }}
{{- end }}
  rules:
{{- range .Rules }}
    -
{{- if or .MethodService .MethodName .HeaderMatches }}
      matches:
        -
{{- if or .MethodService .MethodName }}
          method:
            type: Exact
{{- if .MethodService }}
            service: "{{ .MethodService }}"
{{- end }}
{{- if .MethodName }}
            method: "{{ .MethodName }}"
{{- end }}
{{- end }}
{{- if .HeaderMatches }}
          headers:
{{- range .HeaderMatches }}
            - name: "{{ .Name }}"
{{- if .Type }}
              type: {{ .Type }}
{{- end }}
              value: "{{ .Value }}"
{{- end }}
{{- end }}
{{- end }}
      backendRefs:
{{- range .BackendRefs }}
        - name: {{ .Service }}
          port: {{ .Port }}
{{- if .Weight }}
          weight: {{ .Weight }}
{{- end }}
{{- end }}
{{- end }}
`

	tpl, err := template.New("grpcroute").Parse(tmpl)
	if err != nil {
		t.Fatalf("parse GRPCRoute template: %v", err)
	}
	var buf bytes.Buffer
	data := struct {
		Namespace string
		GRPCRouteSpec
	}{Namespace: namespace, GRPCRouteSpec: spec}
	if err := tpl.Execute(&buf, data); err != nil {
		t.Fatalf("render GRPCRoute manifest: %v", err)
	}
	if err := kubectlApplyStdin(ctx, buf.Bytes()); err != nil {
		t.Fatalf("create GRPCRoute %s/%s: %v\nmanifest:\n%s", namespace, spec.Name, err, buf.String())
	}
	return spec.Name
}
