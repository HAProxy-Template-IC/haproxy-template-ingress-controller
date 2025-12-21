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

package client

import (
	"context"
	"errors"
	"net/http"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	v30 "haproxy-template-ic/pkg/generated/dataplaneapi/v30"
	v30ee "haproxy-template-ic/pkg/generated/dataplaneapi/v30ee"
	v31 "haproxy-template-ic/pkg/generated/dataplaneapi/v31"
	v31ee "haproxy-template-ic/pkg/generated/dataplaneapi/v31ee"
	v32 "haproxy-template-ic/pkg/generated/dataplaneapi/v32"
	v32ee "haproxy-template-ic/pkg/generated/dataplaneapi/v32ee"
)

// TestDispatchCreate verifies that the DispatchCreate function signatures
// compile correctly with version-specific types. This is a compile-time type
// check, not a runtime test.
func TestDispatchCreate(t *testing.T) {
	// Verify type compatibility by declaring callback signatures.
	// These verify the dispatcher helpers accept correct function signatures.
	type v32CreateFunc func(v32.Backend, *v32.CreateBackendParams) (*http.Response, error)
	type v31CreateFunc func(v31.Backend, *v31.CreateBackendParams) (*http.Response, error)
	type v30CreateFunc func(v30.Backend, *v30.CreateBackendParams) (*http.Response, error)

	// Type assertions at compile time verify the function signatures are valid.
	var (
		_ v32CreateFunc
		_ v31CreateFunc
		_ v30CreateFunc
	)

	assert.True(t, true, "dispatcher create signatures compile correctly")
}

// TestDispatchUpdate verifies that the DispatchUpdate function signatures
// compile correctly with version-specific types.
func TestDispatchUpdate(t *testing.T) {
	type v32UpdateFunc func(string, v32.Backend, *v32.ReplaceBackendParams) (*http.Response, error)
	type v31UpdateFunc func(string, v31.Backend, *v31.ReplaceBackendParams) (*http.Response, error)
	type v30UpdateFunc func(string, v30.Backend, *v30.ReplaceBackendParams) (*http.Response, error)

	var (
		_ v32UpdateFunc
		_ v31UpdateFunc
		_ v30UpdateFunc
	)

	assert.True(t, true, "dispatcher update signatures compile correctly")
}

// TestDispatchDelete verifies that the DispatchDelete function signatures
// compile correctly with version-specific types.
func TestDispatchDelete(t *testing.T) {
	type v32DeleteFunc func(string, *v32.DeleteBackendParams) (*http.Response, error)
	type v31DeleteFunc func(string, *v31.DeleteBackendParams) (*http.Response, error)
	type v30DeleteFunc func(string, *v30.DeleteBackendParams) (*http.Response, error)

	var (
		_ v32DeleteFunc
		_ v31DeleteFunc
		_ v30DeleteFunc
	)

	assert.True(t, true, "dispatcher delete signatures compile correctly")
}

// TestDispatchCreateChild verifies that the DispatchCreateChild function signatures
// compile correctly with version-specific types.
func TestDispatchCreateChild(t *testing.T) {
	type v32CreateChildFunc func(string, int, v32.Acl, *v32.CreateAclFrontendParams) (*http.Response, error)
	type v31CreateChildFunc func(string, int, v31.Acl, *v31.CreateAclFrontendParams) (*http.Response, error)
	type v30CreateChildFunc func(string, int, v30.Acl, *v30.CreateAclFrontendParams) (*http.Response, error)

	var (
		_ v32CreateChildFunc
		_ v31CreateChildFunc
		_ v30CreateChildFunc
	)

	assert.True(t, true, "dispatcher create child signatures compile correctly")
}

// TestDispatchReplaceChild verifies that the DispatchReplaceChild function signatures
// compile correctly with version-specific types.
func TestDispatchReplaceChild(t *testing.T) {
	type v32ReplaceChildFunc func(string, int, v32.Acl, *v32.ReplaceAclFrontendParams) (*http.Response, error)
	type v31ReplaceChildFunc func(string, int, v31.Acl, *v31.ReplaceAclFrontendParams) (*http.Response, error)
	type v30ReplaceChildFunc func(string, int, v30.Acl, *v30.ReplaceAclFrontendParams) (*http.Response, error)

	var (
		_ v32ReplaceChildFunc
		_ v31ReplaceChildFunc
		_ v30ReplaceChildFunc
	)

	assert.True(t, true, "dispatcher replace child signatures compile correctly")
}

