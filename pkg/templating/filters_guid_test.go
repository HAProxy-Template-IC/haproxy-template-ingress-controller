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

package templating

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestScriggoMakeGUID(t *testing.T) {
	tests := []struct {
		name      string
		parts     []interface{}
		wantExact string // if set, exact match
		wantMax   int    // if set, max length check
	}{
		{
			name:      "short frontend GUID",
			parts:     []interface{}{"fe", "status"},
			wantExact: "fe:status",
		},
		{
			name:      "short backend GUID",
			parts:     []interface{}{"be", "default_backend"},
			wantExact: "be:default_backend",
		},
		{
			name:      "short server GUID",
			parts:     []interface{}{"srv", "ing_default_my-svc_80", "SRV_1"},
			wantExact: "srv:ing_default_my-svc_80:SRV_1",
		},
		{
			name:    "long backend GUID truncated",
			parts:   []interface{}{"be", "ing_backend-dev_document-translation-web-gateway-pro-api-document-internal_document-translation-web-gateway-pro_grpc-ingress-extra"},
			wantMax: haproxyGUIDMaxLen,
		},
		{
			name:    "long server GUID truncated",
			parts:   []interface{}{"srv", "ing_backend-dev_document-translation-web-gateway-pro-api-document-internal_document-translation-web-gateway-pro_grpc-ingress", "SRV_10"},
			wantMax: haproxyGUIDMaxLen,
		},
		{
			name:      "exactly 127 chars not truncated",
			parts:     []interface{}{"be", strings.Repeat("a", 124)},
			wantExact: "be:" + strings.Repeat("a", 124),
		},
		{
			name:    "128 chars truncated",
			parts:   []interface{}{"be", strings.Repeat("a", 125)},
			wantMax: haproxyGUIDMaxLen,
		},
		{
			name:      "integer part converted to string",
			parts:     []interface{}{"srv", "mybackend", 42},
			wantExact: "srv:mybackend:42",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := scriggoMakeGUID(tt.parts...)

			if tt.wantExact != "" {
				assert.Equal(t, tt.wantExact, result)
			}

			if tt.wantMax > 0 {
				require.LessOrEqual(t, len(result), tt.wantMax,
					"GUID length %d exceeds max %d: %s", len(result), tt.wantMax, result)
				assert.Regexp(t, `\.[0-9a-f]{8}$`, result, "truncated GUID should end with .hash8")
			}
		})
	}
}

func TestScriggoMakeGUID_Uniqueness(t *testing.T) {
	// Two different long names should produce different truncated GUIDs
	guid1 := scriggoMakeGUID("srv", strings.Repeat("a", 120)+"_different1", "SRV_1")
	guid2 := scriggoMakeGUID("srv", strings.Repeat("a", 120)+"_different2", "SRV_1")

	assert.NotEqual(t, guid1, guid2, "different inputs must produce different GUIDs")
	assert.LessOrEqual(t, len(guid1), haproxyGUIDMaxLen)
	assert.LessOrEqual(t, len(guid2), haproxyGUIDMaxLen)
}

func TestScriggoMakeGUID_Deterministic(t *testing.T) {
	parts := []interface{}{"srv", strings.Repeat("x", 150), "SRV_5"}
	guid1 := scriggoMakeGUID(parts...)
	guid2 := scriggoMakeGUID(parts...)

	assert.Equal(t, guid1, guid2, "same inputs must produce same GUID")
}

func TestScriggoMakeGUID_ValidCharacters(t *testing.T) {
	// HAProxy GUIDs only allow: alphanumeric, '.', ':', '-', '_'
	guid := scriggoMakeGUID("srv", strings.Repeat("a", 150), "SRV_1")
	assert.Regexp(t, `^[a-zA-Z0-9.:_-]+$`, guid,
		"truncated GUID must only contain valid HAProxy GUID characters")
}
