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

package httpstore

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"haptic/pkg/controller/testutil"
	"haptic/pkg/httpstore"
)

func TestNewHTTPStoreWrapper(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()
	component := New(bus, logger, 0)
	ctx := context.Background()

	wrapper := NewHTTPStoreWrapper(ctx, component, logger, true)

	require.NotNil(t, wrapper)
	assert.Equal(t, component, wrapper.component)
	assert.True(t, wrapper.isValidation)
	assert.Equal(t, ctx, wrapper.ctx)
}

func TestParseArgs_NoArgs(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()
	component := New(bus, logger, 0)
	wrapper := NewHTTPStoreWrapper(context.Background(), component, logger, false)

	_, err := wrapper.Fetch()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "requires at least 1 argument")
}

func TestParseArgs_InvalidURL(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()
	component := New(bus, logger, 0)
	wrapper := NewHTTPStoreWrapper(context.Background(), component, logger, false)

	_, err := wrapper.Fetch(12345) // Not a string
	require.Error(t, err)
	assert.Contains(t, err.Error(), "url must be a string")
}

func TestParseArgs_InvalidOptions(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()
	component := New(bus, logger, 0)
	wrapper := NewHTTPStoreWrapper(context.Background(), component, logger, false)

	_, err := wrapper.Fetch("http://example.com", "not-a-map")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "options must be a map")
}

func TestParseArgs_InvalidAuth(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()
	component := New(bus, logger, 0)
	wrapper := NewHTTPStoreWrapper(context.Background(), component, logger, false)

	_, err := wrapper.Fetch("http://example.com", nil, "not-a-map")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "auth must be a map")
}

func TestParseFetchOptions(t *testing.T) {
	tests := []struct {
		name    string
		input   map[string]interface{}
		want    httpstore.FetchOptions
		wantErr bool
	}{
		{
			name:  "empty options",
			input: map[string]interface{}{},
			want:  httpstore.FetchOptions{},
		},
		{
			name: "delay string",
			input: map[string]interface{}{
				"delay": "5m",
			},
			want: httpstore.FetchOptions{Delay: 5 * time.Minute},
		},
		{
			name: "timeout string",
			input: map[string]interface{}{
				"timeout": "30s",
			},
			want: httpstore.FetchOptions{Timeout: 30 * time.Second},
		},
		{
			name: "retries int",
			input: map[string]interface{}{
				"retries": 3,
			},
			want: httpstore.FetchOptions{Retries: 3},
		},
		{
			name: "retries int64",
			input: map[string]interface{}{
				"retries": int64(5),
			},
			want: httpstore.FetchOptions{Retries: 5},
		},
		{
			name: "retries float64",
			input: map[string]interface{}{
				"retries": float64(2),
			},
			want: httpstore.FetchOptions{Retries: 2},
		},
		{
			name: "critical bool",
			input: map[string]interface{}{
				"critical": true,
			},
			want: httpstore.FetchOptions{Critical: true},
		},
		{
			name: "all options",
			input: map[string]interface{}{
				"delay":    "1h",
				"timeout":  "60s",
				"retries":  5,
				"critical": true,
			},
			want: httpstore.FetchOptions{
				Delay:    1 * time.Hour,
				Timeout:  60 * time.Second,
				Retries:  5,
				Critical: true,
			},
		},
		{
			name: "invalid delay",
			input: map[string]interface{}{
				"delay": "invalid",
			},
			wantErr: true,
		},
		{
			name: "invalid timeout",
			input: map[string]interface{}{
				"timeout": "invalid",
			},
			wantErr: true,
		},
		{
			name: "invalid retries type",
			input: map[string]interface{}{
				"retries": "not-a-number",
			},
			wantErr: true,
		},
		{
			name: "invalid critical type",
			input: map[string]interface{}{
				"critical": "not-a-bool",
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseFetchOptions(tt.input)

			if tt.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.want, got)
			}
		})
	}
}

func TestParseAuthConfig(t *testing.T) {
	tests := []struct {
		name    string
		input   map[string]interface{}
		want    *httpstore.AuthConfig
		wantErr bool
	}{
		{
			name:  "empty auth",
			input: map[string]interface{}{},
			want:  &httpstore.AuthConfig{},
		},
		{
			name: "bearer auth",
			input: map[string]interface{}{
				"type":  "bearer",
				"token": "my-token",
			},
			want: &httpstore.AuthConfig{
				Type:  "bearer",
				Token: "my-token",
			},
		},
		{
			name: "basic auth",
			input: map[string]interface{}{
				"type":     "basic",
				"username": "user",
				"password": "pass",
			},
			want: &httpstore.AuthConfig{
				Type:     "basic",
				Username: "user",
				Password: "pass",
			},
		},
		{
			name: "headers auth",
			input: map[string]interface{}{
				"type": "header",
				"headers": map[string]interface{}{
					"X-Api-Key": "api-key-value",
				},
			},
			want: &httpstore.AuthConfig{
				Type: "header",
				Headers: map[string]string{
					"X-Api-Key": "api-key-value",
				},
			},
		},
		{
			name: "invalid type",
			input: map[string]interface{}{
				"type": 12345,
			},
			wantErr: true,
		},
		{
			name: "invalid username",
			input: map[string]interface{}{
				"username": 12345,
			},
			wantErr: true,
		},
		{
			name: "invalid password",
			input: map[string]interface{}{
				"password": 12345,
			},
			wantErr: true,
		},
		{
			name: "invalid token",
			input: map[string]interface{}{
				"token": 12345,
			},
			wantErr: true,
		},
		{
			name: "invalid headers type",
			input: map[string]interface{}{
				"headers": "not-a-map",
			},
			wantErr: true,
		},
		{
			name: "invalid header value",
			input: map[string]interface{}{
				"headers": map[string]interface{}{
					"X-Api-Key": 12345,
				},
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseAuthConfig(tt.input)

			if tt.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.want, got)
			}
		})
	}
}