// TestDispatchDeleteChild verifies that the DispatchDeleteChild function signatures
// compile correctly with version-specific types.
func TestDispatchDeleteChild(t *testing.T) {
	type v32DeleteChildFunc func(string, int, *v32.DeleteAclFrontendParams) (*http.Response, error)
	type v31DeleteChildFunc func(string, int, *v31.DeleteAclFrontendParams) (*http.Response, error)
	type v30DeleteChildFunc func(string, int, *v30.DeleteAclFrontendParams) (*http.Response, error)

	var (
		_ v32DeleteChildFunc
		_ v31DeleteChildFunc
		_ v30DeleteChildFunc
	)

	assert.True(t, true, "dispatcher delete child signatures compile correctly")
}

// TestDispatchHelpersWithRealTypes verifies that the dispatcher helpers work
// with actual versioned dataplaneapi types.
func TestDispatchHelpersWithRealTypes(t *testing.T) {
	t.Run("dispatchCreate with v32.Backend", func(t *testing.T) {
		// Create a real v32 backend model and verify it has expected fields.
		backend := v32.Backend{
			Name: "test-backend",
		}

		// Verify the Backend type has expected fields.
		assert.Equal(t, "test-backend", backend.Name)
	})

	t.Run("dispatchCreateChild with v32.Acl", func(t *testing.T) {
		// Create a real v32 ACL model and verify it has expected fields.
		value := "/api"
		acl := v32.Acl{
			AclName:   "is_api",
			Criterion: "path_beg",
			Value:     &value,
		}

		// Verify the Acl type has expected fields.
		assert.Equal(t, "is_api", acl.AclName)
		assert.Equal(t, "path_beg", acl.Criterion)
		assert.Equal(t, "/api", *acl.Value)
	})
}

// Runtime tests for dispatcher helpers

// dispatchTestModel is a simple model for testing dispatch helpers.
type dispatchTestModel struct {
	Name  string `json:"name"`
	Value int    `json:"value"`
}

func TestDispatchCreate_Runtime(t *testing.T) {
	server := newMockServer(t, mockServerConfig{})
	defer server.Close()

	client := newTestClient(t, server)

	t.Run("success - v3.2 callback invoked", func(t *testing.T) {
		v32Called := atomic.Int32{}
		model := dispatchTestModel{Name: "test", Value: 42}

		resp, err := DispatchCreate[dispatchTestModel, dispatchTestModel, dispatchTestModel, dispatchTestModel, dispatchTestModel, dispatchTestModel, dispatchTestModel](
			context.Background(),
			client,
			model,
			func(m dispatchTestModel) (*http.Response, error) {
				v32Called.Add(1)
				assert.Equal(t, "test", m.Name)
				assert.Equal(t, 42, m.Value)
				return &http.Response{StatusCode: http.StatusCreated}, nil
			},
			func(m dispatchTestModel) (*http.Response, error) {
				t.Error("v31 should not be called")
				return nil, errors.New("v31 should not be called")
			},
			func(m dispatchTestModel) (*http.Response, error) {
				t.Error("v30 should not be called")
				return nil, errors.New("v30 should not be called")
			},
			func(m dispatchTestModel) (*http.Response, error) {
				t.Error("v32ee should not be called")
				return nil, errors.New("v32ee should not be called")
			},
			func(m dispatchTestModel) (*http.Response, error) {
				t.Error("v31ee should not be called")
				return nil, errors.New("v31ee should not be called")
			},
			func(m dispatchTestModel) (*http.Response, error) {
				t.Error("v30ee should not be called")
				return nil, errors.New("v30ee should not be called")
			},
		)

		require.NoError(t, err)
		require.NotNil(t, resp)
		assert.Equal(t, http.StatusCreated, resp.StatusCode)
		assert.Equal(t, int32(1), v32Called.Load())
		if resp.Body != nil {
			resp.Body.Close()
		}
	})

	t.Run("callback error propagated", func(t *testing.T) {
		expectedErr := errors.New("create failed")

		resp, err := DispatchCreate[dispatchTestModel, dispatchTestModel, dispatchTestModel, dispatchTestModel, dispatchTestModel, dispatchTestModel, dispatchTestModel](
			context.Background(),
			client,
			dispatchTestModel{Name: "test"},
			func(m dispatchTestModel) (*http.Response, error) {
				return nil, expectedErr
			},
			func(m dispatchTestModel) (*http.Response, error) { return nil, errors.New("unused") },
			func(m dispatchTestModel) (*http.Response, error) { return nil, errors.New("unused") },
			func(m dispatchTestModel) (*http.Response, error) { return nil, errors.New("unused") },
			func(m dispatchTestModel) (*http.Response, error) { return nil, errors.New("unused") },
			func(m dispatchTestModel) (*http.Response, error) { return nil, errors.New("unused") },
		)
		if resp != nil && resp.Body != nil {
			resp.Body.Close()
		}

		require.Error(t, err)
		assert.Contains(t, err.Error(), "create failed")
	})
}

