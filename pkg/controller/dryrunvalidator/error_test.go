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

package dryrunvalidator

import (
	"errors"
	"testing"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane"

	"github.com/stretchr/testify/assert"
)

func TestSimplifyValidationError(t *testing.T) {
	tests := []struct {
		name     string
		input    error
		expected string
	}{
		{
			name:     "nil error",
			input:    nil,
			expected: "",
		},
		{
			name:     "unknown error type",
			input:    errors.New("some other error"),
			expected: "some other error",
		},
		{
			name: "schema validation error with value",
			input: errors.New(`schema validation failed: configuration violates API schema constraints: API schema validation failed:
  - ing_echo_echo-server-auth_echo-server_80.0, http-request rule 0: schema validation failed: Error at "/auth_realm": string doesn't match the regular expression "^[^\s]+$"
Schema:
  {
    "pattern": "^[^\\s]+$",
    "type": "string"
  }

Value:
  "\"Echo-Server Protected\""`),
			expected: `auth_realm string doesn't match the regular expression "^[^\s]+$" (got \"Echo-Server Protected\")`,
		},
		{
			name: "schema validation error without value",
			input: errors.New(`schema validation failed: Error at "/max_connections": value must be between 1 and 1000
Schema:
  {
    "minimum": 1,
    "maximum": 1000
  }`),
			expected: `max_connections value must be between 1 and 1000`,
		},
		{
			name: "semantic validation error with context",
			input: errors.New(`semantic validation failed: configuration has semantic errors: haproxy validation failed:   userlist auth_users
      user admin password ...
→ [ALERT] (001) : parsing [haproxy.cfg:15] : unknown user 'missing' in userlist 'auth_users' (declared at haproxy.cfg:12)
  backend api
      server s1 127.0.0.1:8080`),
			expected: `  userlist auth_users
      user admin password ...
→ [ALERT] (001) : parsing [haproxy.cfg:15] : unknown user 'missing' in userlist 'auth_users' (declared at haproxy.cfg:12)
  backend api
      server s1 127.0.0.1:8080`,
		},
		{
			name: "semantic validation error - backend has no server",
			input: errors.New(`semantic validation failed: configuration has semantic errors: haproxy validation failed:   defaults
      mode http
  backend api
→ [ALERT] (002) : parsing [haproxy.cfg:15] : backend 'api' has no server
      balance roundrobin`),
			expected: `  defaults
      mode http
  backend api
→ [ALERT] (002) : parsing [haproxy.cfg:15] : backend 'api' has no server
      balance roundrobin`,
		},
		{
			name:     "semantic validation error without context",
			input:    errors.New(`semantic validation failed: configuration has semantic errors: haproxy validation failed: unknown keyword in global section`),
			expected: `unknown keyword in global section`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := dataplane.SimplifyValidationError(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}