func TestToString(t *testing.T) {
	tests := []struct {
		name    string
		input   interface{}
		want    string
		wantErr bool
	}{
		{
			name:  "string",
			input: "hello",
			want:  "hello",
		},
		{
			name:  "stringer type",
			input: stringerType{val: "world"},
			want:  "world",
		},
		{
			name:    "invalid type",
			input:   12345,
			wantErr: true,
		},
		{
			name:    "nil",
			input:   nil,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := toString(tt.input)

			if tt.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.want, got)
			}
		})
	}
}

// stringerType implements fmt.Stringer for testing.
type stringerType struct {
	val string
}

func (s stringerType) String() string {
	return s.val
}

func TestToMap(t *testing.T) {
	tests := []struct {
		name  string
		input interface{}
		want  map[string]interface{}
		ok    bool
	}{
		{
			name:  "map[string]interface{}",
			input: map[string]interface{}{"key": "value"},
			want:  map[string]interface{}{"key": "value"},
			ok:    true,
		},
		{
			name: "map[interface{}]interface{} with string keys",
			input: map[interface{}]interface{}{
				"key1": "value1",
				"key2": "value2",
			},
			want: map[string]interface{}{
				"key1": "value1",
				"key2": "value2",
			},
			ok: true,
		},
		{
			name: "map[interface{}]interface{} with stringer keys",
			input: map[interface{}]interface{}{
				stringerType{"key1"}: "value1",
			},
			want: map[string]interface{}{
				"key1": "value1",
			},
			ok: true,
		},
		{
			name:  "invalid type",
			input: "not-a-map",
			ok:    false,
		},
		{
			name:  "nil",
			input: nil,
			ok:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := toMap(tt.input)

			assert.Equal(t, tt.ok, ok)
			if tt.ok {
				assert.Equal(t, tt.want, got)
			}
		})
	}
}

func TestToInt(t *testing.T) {
	tests := []struct {
		name    string
		input   interface{}
		want    int
		wantErr bool
	}{
		{
			name:  "int",
			input: 42,
			want:  42,
		},
		{
			name:  "int64",
			input: int64(100),
			want:  100,
		},
		{
			name:  "float64",
			input: float64(3.14),
			want:  3,
		},
		{
			name:    "string",
			input:   "not-a-number",
			wantErr: true,
		},
		{
			name:    "nil",
			input:   nil,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := toInt(tt.input)

			if tt.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.want, got)
			}
		})
	}
}

func TestToBool(t *testing.T) {
	tests := []struct {
		name    string
		input   interface{}
		want    bool
		wantErr bool
	}{
		{
			name:  "true",
			input: true,
			want:  true,
		},
		{
			name:  "false",
			input: false,
			want:  false,
		},
		{
			name:    "string",
			input:   "true",
			wantErr: true,
		},
		{
			name:    "int",
			input:   1,
			wantErr: true,
		},
		{
			name:    "nil",
			input:   nil,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := toBool(tt.input)

			if tt.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.want, got)
			}
		})
	}
}

func TestParseDuration(t *testing.T) {
	tests := []struct {
		name    string
		input   interface{}
		want    time.Duration
		wantErr bool
	}{
		{
			name:  "time.Duration",
			input: 5 * time.Minute,
			want:  5 * time.Minute,
		},
		{
			name:  "string",
			input: "30s",
			want:  30 * time.Second,
		},
		{
			name:  "stringer",
			input: stringerType{"1h"},
			want:  1 * time.Hour,
		},
		{
			name:    "invalid string",
			input:   "invalid",
			wantErr: true,
		},
		{
			name:    "int",
			input:   12345,
			wantErr: true,
		},
		{
			name:    "nil",
			input:   nil,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseDuration(tt.input)

			if tt.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.want, got)
			}
		})
	}
}

func TestParseOptionsArg_NilArgs(t *testing.T) {
	// Test with nil second argument
	opts, err := parseOptionsArg([]interface{}{"http://example.com", nil})
	require.NoError(t, err)
	assert.Equal(t, httpstore.FetchOptions{}, opts)
}

func TestParseOptionsArg_SingleArg(t *testing.T) {
	// Test with only URL
	opts, err := parseOptionsArg([]interface{}{"http://example.com"})
	require.NoError(t, err)
	assert.Equal(t, httpstore.FetchOptions{}, opts)
}

func TestHTTPStoreWrapper_GetCachedContent_Validation(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()
	component := New(bus, logger, 0)

	// Create wrapper in validation mode
	wrapper := NewHTTPStoreWrapper(context.Background(), component, logger, true)

	// URL not in cache
	content, ok := wrapper.getCachedContent("http://example.com")
	assert.False(t, ok)
	assert.Empty(t, content)
}

func TestHTTPStoreWrapper_GetCachedContent_Production(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()
	component := New(bus, logger, 0)

	// Create wrapper in production mode
	wrapper := NewHTTPStoreWrapper(context.Background(), component, logger, false)

	// URL not in cache
	content, ok := wrapper.getCachedContent("http://example.com")
	assert.False(t, ok)
	assert.Empty(t, content)
}