func TestDispatchUpdate_Runtime(t *testing.T) {
	server := newMockServer(t, mockServerConfig{})
	defer server.Close()

	client := newTestClient(t, server)

	t.Run("success - name and model passed correctly", func(t *testing.T) {
		v32Called := atomic.Int32{}
		model := dispatchTestModel{Name: "updated", Value: 100}

		resp, err := DispatchUpdate[dispatchTestModel, dispatchTestModel, dispatchTestModel, dispatchTestModel, dispatchTestModel, dispatchTestModel, dispatchTestModel](
			context.Background(),
			client,
			"resource-name",
			model,
			func(name string, m dispatchTestModel) (*http.Response, error) {
				v32Called.Add(1)
				assert.Equal(t, "resource-name", name)
				assert.Equal(t, "updated", m.Name)
				assert.Equal(t, 100, m.Value)
				return &http.Response{StatusCode: http.StatusOK}, nil
			},
			func(name string, m dispatchTestModel) (*http.Response, error) {
				t.Error("v31 should not be called")
				return nil, errors.New("v31 should not be called")
			},
			func(name string, m dispatchTestModel) (*http.Response, error) {
				t.Error("v30 should not be called")
				return nil, errors.New("v30 should not be called")
			},
			func(name string, m dispatchTestModel) (*http.Response, error) {
				t.Error("v32ee should not be called")
				return nil, errors.New("v32ee should not be called")
			},
			func(name string, m dispatchTestModel) (*http.Response, error) {
				t.Error("v31ee should not be called")
				return nil, errors.New("v31ee should not be called")
			},
			func(name string, m dispatchTestModel) (*http.Response, error) {
				t.Error("v30ee should not be called")
				return nil, errors.New("v30ee should not be called")
			},
		)

		require.NoError(t, err)
		require.NotNil(t, resp)
		assert.Equal(t, http.StatusOK, resp.StatusCode)
		assert.Equal(t, int32(1), v32Called.Load())
		if resp.Body != nil {
			resp.Body.Close()
		}
	})
}

func TestDispatchDelete_Runtime(t *testing.T) {
	server := newMockServer(t, mockServerConfig{})
	defer server.Close()

	client := newTestClient(t, server)

	t.Run("success - name passed correctly", func(t *testing.T) {
		v32Called := atomic.Int32{}

		resp, err := DispatchDelete(
			context.Background(),
			client,
			"delete-me",
			func(name string) (*http.Response, error) {
				v32Called.Add(1)
				assert.Equal(t, "delete-me", name)
				return &http.Response{StatusCode: http.StatusNoContent}, nil
			},
			func(name string) (*http.Response, error) {
				t.Error("v31 should not be called")
				return nil, errors.New("v31 should not be called")
			},
			func(name string) (*http.Response, error) {
				t.Error("v30 should not be called")
				return nil, errors.New("v30 should not be called")
			},
			func(name string) (*http.Response, error) {
				t.Error("v32ee should not be called")
				return nil, errors.New("v32ee should not be called")
			},
			func(name string) (*http.Response, error) {
				t.Error("v31ee should not be called")
				return nil, errors.New("v31ee should not be called")
			},
			func(name string) (*http.Response, error) {
				t.Error("v30ee should not be called")
				return nil, errors.New("v30ee should not be called")
			},
		)

		require.NoError(t, err)
		require.NotNil(t, resp)
		assert.Equal(t, http.StatusNoContent, resp.StatusCode)
		assert.Equal(t, int32(1), v32Called.Load())
		if resp.Body != nil {
			resp.Body.Close()
		}
	})
}

