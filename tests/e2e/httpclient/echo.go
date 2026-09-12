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
	"encoding/json"
	"strings"
)

// EchoBody is a typed view of the JSON the echo-server emits.
//
// The dev-env echo-server is jmalloc/echo-server, which serializes the
// incoming request as JSON; tests assert against structured fields rather
// than substring-matching the response body.
//
// Only fields the test suite asserts against are mapped.
type EchoBody struct {
	// Host is the value of the Host header echo-server saw.
	Host string `json:"host"`

	// Method is the HTTP method (GET, POST, ...).
	Method string `json:"method"`

	// Path is the request path as the upstream backend received it. After
	// HAProxy URL rewriting this is the rewritten path, not what the client
	// sent.
	Path string `json:"path"`

	// Headers maps lower-cased header names to their first value. Useful for
	// asserting things like X-Forwarded-For or X-Auth-User without parsing
	// raw HTTP.
	Headers map[string]string `json:"-"`

	// Environment is the value of the ENVIRONMENT env var the echo-server
	// pod was started with. The dev env uses this to distinguish
	// echo-server (no ENVIRONMENT) from echo-server-v2 (ENVIRONMENT=v2)
	// for weighted-routing tests.
	Environment string `json:"-"`

	// PodHostname is the serving pod's hostname — the container HOSTNAME env,
	// which Kubernetes sets to the pod name. Distinct values across requests
	// identify distinct backend pods; Host is the request Host header (constant
	// across requests to one Ingress), so use this to tell pods apart.
	PodHostname string `json:"-"`
}

// parseEchoBody unmarshals an echo-server response. Returns nil if the body
// isn't recognizable JSON (in which case the caller falls back to substring
// matching on Body bytes).
//
// ealen/echo-server emits a nested JSON structure like:
//
//	{
//	  "host":    {"hostname": "...", "ip": "...", "ips": []},
//	  "http":    {"method": "GET", "originalUrl": "/path", "protocol": "http"},
//	  "request": {"headers": {"host": "...", ...}, "query": {}, "body": {}, ...},
//	  "environment": {"PATH": "...", "ENVIRONMENT": "v2", ...}
//	}
//
// We pull the fields we care about out of the nested structure and expose
// them flat on EchoBody so tests stay readable.
func parseEchoBody(body []byte) *EchoBody {
	var raw map[string]any
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil
	}
	// Reject obviously non-echo responses (HAProxy 503/404 error pages,
	// etc.) so callers don't get a non-nil Echo with all fields empty.
	httpBlock, ok := raw["http"].(map[string]any)
	if !ok {
		return nil
	}

	echo := &EchoBody{
		Headers: map[string]string{},
	}

	hostBlock, _ := raw["host"].(map[string]any)
	echo.Host, _ = hostBlock["hostname"].(string)
	echo.Method, _ = httpBlock["method"].(string)
	echo.Path, _ = httpBlock["originalUrl"].(string)
	reqBlock, _ := raw["request"].(map[string]any)
	headers, _ := reqBlock["headers"].(map[string]any)
	for name, value := range headers {
		lower := strings.ToLower(name)
		switch val := value.(type) {
		case string:
			echo.Headers[lower] = val
		case []any:
			if len(val) == 0 {
				continue
			}
			if first, ok := val[0].(string); ok {
				echo.Headers[lower] = first
			}
		}
	}
	environment, _ := raw["environment"].(map[string]any)
	echo.Environment, _ = environment["ENVIRONMENT"].(string)
	echo.PodHostname, _ = environment["HOSTNAME"].(string)

	return echo
}
