package dataplane

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSyncError_Error(t *testing.T) {
	tests := []struct {
		name     string
		err      *SyncError
		wantMsg  string
		contains []string
	}{
		{
			name: "with cause",
			err: &SyncError{
				Stage:   "apply",
				Message: "operation failed",
				Cause:   errors.New("underlying error"),
			},
			wantMsg: "apply stage failed: operation failed: underlying error",
		},
		{
			name: "without cause",
			err: &SyncError{
				Stage:   "parse-current",
				Message: "invalid syntax",
				Cause:   nil,
			},
			wantMsg: "parse-current stage failed: invalid syntax",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.err.Error()
			assert.Equal(t, tt.wantMsg, got)
		})
	}
}

func TestSyncError_Unwrap(t *testing.T) {
	cause := errors.New("root cause")
	syncErr := &SyncError{
		Stage:   "apply",
		Message: "failed",
		Cause:   cause,
	}

	unwrapped := syncErr.Unwrap()
	assert.Equal(t, cause, unwrapped)
}

func TestConnectionError(t *testing.T) {
	cause := errors.New("connection refused")
	connErr := &ConnectionError{
		Endpoint: "http://haproxy:5555",
		Cause:    cause,
	}

	errMsg := connErr.Error()
	assert.Contains(t, errMsg, "connecting to dataplane API")
	assert.Contains(t, errMsg, "http://haproxy:5555")
	assert.Contains(t, errMsg, "connection refused")

	unwrapped := connErr.Unwrap()
	assert.Equal(t, cause, unwrapped)
}

func TestParseError(t *testing.T) {
	tests := []struct {
		name     string
		err      *ParseError
		contains []string
	}{
		{
			name: "with snippet",
			err: &ParseError{
				ConfigType:    "current",
				ConfigSnippet: "frontend http\n  bind :80",
				Cause:         errors.New("unexpected token"),
			},
			contains: []string{"parsing current configuration", "unexpected token"},
		},
		{
			name: "without snippet",
			err: &ParseError{
				ConfigType: "desired",
				Cause:      errors.New("invalid directive"),
			},
			contains: []string{"parsing desired configuration", "invalid directive"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			errMsg := tt.err.Error()
			for _, s := range tt.contains {
				assert.Contains(t, errMsg, s)
			}

			unwrapped := tt.err.Unwrap()
			assert.NotNil(t, unwrapped)
		})
	}
}

func TestNewConnectionError(t *testing.T) {
	cause := errors.New("connection refused")
	syncErr := NewConnectionError("http://haproxy:5555", cause)

	require.NotNil(t, syncErr)
	assert.Equal(t, "connect", syncErr.Stage)
	assert.Contains(t, syncErr.Message, "http://haproxy:5555")
	require.NotEmpty(t, syncErr.Hints)
	assert.Contains(t, syncErr.Hints[0], "Verify the dataplane API URL")

	connErr, ok := errors.AsType[*ConnectionError](syncErr)
	require.True(t, ok)
	assert.Equal(t, "http://haproxy:5555", connErr.Endpoint)
}

func TestNewParseError(t *testing.T) {
	tests := []struct {
		name       string
		configType string
		wantHint   string
	}{
		{
			name:       "current config",
			configType: "current",
			wantHint:   "current config from dataplane API may be corrupted",
		},
		{
			name:       "desired config",
			configType: "desired",
			wantHint:   "Review the desired configuration for syntax errors",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cause := errors.New("parse failed")
			syncErr := NewParseError(tt.configType, "frontend http\n", cause)

			require.NotNil(t, syncErr)
			assert.Equal(t, "parse-"+tt.configType, syncErr.Stage)
			assert.Contains(t, syncErr.Message, tt.configType)

			hasExpectedHint := false
			for _, hint := range syncErr.Hints {
				if contains(hint, tt.wantHint) {
					hasExpectedHint = true
					break
				}
			}
			assert.True(t, hasExpectedHint, "expected hint not found: %s", tt.wantHint)

			parseErr, ok := errors.AsType[*ParseError](syncErr)
			require.True(t, ok)
			assert.Equal(t, tt.configType, parseErr.ConfigType)
		})
	}
}

