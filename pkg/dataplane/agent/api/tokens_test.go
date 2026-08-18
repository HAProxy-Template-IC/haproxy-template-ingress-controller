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

package api_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/agent/api"
)

// TestSafeToken pins the grammar both ends compile: the controller composes no
// op whose tokens fail it, and the agent executes none.
func TestSafeToken(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want bool
	}{
		{name: "a path", in: "maps/route-backend.map", want: true},
		{name: "a host key", in: "a.example.com", want: true},
		{name: "a keyword argument", in: "h2,http/1.1", want: true},
		{name: "empty", in: "", want: false},
		{name: "a space", in: "be a", want: false},
		{name: "a TAB", in: "be\ta", want: false},
		{name: "a command separator", in: "be;shutdown", want: false},
		{name: "a payload introducer", in: "be<<x", want: false},
		{name: "a closing angle bracket", in: "a>b", want: false},
		{name: "a backslash", in: `a\b`, want: false},
		{name: "a newline", in: "a\nb", want: false},
		{name: "a NUL", in: "a\x00b", want: false},
		{name: "a vertical tab", in: "a\vb", want: false},
		{name: "a DEL", in: "a\x7fb", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, api.SafeToken(tt.in))
		})
	}
}

// TestSafePayloadValue pins the weaker rule a payload block needs: only the
// line framing is significant there.
func TestSafePayloadValue(t *testing.T) {
	assert.True(t, api.SafePayloadValue(""))
	assert.True(t, api.SafePayloadValue("301|https|example.com; x"))
	assert.True(t, api.SafePayloadValue("a>b"))
	assert.False(t, api.SafePayloadValue("a\nb"))
	assert.False(t, api.SafePayloadValue("a\rb"))
	assert.False(t, api.SafePayloadValue("a\x00b"))
}