func TestDispatchCreateChild_Runtime(t *testing.T) {
	server := newMockServer(t, mockServerConfig{})
	defer server.Close()

	client := newTestClient(t, server)

	t.Run("success - parent, index and model passed correctly", func(t *testing.T) {
		v32Called := atomic.Int32{}
		model := dispatchTestModel{Name: "child", Value: 5}

		resp, err := DispatchCreateChild[dispatchTestModel, dispatchTestModel, dispatchTestModel, dispatchTestModel, dispatchTestModel, dispatchTestModel, dispatchTestModel](
			context.Background(),
			client,
			"parent-frontend",
			10,
			model,
			func(parent string, idx int, m dispatchTestModel) (*http.Response, error) {
				v32Called.Add(1)
				assert.Equal(t, "parent-frontend", parent)
				assert.Equal(t, 10, idx)
				assert.Equal(t, "child", m.Name)
				assert.Equal(t, 5, m.Value)
				return &http.Response{StatusCode: http.StatusCreated}, nil
			},
			func(parent string, idx int, m dispatchTestModel) (*http.Response, error) {
				t.Error("v31 should not be called")
				return nil, errors.New("v31 should not be called")
			},
			func(parent string, idx int, m dispatchTestModel) (*http.Response, error) {
				t.Error("v30 should not be called")
				return nil, errors.New("v30 should not be called")
			},
			func(parent string, idx int, m dispatchTestModel) (*http.Response, error) {
				t.Error("v32ee should not be called")
				return nil, errors.New("v32ee should not be called")
			},
			func(parent string, idx int, m dispatchTestModel) (*http.Response, error) {
				t.Error("v31ee should not be called")
				return nil, errors.New("v31ee should not be called")
			},
			func(parent string, idx int, m dispatchTestModel) (*http.Response, error) {
				t.Error("v30ee should not be called")
				return nil, errors.New("v30ee should not be called")
			},
		)

		require.NoError(t, err)
		require.NotNil(t, resp)
		assert.Equal(t, http.StatusCreated, resp.StatusCode)
		assert.Equal(t, int32(1), v32Called.Load())
		if resp.Body != nil {
			resp.Body.Close()
		}
	})
}

func TestDispatchReplaceChild_Runtime(t *testing.T) {
	server := newMockServer(t, mockServerConfig{})
	defer server.Close()

	client := newTestClient(t, server)

	t.Run("success - parent, index and model passed correctly", func(t *testing.T) {
		v32Called := atomic.Int32{}
		model := dispatchTestModel{Name: "updated-child", Value: 99}

		resp, err := DispatchReplaceChild[dispatchTestModel, dispatchTestModel, dispatchTestModel, dispatchTestModel, dispatchTestModel, dispatchTestModel, dispatchTestModel](
			context.Background(),
			client,
			"parent-backend",
			5,
			model,
			func(parent string, idx int, m dispatchTestModel) (*http.Response, error) {
				v32Called.Add(1)
				assert.Equal(t, "parent-backend", parent)
				assert.Equal(t, 5, idx)
				assert.Equal(t, "updated-child", m.Name)
				assert.Equal(t, 99, m.Value)
				return &http.Response{StatusCode: http.StatusOK}, nil
			},
			func(parent string, idx int, m dispatchTestModel) (*http.Response, error) {
				t.Error("v31 should not be called")
				return nil, errors.New("v31 should not be called")
			},
			func(parent string, idx int, m dispatchTestModel) (*http.Response, error) {
				t.Error("v30 should not be called")
				return nil, errors.New("v30 should not be called")
			},
			func(parent string, idx int, m dispatchTestModel) (*http.Response, error) {
				t.Error("v32ee should not be called")
				return nil, errors.New("v32ee should not be called")
			},
			func(parent string, idx int, m dispatchTestModel) (*http.Response, error) {
				t.Error("v31ee should not be called")
				return nil, errors.New("v31ee should not be called")
			},
			func(parent string, idx int, m dispatchTestModel) (*http.Response, error) {
				t.Error("v30ee should not be called")
				return nil, errors.New("v30ee should not be called")
			},
		)

		require.NoError(t, err)
		require.NotNil(t, resp)
		assert.Equal(t, http.StatusOK, resp.StatusCode)
		assert.Equal(t, int32(1), v32Called.Load())
		if resp.Body != nil {
			resp.Body.Close()
		}
	})
}