func TestSimplifyValidationError(t *testing.T) {
	tests := []struct {
		name    string
		err     error
		want    string
		wantNot []string
	}{
		{
			name: "nil error",
			err:  nil,
			want: "",
		},
		{
			name: "semantic validation error",
			err:  errors.New("semantic validation failed: configuration has semantic errors: haproxy validation failed: [ALERT] backend 'missing' not found"),
			want: "[ALERT] backend 'missing' not found",
		},
		{
			name: "semantic error without haproxy marker",
			err:  errors.New("semantic validation failed: configuration has semantic errors"),
			want: "semantic validation failed: configuration has semantic errors",
		},
		{
			name: "semantic validation error with multi-line context",
			err: errors.New(`semantic validation failed: configuration has semantic errors: haproxy validation failed:   userlist auth_users
      user admin password ...
→ [ALERT] (001) : parsing [haproxy.cfg:15] : unknown user 'missing' in userlist 'auth_users' (declared at haproxy.cfg:12)
  backend api
      server s1 127.0.0.1:8080`),
			want: `  userlist auth_users
      user admin password ...
→ [ALERT] (001) : parsing [haproxy.cfg:15] : unknown user 'missing' in userlist 'auth_users' (declared at haproxy.cfg:12)
  backend api
      server s1 127.0.0.1:8080`,
		},
		{
			name: "semantic validation error - backend has no server",
			err: errors.New(`semantic validation failed: configuration has semantic errors: haproxy validation failed:   defaults
      mode http
  backend api
→ [ALERT] (002) : parsing [haproxy.cfg:15] : backend 'api' has no server
      balance roundrobin`),
			want: `  defaults
      mode http
  backend api
→ [ALERT] (002) : parsing [haproxy.cfg:15] : backend 'api' has no server
      balance roundrobin`,
		},
		{
			name:    "schema validation error with value",
			err:     errors.New(`schema validation failed: configuration violates API schema constraints: Error at "/maxconn": must be >= 1` + "\nValue:\n  \"0\""),
			want:    "maxconn must be >= 1 (got 0)",
			wantNot: []string{"schema validation failed", "Error at"},
		},
		{
			name:    "schema validation error without value",
			err:     errors.New(`schema validation failed: Error at "/weight": number must be at most 256`),
			want:    "weight number must be at most 256",
			wantNot: []string{"Error at"},
		},
		{
			name: "schema error without Error at",
			err:  errors.New(`schema validation failed: some other error format`),
			want: `schema validation failed: some other error format`,
		},
		{
			name: "schema error with malformed field",
			err:  errors.New(`schema validation failed: Error at "/field`),
			want: `schema validation failed: Error at "/field`,
		},
		{
			name: "schema error with short constraint",
			err:  errors.New(`schema validation failed: Error at "/x": `),
			want: `schema validation failed: Error at "/x": `,
		},
		{
			name: "unknown error type",
			err:  errors.New("some other error"),
			want: "some other error",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := SimplifyValidationError(tt.err)
			assert.Equal(t, tt.want, got)

			for _, not := range tt.wantNot {
				assert.NotContains(t, got, not)
			}
		})
	}
}

func TestSimplifyRenderingError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want string
	}{
		{
			name: "nil error",
			err:  nil,
			want: "",
		},
		{
			name: "fail function error",
			err:  errors.New("rendering haproxy.cfg: rendering template 'haproxy.cfg': unable to execute template: invalid call to function 'fail': Service 'api-backend' not found in namespace 'default'"),
			want: "Service 'api-backend' not found in namespace 'default'",
		},
		{
			name: "fail function with whitespace",
			err:  errors.New("invalid call to function 'fail': Missing required field   "),
			want: "Missing required field",
		},
		{
			name: "non-fail error",
			err:  errors.New("failed to render: undefined variable 'foo'"),
			want: "failed to render: undefined variable 'foo'",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := SimplifyRenderingError(tt.err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || substr == "" ||
		(s != "" && substr != "" && s != substr && containsSubstr(s, substr)))
}

func containsSubstr(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
