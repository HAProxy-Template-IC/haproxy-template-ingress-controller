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
	assert.Contains(t, errMsg, "failed to connect to dataplane API")
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
			name: "with line number",
			err: &ParseError{
				ConfigType:    "current",
				ConfigSnippet: "frontend http\n  bind :80",
				Line:          42,
				Cause:         errors.New("unexpected token"),
			},
			contains: []string{"failed to parse current configuration", "near line 42", "unexpected token"},
		},
		{
			name: "without line number",
			err: &ParseError{
				ConfigType: "desired",
				Cause:      errors.New("invalid directive"),
			},
			contains: []string{"failed to parse desired configuration", "invalid directive"},
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

func TestConflictError(t *testing.T) {
	conflictErr := &ConflictError{
		Retries:         3,
		ExpectedVersion: 100,
		ActualVersion:   "102",
	}

	errMsg := conflictErr.Error()
	assert.Contains(t, errMsg, "version conflict after 3 retries")
	assert.Contains(t, errMsg, "expected version 100")
	assert.Contains(t, errMsg, "server has 102")
}

func TestOperationError(t *testing.T) {
	cause := errors.New("backend not found")
	opErr := &OperationError{
		OperationType: "delete",
		Section:       "backend",
		Resource:      "api-backend",
		Cause:         cause,
	}

	errMsg := opErr.Error()
	assert.Contains(t, errMsg, "failed to delete backend 'api-backend'")
	assert.Contains(t, errMsg, "backend not found")

	unwrapped := opErr.Unwrap()
	assert.Equal(t, cause, unwrapped)
}

func TestFallbackError(t *testing.T) {
	originalErr := errors.New("fine-grained sync failed")
	fallbackCause := errors.New("raw config push also failed")
	fallbackErr := &FallbackError{
		OriginalError: originalErr,
		FallbackCause: fallbackCause,
	}

	errMsg := fallbackErr.Error()
	assert.Contains(t, errMsg, "fine-grained sync failed")
	assert.Contains(t, errMsg, "fallback to raw config also failed")

	unwrapped := fallbackErr.Unwrap()
	assert.Equal(t, fallbackCause, unwrapped)
}

func TestNewConnectionError(t *testing.T) {
	cause := errors.New("connection refused")
	syncErr := NewConnectionError("http://haproxy:5555", cause)

	require.NotNil(t, syncErr)
	assert.Equal(t, "connect", syncErr.Stage)
	assert.Contains(t, syncErr.Message, "http://haproxy:5555")
	require.NotEmpty(t, syncErr.Hints)
	assert.Contains(t, syncErr.Hints[0], "Verify the dataplane API URL")

	var connErr *ConnectionError
	require.True(t, errors.As(syncErr, &connErr))
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

			var parseErr *ParseError
			require.True(t, errors.As(syncErr, &parseErr))
			assert.Equal(t, tt.configType, parseErr.ConfigType)
		})
	}
}

func TestNewValidationError(t *testing.T) {
	cause := errors.New("backend 'missing' not found")
	syncErr := NewValidationError("invalid reference", cause)

	require.NotNil(t, syncErr)
	assert.Equal(t, "apply", syncErr.Stage)
	assert.Contains(t, syncErr.Message, "rejected the configuration")
	require.NotEmpty(t, syncErr.Hints)

	var valErr *ValidationError
	require.True(t, errors.As(syncErr, &valErr))
	assert.Equal(t, "semantic", valErr.Phase)
}

func TestNewConflictError(t *testing.T) {
	syncErr := NewConflictError(5, 100, "105")

	require.NotNil(t, syncErr)
	assert.Equal(t, "commit", syncErr.Stage)
	assert.Contains(t, syncErr.Message, "5 retries")
	require.NotEmpty(t, syncErr.Hints)

	var conflictErr *ConflictError
	require.True(t, errors.As(syncErr, &conflictErr))
	assert.Equal(t, 5, conflictErr.Retries)
	assert.Equal(t, int64(100), conflictErr.ExpectedVersion)
	assert.Equal(t, "105", conflictErr.ActualVersion)
}

func TestNewOperationError(t *testing.T) {
	cause := errors.New("server limit exceeded")
	syncErr := NewOperationError("create", "server", "web1", cause)

	require.NotNil(t, syncErr)
	assert.Equal(t, "apply", syncErr.Stage)
	assert.Contains(t, syncErr.Message, "create")
	assert.Contains(t, syncErr.Message, "server")
	assert.Contains(t, syncErr.Message, "web1")
	require.NotEmpty(t, syncErr.Hints)

	var opErr *OperationError
	require.True(t, errors.As(syncErr, &opErr))
	assert.Equal(t, "create", opErr.OperationType)
	assert.Equal(t, "server", opErr.Section)
	assert.Equal(t, "web1", opErr.Resource)
}

func TestNewFallbackError(t *testing.T) {
	originalErr := errors.New("fine-grained failed")
	fallbackCause := errors.New("raw also failed")
	syncErr := NewFallbackError(originalErr, fallbackCause)

	require.NotNil(t, syncErr)
	assert.Equal(t, "fallback", syncErr.Stage)
	assert.Contains(t, syncErr.Message, "fine-grained sync")
	assert.Contains(t, syncErr.Message, "fallback failed")
	require.NotEmpty(t, syncErr.Hints)

	var fallbackErr *FallbackError
	require.True(t, errors.As(syncErr, &fallbackErr))
	assert.Equal(t, originalErr, fallbackErr.OriginalError)
	assert.Equal(t, fallbackCause, fallbackErr.FallbackCause)
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
			err:  errors.New("failed to render haproxy.cfg: failed to render template 'haproxy.cfg': unable to execute template: invalid call to function 'fail': Service 'api-backend' not found in namespace 'default'"),
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