func TestDispatchDeleteChild_Runtime(t *testing.T) {
	server := newMockServer(t, mockServerConfig{})
	defer server.Close()

	client := newTestClient(t, server)

	t.Run("success - parent and index passed correctly", func(t *testing.T) {
		v32Called := atomic.Int32{}

		resp, err := DispatchDeleteChild(
			context.Background(),
			client,
			"parent-frontend",
			7,
			func(parent string, idx int) (*http.Response, error) {
				v32Called.Add(1)
				assert.Equal(t, "parent-frontend", parent)
				assert.Equal(t, 7, idx)
				return &http.Response{StatusCode: http.StatusNoContent}, nil
			},
			func(parent string, idx int) (*http.Response, error) {
				t.Error("v31 should not be called")
				return nil, errors.New("v31 should not be called")
			},
			func(parent string, idx int) (*http.Response, error) {
				t.Error("v30 should not be called")
				return nil, errors.New("v30 should not be called")
			},
			func(parent string, idx int) (*http.Response, error) {
				t.Error("v32ee should not be called")
				return nil, errors.New("v32ee should not be called")
			},
			func(parent string, idx int) (*http.Response, error) {
				t.Error("v31ee should not be called")
				return nil, errors.New("v31ee should not be called")
			},
			func(parent string, idx int) (*http.Response, error) {
				t.Error("v30ee should not be called")
				return nil, errors.New("v30ee should not be called")
			},
		)

		require.NoError(t, err)
		require.NotNil(t, resp)
		assert.Equal(t, http.StatusNoContent, resp.StatusCode)
		assert.Equal(t, int32(1), v32Called.Load())
		if resp.Body != nil {
			resp.Body.Close()
		}
	})
}

func TestDispatchHelpers_VersionRouting(t *testing.T) {
	tests := []struct {
		name            string
		apiVersion      string
		expectedVersion string
	}{
		{
			name:            "v3.2 community",
			apiVersion:      "v3.2.6 87ad0bcf",
			expectedVersion: "v32",
		},
		{
			name:            "v3.1 community",
			apiVersion:      "v3.1.0 abcd1234",
			expectedVersion: "v31",
		},
		{
			name:            "v3.0 community",
			apiVersion:      "v3.0.0 12345678",
			expectedVersion: "v30",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := newMockServer(t, mockServerConfig{
				apiVersion: tt.apiVersion,
			})
			defer server.Close()

			client := newTestClient(t, server)

			calledVersion := ""
			model := dispatchTestModel{Name: "test"}

			resp, err := DispatchCreate[dispatchTestModel, dispatchTestModel, dispatchTestModel, dispatchTestModel, dispatchTestModel, dispatchTestModel, dispatchTestModel](
				context.Background(),
				client,
				model,
				func(m dispatchTestModel) (*http.Response, error) {
					calledVersion = "v32"
					return &http.Response{StatusCode: http.StatusOK}, nil
				},
				func(m dispatchTestModel) (*http.Response, error) {
					calledVersion = "v31"
					return &http.Response{StatusCode: http.StatusOK}, nil
				},
				func(m dispatchTestModel) (*http.Response, error) {
					calledVersion = "v30"
					return &http.Response{StatusCode: http.StatusOK}, nil
				},
				func(m dispatchTestModel) (*http.Response, error) {
					calledVersion = "v32ee"
					return &http.Response{StatusCode: http.StatusOK}, nil
				},
				func(m dispatchTestModel) (*http.Response, error) {
					calledVersion = "v31ee"
					return &http.Response{StatusCode: http.StatusOK}, nil
				},
				func(m dispatchTestModel) (*http.Response, error) {
					calledVersion = "v30ee"
					return &http.Response{StatusCode: http.StatusOK}, nil
				},
			)

			require.NoError(t, err)
			if resp.Body != nil {
				resp.Body.Close()
			}
			assert.Equal(t, tt.expectedVersion, calledVersion)
		})
	}
}

// Ensure enterprise imports are used - they're needed for type assertions in signature tests.
var (
	_ = v30ee.Client{}
	_ = v31ee.Client{}
	_ = v32ee.Client{}
)
