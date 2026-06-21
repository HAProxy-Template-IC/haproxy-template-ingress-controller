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

package sections

import (
	"testing"

	"github.com/haproxytech/client-native/v6/models"
	"github.com/stretchr/testify/assert"
)

func TestPtrStr(t *testing.T) {
	tests := []struct {
		name string
		in   *string
		want string
	}{
		{
			name: "nil pointer",
			in:   nil,
			want: "",
		},
		{
			name: "empty string pointer",
			in:   new(""),
			want: "",
		},
		{
			name: "non-empty string pointer",
			in:   new("test"),
			want: "test",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ptrStr(tt.in)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestBackendFactoryFunctions(t *testing.T) {
	backend := &models.Backend{}
	backend.Name = "api-backend"

	tests := []struct {
		name             string
		factory          func(*models.Backend) Operation
		wantType         OperationType
		wantSection      string
		wantDescContains string
	}{
		{
			name:             "NewBackendCreate",
			factory:          NewBackendCreate,
			wantType:         OperationCreate,
			wantSection:      "backend",
			wantDescContains: "Create backend 'api-backend'",
		},
		{
			name:             "NewBackendUpdate",
			factory:          NewBackendUpdate,
			wantType:         OperationUpdate,
			wantSection:      "backend",
			wantDescContains: "Update backend 'api-backend'",
		},
		{
			name:             "NewBackendDelete",
			factory:          NewBackendDelete,
			wantType:         OperationDelete,
			wantSection:      "backend",
			wantDescContains: "Delete backend 'api-backend'",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			op := tt.factory(backend)
			assertOperation(t, op, tt.wantType, tt.wantSection, tt.wantDescContains)
		})
	}
}

func TestFrontendFactoryFunctions(t *testing.T) {
	frontend := &models.Frontend{}
	frontend.Name = "http-frontend"

	tests := []struct {
		name             string
		factory          func(*models.Frontend) Operation
		wantType         OperationType
		wantSection      string
		wantDescContains string
	}{
		{
			name:             "NewFrontendCreate",
			factory:          NewFrontendCreate,
			wantType:         OperationCreate,
			wantSection:      "frontend",
			wantDescContains: "Create frontend 'http-frontend'",
		},
		{
			name:             "NewFrontendUpdate",
			factory:          NewFrontendUpdate,
			wantType:         OperationUpdate,
			wantSection:      "frontend",
			wantDescContains: "Update frontend 'http-frontend'",
		},
		{
			name:             "NewFrontendDelete",
			factory:          NewFrontendDelete,
			wantType:         OperationDelete,
			wantSection:      "frontend",
			wantDescContains: "Delete frontend 'http-frontend'",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			op := tt.factory(frontend)
			assertOperation(t, op, tt.wantType, tt.wantSection, tt.wantDescContains)
		})
	}
}

func TestDefaultsFactoryFunctions(t *testing.T) {
	defaults := &models.Defaults{}
	defaults.Name = "http-defaults"

	tests := []struct {
		name             string
		factory          func(*models.Defaults) Operation
		wantType         OperationType
		wantSection      string
		wantDescContains string
	}{
		{
			name:             "NewDefaultsCreate",
			factory:          NewDefaultsCreate,
			wantType:         OperationCreate,
			wantSection:      "defaults",
			wantDescContains: "Create defaults section 'http-defaults'",
		},
		{
			name:             "NewDefaultsUpdate",
			factory:          NewDefaultsUpdate,
			wantType:         OperationUpdate,
			wantSection:      "defaults",
			wantDescContains: "Update defaults section 'http-defaults'",
		},
		{
			name:             "NewDefaultsDelete",
			factory:          NewDefaultsDelete,
			wantType:         OperationDelete,
			wantSection:      "defaults",
			wantDescContains: "Delete defaults section 'http-defaults'",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			op := tt.factory(defaults)
			assertOperation(t, op, tt.wantType, tt.wantSection, tt.wantDescContains)
		})
	}
}

func TestGlobalFactoryFunction(t *testing.T) {
	global := &models.Global{}

	op := NewGlobalUpdate(global)

	assert.Equal(t, OperationUpdate, op.Type())
	assert.Equal(t, "global", op.Section())
	assert.Equal(t, "Update global section", op.Describe())
}

func TestACLFactoryFunctions(t *testing.T) {
	acl := &models.ACL{ACLName: "is_api"}

	t.Run("frontend ACL operations", func(t *testing.T) {
		tests := []struct {
			name             string
			factory          func(string, *models.ACL, int) Operation
			wantType         OperationType
			wantDescContains string
		}{
			{
				name:             "NewACLFrontendCreate",
				factory:          NewACLFrontendCreate,
				wantType:         OperationCreate,
				wantDescContains: "Create ACL 'is_api' in frontend 'http'",
			},
			{
				name:             "NewACLFrontendUpdate",
				factory:          NewACLFrontendUpdate,
				wantType:         OperationUpdate,
				wantDescContains: "Update ACL 'is_api' in frontend 'http'",
			},
			{
				name:             "NewACLFrontendDelete",
				factory:          NewACLFrontendDelete,
				wantType:         OperationDelete,
				wantDescContains: "Delete ACL 'is_api' from frontend 'http'",
			},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				op := tt.factory("http", acl, 0)

				assert.Equal(t, tt.wantType, op.Type())
				assert.Equal(t, "acl", op.Section())
				assert.Contains(t, op.Describe(), tt.wantDescContains)
			})
		}
	})

	t.Run("backend ACL operations", func(t *testing.T) {
		tests := []struct {
			name             string
			factory          func(string, *models.ACL, int) Operation
			wantType         OperationType
			wantDescContains string
		}{
			{
				name:             "NewACLBackendCreate",
				factory:          NewACLBackendCreate,
				wantType:         OperationCreate,
				wantDescContains: "Create ACL 'is_api' in backend 'api'",
			},
			{
				name:             "NewACLBackendUpdate",
				factory:          NewACLBackendUpdate,
				wantType:         OperationUpdate,
				wantDescContains: "Update ACL 'is_api' in backend 'api'",
			},
			{
				name:             "NewACLBackendDelete",
				factory:          NewACLBackendDelete,
				wantType:         OperationDelete,
				wantDescContains: "Delete ACL 'is_api' from backend 'api'",
			},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				op := tt.factory("api", acl, 0)

				assert.Equal(t, tt.wantType, op.Type())
				assert.Equal(t, "acl", op.Section())
				assert.Contains(t, op.Describe(), tt.wantDescContains)
			})
		}
	})
}

func TestServerFactoryFunctions(t *testing.T) {
	server := &models.Server{Name: "web1"}

	tests := []struct {
		name             string
		factory          func(string, *models.Server) Operation
		wantType         OperationType
		wantDescContains string
	}{
		{
			name:             "NewServerCreate",
			factory:          NewServerCreate,
			wantType:         OperationCreate,
			wantDescContains: "Create server 'web1' in backend 'api'",
		},
		{
			name:             "NewServerDelete",
			factory:          NewServerDelete,
			wantType:         OperationDelete,
			wantDescContains: "Delete server 'web1' from backend 'api'",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			op := tt.factory("api", server)
			assertOperation(t, op, tt.wantType, "server", tt.wantDescContains)
		})
	}

	// NewServerUpdate has a different signature (current + desired) so it's tested separately.
	t.Run("NewServerUpdate", func(t *testing.T) {
		op := NewServerUpdate("api", server, server)
		assertOperation(t, op, OperationUpdate, "server", "Update server 'web1' in backend 'api'")
	})
}

func TestBindFactoryFunctions(t *testing.T) {
	bind := &models.Bind{Name: "http-bind"}

	tests := []struct {
		name             string
		factory          func(string, string, *models.Bind) Operation
		wantType         OperationType
		wantDescContains string
	}{
		{
			name:             "NewBindFrontendCreate",
			factory:          NewBindFrontendCreate,
			wantType:         OperationCreate,
			wantDescContains: "Create bind 'http-bind' in frontend 'http'",
		},
		{
			name:             "NewBindFrontendUpdate",
			factory:          NewBindFrontendUpdate,
			wantType:         OperationUpdate,
			wantDescContains: "Update bind 'http-bind' in frontend 'http'",
		},
		{
			name:             "NewBindFrontendDelete",
			factory:          NewBindFrontendDelete,
			wantType:         OperationDelete,
			wantDescContains: "Delete bind 'http-bind' from frontend 'http'",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			op := tt.factory("http", "http-bind", bind)
			assertOperation(t, op, tt.wantType, "bind", tt.wantDescContains)
		})
	}
}

func TestHTTPRequestRuleFactoryFunctions(t *testing.T) {
	rule := &models.HTTPRequestRule{}

	t.Run("frontend HTTP request rule operations", func(t *testing.T) {
		tests := []struct {
			name             string
			factory          func(string, *models.HTTPRequestRule, int) Operation
			wantType         OperationType
			wantDescContains string
		}{
			{
				name:             "NewHTTPRequestRuleFrontendCreate",
				factory:          NewHTTPRequestRuleFrontendCreate,
				wantType:         OperationCreate,
				wantDescContains: "Create HTTP request rule at index 5 in frontend 'http'",
			},
			{
				name:             "NewHTTPRequestRuleFrontendUpdate",
				factory:          NewHTTPRequestRuleFrontendUpdate,
				wantType:         OperationUpdate,
				wantDescContains: "Update HTTP request rule at index 5 in frontend 'http'",
			},
			{
				name:             "NewHTTPRequestRuleFrontendDelete",
				factory:          NewHTTPRequestRuleFrontendDelete,
				wantType:         OperationDelete,
				wantDescContains: "Delete HTTP request rule at index 5 from frontend 'http'",
			},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				op := tt.factory("http", rule, 5)

				assert.Equal(t, tt.wantType, op.Type())
				assert.Equal(t, "http_request_rule", op.Section())
				assert.Contains(t, op.Describe(), tt.wantDescContains)
			})
		}
	})

	t.Run("backend HTTP request rule operations", func(t *testing.T) {
		tests := []struct {
			name             string
			factory          func(string, *models.HTTPRequestRule, int) Operation
			wantType         OperationType
			wantDescContains string
		}{
			{
				name:             "NewHTTPRequestRuleBackendCreate",
				factory:          NewHTTPRequestRuleBackendCreate,
				wantType:         OperationCreate,
				wantDescContains: "Create HTTP request rule at index 3 in backend 'api'",
			},
			{
				name:             "NewHTTPRequestRuleBackendUpdate",
				factory:          NewHTTPRequestRuleBackendUpdate,
				wantType:         OperationUpdate,
				wantDescContains: "Update HTTP request rule at index 3 in backend 'api'",
			},
			{
				name:             "NewHTTPRequestRuleBackendDelete",
				factory:          NewHTTPRequestRuleBackendDelete,
				wantType:         OperationDelete,
				wantDescContains: "Delete HTTP request rule at index 3 from backend 'api'",
			},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				op := tt.factory("api", rule, 3)

				assert.Equal(t, tt.wantType, op.Type())
				assert.Equal(t, "http_request_rule", op.Section())
				assert.Contains(t, op.Describe(), tt.wantDescContains)
			})
		}
	})
}

func TestBackendSwitchingRuleFactoryFunctions(t *testing.T) {
	rule := &models.BackendSwitchingRule{}

	tests := []struct {
		name             string
		factory          func(string, *models.BackendSwitchingRule, int) Operation
		wantType         OperationType
		wantDescContains string
	}{
		{
			name:             "NewBackendSwitchingRuleFrontendCreate",
			factory:          NewBackendSwitchingRuleFrontendCreate,
			wantType:         OperationCreate,
			wantDescContains: "Create backend switching rule at index 0 in frontend 'http'",
		},
		{
			name:             "NewBackendSwitchingRuleFrontendUpdate",
			factory:          NewBackendSwitchingRuleFrontendUpdate,
			wantType:         OperationUpdate,
			wantDescContains: "Update backend switching rule at index 0 in frontend 'http'",
		},
		{
			name:             "NewBackendSwitchingRuleFrontendDelete",
			factory:          NewBackendSwitchingRuleFrontendDelete,
			wantType:         OperationDelete,
			wantDescContains: "Delete backend switching rule at index 0 from frontend 'http'",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			op := tt.factory("http", rule, 0)

			assert.Equal(t, tt.wantType, op.Type())
			assert.Equal(t, "backend_switching_rule", op.Section())
			assert.Contains(t, op.Describe(), tt.wantDescContains)
		})
	}
}

func TestUserFactoryFunctions(t *testing.T) {
	user := &models.User{Username: "admin"}

	tests := []struct {
		name             string
		factory          func(string, *models.User) Operation
		wantType         OperationType
		wantDescContains string
	}{
		{
			name:             "NewUserCreate",
			factory:          NewUserCreate,
			wantType:         OperationCreate,
			wantDescContains: "Create user 'admin' in userlist 'admins'",
		},
		{
			name:             "NewUserUpdate",
			factory:          NewUserUpdate,
			wantType:         OperationUpdate,
			wantDescContains: "Update user 'admin' in userlist 'admins'",
		},
		{
			name:             "NewUserDelete",
			factory:          NewUserDelete,
			wantType:         OperationDelete,
			wantDescContains: "Delete user 'admin' from userlist 'admins'",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			op := tt.factory("admins", user)

			assert.Equal(t, tt.wantType, op.Type())
			assert.Equal(t, "user", op.Section())
			assert.Contains(t, op.Describe(), tt.wantDescContains)
		})
	}
}

func TestCacheFactoryFunctions(t *testing.T) {
	cacheName := "my-cache"
	cache := &models.Cache{Name: &cacheName}

	tests := []struct {
		name             string
		factory          func(*models.Cache) Operation
		wantType         OperationType
		wantDescContains string
	}{
		{
			name:             "NewCacheCreate",
			factory:          NewCacheCreate,
			wantType:         OperationCreate,
			wantDescContains: "Create cache 'my-cache'",
		},
		{
			name:             "NewCacheUpdate",
			factory:          NewCacheUpdate,
			wantType:         OperationUpdate,
			wantDescContains: "Update cache 'my-cache'",
		},
		{
			name:             "NewCacheDelete",
			factory:          NewCacheDelete,
			wantType:         OperationDelete,
			wantDescContains: "Delete cache 'my-cache'",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			op := tt.factory(cache)

			assert.Equal(t, tt.wantType, op.Type())
			assert.Equal(t, "cache", op.Section())
			assert.Contains(t, op.Describe(), tt.wantDescContains)
		})
	}
}

func TestResolverFactoryFunctions(t *testing.T) {
	resolver := &models.Resolver{}
	resolver.Name = "dns-resolver"

	tests := []struct {
		name             string
		factory          func(*models.Resolver) Operation
		wantType         OperationType
		wantDescContains string
	}{
		{
			name:             "NewResolverCreate",
			factory:          NewResolverCreate,
			wantType:         OperationCreate,
			wantDescContains: "Create resolver 'dns-resolver'",
		},
		{
			name:             "NewResolverUpdate",
			factory:          NewResolverUpdate,
			wantType:         OperationUpdate,
			wantDescContains: "Update resolver 'dns-resolver'",
		},
		{
			name:             "NewResolverDelete",
			factory:          NewResolverDelete,
			wantType:         OperationDelete,
			wantDescContains: "Delete resolver 'dns-resolver'",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			op := tt.factory(resolver)

			assert.Equal(t, tt.wantType, op.Type())
			assert.Equal(t, "resolver", op.Section())
			assert.Contains(t, op.Describe(), tt.wantDescContains)
		})
	}
}

func TestNameserverFactoryFunctions(t *testing.T) {
	nameserver := &models.Nameserver{Name: "ns1"}

	tests := []struct {
		name             string
		factory          func(string, *models.Nameserver) Operation
		wantType         OperationType
		wantDescContains string
	}{
		{
			name:             "NewNameserverCreate",
			factory:          NewNameserverCreate,
			wantType:         OperationCreate,
			wantDescContains: "Create nameserver 'ns1' in resolvers section 'dns'",
		},
		{
			name:             "NewNameserverUpdate",
			factory:          NewNameserverUpdate,
			wantType:         OperationUpdate,
			wantDescContains: "Update nameserver 'ns1' in resolvers section 'dns'",
		},
		{
			name:             "NewNameserverDelete",
			factory:          NewNameserverDelete,
			wantType:         OperationDelete,
			wantDescContains: "Delete nameserver 'ns1' from resolvers section 'dns'",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			op := tt.factory("dns", nameserver)

			assert.Equal(t, tt.wantType, op.Type())
			assert.Equal(t, "nameserver", op.Section())
			assert.Contains(t, op.Describe(), tt.wantDescContains)
		})
	}
}

func TestHTTPResponseRuleFactoryFunctions(t *testing.T) {
	rule := &models.HTTPResponseRule{Type: "set-header"}

	t.Run("frontend HTTP response rule operations", func(t *testing.T) {
		tests := []struct {
			name             string
			factory          func(string, *models.HTTPResponseRule, int) Operation
			wantType         OperationType
			wantDescContains string
		}{
			{
				name:             "NewHTTPResponseRuleFrontendCreate",
				factory:          NewHTTPResponseRuleFrontendCreate,
				wantType:         OperationCreate,
				wantDescContains: "Create HTTP response rule",
			},
			{
				name:             "NewHTTPResponseRuleFrontendUpdate",
				factory:          NewHTTPResponseRuleFrontendUpdate,
				wantType:         OperationUpdate,
				wantDescContains: "Update HTTP response rule",
			},
			{
				name:             "NewHTTPResponseRuleFrontendDelete",
				factory:          NewHTTPResponseRuleFrontendDelete,
				wantType:         OperationDelete,
				wantDescContains: "Delete HTTP response rule",
			},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				op := tt.factory("http", rule, 5)

				assert.Equal(t, tt.wantType, op.Type())
				assert.Equal(t, "http_response_rule", op.Section())
				assert.Contains(t, op.Describe(), tt.wantDescContains)
			})
		}
	})

	t.Run("backend HTTP response rule operations", func(t *testing.T) {
		tests := []struct {
			name             string
			factory          func(string, *models.HTTPResponseRule, int) Operation
			wantType         OperationType
			wantDescContains string
		}{
			{
				name:             "NewHTTPResponseRuleBackendCreate",
				factory:          NewHTTPResponseRuleBackendCreate,
				wantType:         OperationCreate,
				wantDescContains: "Create HTTP response rule",
			},
			{
				name:             "NewHTTPResponseRuleBackendUpdate",
				factory:          NewHTTPResponseRuleBackendUpdate,
				wantType:         OperationUpdate,
				wantDescContains: "Update HTTP response rule",
			},
			{
				name:             "NewHTTPResponseRuleBackendDelete",
				factory:          NewHTTPResponseRuleBackendDelete,
				wantType:         OperationDelete,
				wantDescContains: "Delete HTTP response rule",
			},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				op := tt.factory("api", rule, 3)

				assert.Equal(t, tt.wantType, op.Type())
				assert.Equal(t, "http_response_rule", op.Section())
				assert.Contains(t, op.Describe(), tt.wantDescContains)
			})
		}
	})
}

func TestFilterFactoryFunctions(t *testing.T) {
	filter := &models.Filter{Type: "trace"}

	t.Run("frontend filter operations", func(t *testing.T) {
		tests := []struct {
			name             string
			factory          func(string, *models.Filter, int) Operation
			wantType         OperationType
			wantDescContains string
		}{
			{
				name:             "NewFilterFrontendCreate",
				factory:          NewFilterFrontendCreate,
				wantType:         OperationCreate,
				wantDescContains: "Create filter",
			},
			{
				name:             "NewFilterFrontendUpdate",
				factory:          NewFilterFrontendUpdate,
				wantType:         OperationUpdate,
				wantDescContains: "Update filter",
			},
			{
				name:             "NewFilterFrontendDelete",
				factory:          NewFilterFrontendDelete,
				wantType:         OperationDelete,
				wantDescContains: "Delete filter",
			},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				op := tt.factory("http", filter, 2)

				assert.Equal(t, tt.wantType, op.Type())
				assert.Equal(t, "filter", op.Section())
				assert.Contains(t, op.Describe(), tt.wantDescContains)
			})
		}
	})

	t.Run("backend filter operations", func(t *testing.T) {
		tests := []struct {
			name             string
			factory          func(string, *models.Filter, int) Operation
			wantType         OperationType
			wantDescContains string
		}{
			{
				name:             "NewFilterBackendCreate",
				factory:          NewFilterBackendCreate,
				wantType:         OperationCreate,
				wantDescContains: "Create filter",
			},
			{
				name:             "NewFilterBackendUpdate",
				factory:          NewFilterBackendUpdate,
				wantType:         OperationUpdate,
				wantDescContains: "Update filter",
			},
			{
				name:             "NewFilterBackendDelete",
				factory:          NewFilterBackendDelete,
				wantType:         OperationDelete,
				wantDescContains: "Delete filter",
			},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				op := tt.factory("api", filter, 1)

				assert.Equal(t, tt.wantType, op.Type())
				assert.Equal(t, "filter", op.Section())
				assert.Contains(t, op.Describe(), tt.wantDescContains)
			})
		}
	})
}

func TestLogTargetFactoryFunctions(t *testing.T) {
	logTarget := &models.LogTarget{Address: "127.0.0.1"}

	t.Run("frontend log target operations", func(t *testing.T) {
		tests := []struct {
			name             string
			factory          func(string, *models.LogTarget, int) Operation
			wantType         OperationType
			wantDescContains string
		}{
			{
				name:             "NewLogTargetFrontendCreate",
				factory:          NewLogTargetFrontendCreate,
				wantType:         OperationCreate,
				wantDescContains: "Create log target",
			},
			{
				name:             "NewLogTargetFrontendUpdate",
				factory:          NewLogTargetFrontendUpdate,
				wantType:         OperationUpdate,
				wantDescContains: "Update log target",
			},
			{
				name:             "NewLogTargetFrontendDelete",
				factory:          NewLogTargetFrontendDelete,
				wantType:         OperationDelete,
				wantDescContains: "Delete log target",
			},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				op := tt.factory("http", logTarget, 0)

				assert.Equal(t, tt.wantType, op.Type())
				assert.Equal(t, "log_target", op.Section())
				assert.Contains(t, op.Describe(), tt.wantDescContains)
			})
		}
	})

	t.Run("backend log target operations", func(t *testing.T) {
		tests := []struct {
			name             string
			factory          func(string, *models.LogTarget, int) Operation
			wantType         OperationType
			wantDescContains string
		}{
			{
				name:             "NewLogTargetBackendCreate",
				factory:          NewLogTargetBackendCreate,
				wantType:         OperationCreate,
				wantDescContains: "Create log target",
			},
			{
				name:             "NewLogTargetBackendUpdate",
				factory:          NewLogTargetBackendUpdate,
				wantType:         OperationUpdate,
				wantDescContains: "Update log target",
			},
			{
				name:             "NewLogTargetBackendDelete",
				factory:          NewLogTargetBackendDelete,
				wantType:         OperationDelete,
				wantDescContains: "Delete log target",
			},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				op := tt.factory("api", logTarget, 0)

				assert.Equal(t, tt.wantType, op.Type())
				assert.Equal(t, "log_target", op.Section())
				assert.Contains(t, op.Describe(), tt.wantDescContains)
			})
		}
	})
}

func TestServerTemplateFactoryFunctions(t *testing.T) {
	serverTemplate := &models.ServerTemplate{}
	serverTemplate.Prefix = "web"

	tests := []struct {
		name             string
		factory          func(string, *models.ServerTemplate) Operation
		wantType         OperationType
		wantDescContains string
	}{
		{
			name:             "NewServerTemplateCreate",
			factory:          NewServerTemplateCreate,
			wantType:         OperationCreate,
			wantDescContains: "Create server template 'web'",
		},
		{
			name:             "NewServerTemplateUpdate",
			factory:          NewServerTemplateUpdate,
			wantType:         OperationUpdate,
			wantDescContains: "Update server template 'web'",
		},
		{
			name:             "NewServerTemplateDelete",
			factory:          NewServerTemplateDelete,
			wantType:         OperationDelete,
			wantDescContains: "Delete server template 'web'",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			op := tt.factory("api", serverTemplate)

			assert.Equal(t, tt.wantType, op.Type())
			assert.Equal(t, "server_template", op.Section())
			assert.Contains(t, op.Describe(), tt.wantDescContains)
		})
	}
}

func TestMailerEntryFactoryFunctions(t *testing.T) {
	entry := &models.MailerEntry{Name: "smtp1"}

	tests := []struct {
		name             string
		factory          func(string, *models.MailerEntry) Operation
		wantType         OperationType
		wantDescContains string
	}{
		{
			name:             "NewMailerEntryCreate",
			factory:          NewMailerEntryCreate,
			wantType:         OperationCreate,
			wantDescContains: "Create mailer entry 'smtp1'",
		},
		{
			name:             "NewMailerEntryUpdate",
			factory:          NewMailerEntryUpdate,
			wantType:         OperationUpdate,
			wantDescContains: "Update mailer entry 'smtp1'",
		},
		{
			name:             "NewMailerEntryDelete",
			factory:          NewMailerEntryDelete,
			wantType:         OperationDelete,
			wantDescContains: "Delete mailer entry 'smtp1'",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			op := tt.factory("mailers1", entry)

			assert.Equal(t, tt.wantType, op.Type())
			assert.Equal(t, "mailer_entry", op.Section())
			assert.Contains(t, op.Describe(), tt.wantDescContains)
		})
	}
}

func TestPeerEntryFactoryFunctions(t *testing.T) {
	entry := &models.PeerEntry{Name: "peer1"}

	tests := []struct {
		name             string
		factory          func(string, *models.PeerEntry) Operation
		wantType         OperationType
		wantDescContains string
	}{
		{
			name:             "NewPeerEntryCreate",
			factory:          NewPeerEntryCreate,
			wantType:         OperationCreate,
			wantDescContains: "Create peer entry 'peer1'",
		},
		{
			name:             "NewPeerEntryUpdate",
			factory:          NewPeerEntryUpdate,
			wantType:         OperationUpdate,
			wantDescContains: "Update peer entry 'peer1'",
		},
		{
			name:             "NewPeerEntryDelete",
			factory:          NewPeerEntryDelete,
			wantType:         OperationDelete,
			wantDescContains: "Delete peer entry 'peer1'",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			op := tt.factory("mypeers", entry)

			assert.Equal(t, tt.wantType, op.Type())
			assert.Equal(t, "peer_entry", op.Section())
			assert.Contains(t, op.Describe(), tt.wantDescContains)
		})
	}
}

func TestHTTPErrorsSectionFactoryFunctions(t *testing.T) {
	section := &models.HTTPErrorsSection{Name: "custom-errors"}

	tests := []struct {
		name             string
		factory          func(*models.HTTPErrorsSection) Operation
		wantType         OperationType
		wantDescContains string
	}{
		{
			name:             "NewHTTPErrorsSectionCreate",
			factory:          NewHTTPErrorsSectionCreate,
			wantType:         OperationCreate,
			wantDescContains: "Create http-errors section 'custom-errors'",
		},
		{
			name:             "NewHTTPErrorsSectionUpdate",
			factory:          NewHTTPErrorsSectionUpdate,
			wantType:         OperationUpdate,
			wantDescContains: "Update http-errors section 'custom-errors'",
		},
		{
			name:             "NewHTTPErrorsSectionDelete",
			factory:          NewHTTPErrorsSectionDelete,
			wantType:         OperationDelete,
			wantDescContains: "Delete http-errors section 'custom-errors'",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			op := tt.factory(section)

			assert.Equal(t, tt.wantType, op.Type())
			assert.Equal(t, "http_errors", op.Section())
			assert.Contains(t, op.Describe(), tt.wantDescContains)
		})
	}
}

func TestLogForwardFactoryFunctions(t *testing.T) {
	logForward := &models.LogForward{
		LogForwardBase: models.LogForwardBase{Name: "syslogs"},
	}

	tests := []struct {
		name             string
		factory          func(*models.LogForward) Operation
		wantType         OperationType
		wantDescContains string
	}{
		{
			name:             "NewLogForwardCreate",
			factory:          NewLogForwardCreate,
			wantType:         OperationCreate,
			wantDescContains: "Create log-forward 'syslogs'",
		},
		{
			name:             "NewLogForwardUpdate",
			factory:          NewLogForwardUpdate,
			wantType:         OperationUpdate,
			wantDescContains: "Update log-forward 'syslogs'",
		},
		{
			name:             "NewLogForwardDelete",
			factory:          NewLogForwardDelete,
			wantType:         OperationDelete,
			wantDescContains: "Delete log-forward 'syslogs'",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			op := tt.factory(logForward)

			assert.Equal(t, tt.wantType, op.Type())
			assert.Equal(t, "log_forward", op.Section())
			assert.Contains(t, op.Describe(), tt.wantDescContains)
		})
	}
}

func TestMailersSectionFactoryFunctions(t *testing.T) {
	section := &models.MailersSection{
		MailersSectionBase: models.MailersSectionBase{Name: "mailers1"},
	}

	tests := []struct {
		name             string
		factory          func(*models.MailersSection) Operation
		wantType         OperationType
		wantDescContains string
	}{
		{
			name:             "NewMailersSectionCreate",
			factory:          NewMailersSectionCreate,
			wantType:         OperationCreate,
			wantDescContains: "Create mailers 'mailers1'",
		},
		{
			name:             "NewMailersSectionUpdate",
			factory:          NewMailersSectionUpdate,
			wantType:         OperationUpdate,
			wantDescContains: "Update mailers 'mailers1'",
		},
		{
			name:             "NewMailersSectionDelete",
			factory:          NewMailersSectionDelete,
			wantType:         OperationDelete,
			wantDescContains: "Delete mailers 'mailers1'",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			op := tt.factory(section)

			assert.Equal(t, tt.wantType, op.Type())
			assert.Equal(t, "mailers", op.Section())
			assert.Contains(t, op.Describe(), tt.wantDescContains)
		})
	}
}

func TestPeerSectionFactoryFunctions(t *testing.T) {
	section := &models.PeerSection{
		PeerSectionBase: models.PeerSectionBase{Name: "mypeers"},
	}

	tests := []struct {
		name             string
		factory          func(*models.PeerSection) Operation
		wantType         OperationType
		wantDescContains string
	}{
		{
			name:             "NewPeerSectionCreate",
			factory:          NewPeerSectionCreate,
			wantType:         OperationCreate,
			wantDescContains: "Create peer section 'mypeers'",
		},
		{
			name:             "NewPeerSectionUpdate",
			factory:          NewPeerSectionUpdate,
			wantType:         OperationUpdate,
			wantDescContains: "Update peer section 'mypeers'",
		},
		{
			name:             "NewPeerSectionDelete",
			factory:          NewPeerSectionDelete,
			wantType:         OperationDelete,
			wantDescContains: "Delete peer section 'mypeers'",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			op := tt.factory(section)

			assert.Equal(t, tt.wantType, op.Type())
			assert.Equal(t, "peers", op.Section())
			assert.Contains(t, op.Describe(), tt.wantDescContains)
		})
	}
}

func TestProgramFactoryFunctions(t *testing.T) {
	program := &models.Program{Name: "myprogram"}

	tests := []struct {
		name             string
		factory          func(*models.Program) Operation
		wantType         OperationType
		wantDescContains string
	}{
		{
			name:             "NewProgramCreate",
			factory:          NewProgramCreate,
			wantType:         OperationCreate,
			wantDescContains: "Create program 'myprogram'",
		},
		{
			name:             "NewProgramUpdate",
			factory:          NewProgramUpdate,
			wantType:         OperationUpdate,
			wantDescContains: "Update program 'myprogram'",
		},
		{
			name:             "NewProgramDelete",
			factory:          NewProgramDelete,
			wantType:         OperationDelete,
			wantDescContains: "Delete program 'myprogram'",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			op := tt.factory(program)

			assert.Equal(t, tt.wantType, op.Type())
			assert.Equal(t, "program", op.Section())
			assert.Contains(t, op.Describe(), tt.wantDescContains)
		})
	}
}

func TestRingFactoryFunctions(t *testing.T) {
	ring := &models.Ring{
		RingBase: models.RingBase{Name: "myring"},
	}

	tests := []struct {
		name             string
		factory          func(*models.Ring) Operation
		wantType         OperationType
		wantDescContains string
	}{
		{
			name:             "NewRingCreate",
			factory:          NewRingCreate,
			wantType:         OperationCreate,
			wantDescContains: "Create ring 'myring'",
		},
		{
			name:             "NewRingUpdate",
			factory:          NewRingUpdate,
			wantType:         OperationUpdate,
			wantDescContains: "Update ring 'myring'",
		},
		{
			name:             "NewRingDelete",
			factory:          NewRingDelete,
			wantType:         OperationDelete,
			wantDescContains: "Delete ring 'myring'",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			op := tt.factory(ring)

			assert.Equal(t, tt.wantType, op.Type())
			assert.Equal(t, "ring", op.Section())
			assert.Contains(t, op.Describe(), tt.wantDescContains)
		})
	}
}

func TestCrtStoreFactoryFunctions(t *testing.T) {
	crtStore := &models.CrtStore{CrtStoreBase: models.CrtStoreBase{Name: "my-certs"}}

	tests := []struct {
		name             string
		factory          func(*models.CrtStore) Operation
		wantType         OperationType
		wantDescContains string
	}{
		{
			name:             "NewCrtStoreCreate",
			factory:          NewCrtStoreCreate,
			wantType:         OperationCreate,
			wantDescContains: "Create crt-store 'my-certs'",
		},
		{
			name:             "NewCrtStoreUpdate",
			factory:          NewCrtStoreUpdate,
			wantType:         OperationUpdate,
			wantDescContains: "Update crt-store 'my-certs'",
		},
		{
			name:             "NewCrtStoreDelete",
			factory:          NewCrtStoreDelete,
			wantType:         OperationDelete,
			wantDescContains: "Delete crt-store 'my-certs'",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			op := tt.factory(crtStore)

			assert.Equal(t, tt.wantType, op.Type())
			assert.Equal(t, "crt_store", op.Section())
			assert.Contains(t, op.Describe(), tt.wantDescContains)
		})
	}
}

func TestUserlistFactoryFunctions(t *testing.T) {
	userlist := &models.Userlist{
		UserlistBase: models.UserlistBase{Name: "admins"},
	}

	tests := []struct {
		name             string
		factory          func(*models.Userlist) Operation
		wantType         OperationType
		wantDescContains string
	}{
		{
			name:             "NewUserlistCreate",
			factory:          NewUserlistCreate,
			wantType:         OperationCreate,
			wantDescContains: "Create userlist 'admins'",
		},
		{
			name:             "NewUserlistDelete",
			factory:          NewUserlistDelete,
			wantType:         OperationDelete,
			wantDescContains: "Delete userlist 'admins'",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			op := tt.factory(userlist)

			assert.Equal(t, tt.wantType, op.Type())
			assert.Equal(t, "userlist", op.Section())
			assert.Contains(t, op.Describe(), tt.wantDescContains)
		})
	}
}

func TestFCGIAppFactoryFunctions(t *testing.T) {
	fcgiApp := &models.FCGIApp{
		FCGIAppBase: models.FCGIAppBase{Name: "php-fpm"},
	}

	tests := []struct {
		name             string
		factory          func(*models.FCGIApp) Operation
		wantType         OperationType
		wantDescContains string
	}{
		{
			name:             "NewFCGIAppCreate",
			factory:          NewFCGIAppCreate,
			wantType:         OperationCreate,
			wantDescContains: "Create fcgi-app 'php-fpm'",
		},
		{
			name:             "NewFCGIAppUpdate",
			factory:          NewFCGIAppUpdate,
			wantType:         OperationUpdate,
			wantDescContains: "Update fcgi-app 'php-fpm'",
		},
		{
			name:             "NewFCGIAppDelete",
			factory:          NewFCGIAppDelete,
			wantType:         OperationDelete,
			wantDescContains: "Delete fcgi-app 'php-fpm'",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			op := tt.factory(fcgiApp)

			assert.Equal(t, tt.wantType, op.Type())
			assert.Equal(t, "fcgi_app", op.Section())
			assert.Contains(t, op.Describe(), tt.wantDescContains)
		})
	}
}

func TestTCPRequestRuleFactoryFunctions(t *testing.T) {
	rule := &models.TCPRequestRule{Type: "inspect-delay"}

	t.Run("frontend TCP request rule operations", func(t *testing.T) {
		tests := []struct {
			name             string
			factory          func(string, *models.TCPRequestRule, int) Operation
			wantType         OperationType
			wantDescContains string
		}{
			{
				name:             "NewTCPRequestRuleFrontendCreate",
				factory:          NewTCPRequestRuleFrontendCreate,
				wantType:         OperationCreate,
				wantDescContains: "Create TCP request rule",
			},
			{
				name:             "NewTCPRequestRuleFrontendUpdate",
				factory:          NewTCPRequestRuleFrontendUpdate,
				wantType:         OperationUpdate,
				wantDescContains: "Update TCP request rule",
			},
			{
				name:             "NewTCPRequestRuleFrontendDelete",
				factory:          NewTCPRequestRuleFrontendDelete,
				wantType:         OperationDelete,
				wantDescContains: "Delete TCP request rule",
			},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				op := tt.factory("tcp", rule, 0)

				assert.Equal(t, tt.wantType, op.Type())
				assert.Equal(t, "tcp_request_rule", op.Section())
				assert.Contains(t, op.Describe(), tt.wantDescContains)
			})
		}
	})

	t.Run("backend TCP request rule operations", func(t *testing.T) {
		tests := []struct {
			name             string
			factory          func(string, *models.TCPRequestRule, int) Operation
			wantType         OperationType
			wantDescContains string
		}{
			{
				name:             "NewTCPRequestRuleBackendCreate",
				factory:          NewTCPRequestRuleBackendCreate,
				wantType:         OperationCreate,
				wantDescContains: "Create TCP request rule",
			},
			{
				name:             "NewTCPRequestRuleBackendUpdate",
				factory:          NewTCPRequestRuleBackendUpdate,
				wantType:         OperationUpdate,
				wantDescContains: "Update TCP request rule",
			},
			{
				name:             "NewTCPRequestRuleBackendDelete",
				factory:          NewTCPRequestRuleBackendDelete,
				wantType:         OperationDelete,
				wantDescContains: "Delete TCP request rule",
			},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				op := tt.factory("api", rule, 0)

				assert.Equal(t, tt.wantType, op.Type())
				assert.Equal(t, "tcp_request_rule", op.Section())
				assert.Contains(t, op.Describe(), tt.wantDescContains)
			})
		}
	})
}

func TestTCPResponseRuleFactoryFunctions(t *testing.T) {
	rule := &models.TCPResponseRule{Type: "content"}

	tests := []struct {
		name             string
		factory          func(string, *models.TCPResponseRule, int) Operation
		wantType         OperationType
		wantDescContains string
	}{
		{
			name:             "NewTCPResponseRuleBackendCreate",
			factory:          NewTCPResponseRuleBackendCreate,
			wantType:         OperationCreate,
			wantDescContains: "Create TCP response rule",
		},
		{
			name:             "NewTCPResponseRuleBackendUpdate",
			factory:          NewTCPResponseRuleBackendUpdate,
			wantType:         OperationUpdate,
			wantDescContains: "Update TCP response rule",
		},
		{
			name:             "NewTCPResponseRuleBackendDelete",
			factory:          NewTCPResponseRuleBackendDelete,
			wantType:         OperationDelete,
			wantDescContains: "Delete TCP response rule",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			op := tt.factory("api", rule, 0)

			assert.Equal(t, tt.wantType, op.Type())
			assert.Equal(t, "tcp_response_rule", op.Section())
			assert.Contains(t, op.Describe(), tt.wantDescContains)
		})
	}
}

func TestStickRuleFactoryFunctions(t *testing.T) {
	rule := &models.StickRule{Type: "store-request"}

	tests := []struct {
		name             string
		factory          func(string, *models.StickRule, int) Operation
		wantType         OperationType
		wantDescContains string
	}{
		{
			name:             "NewStickRuleBackendCreate",
			factory:          NewStickRuleBackendCreate,
			wantType:         OperationCreate,
			wantDescContains: "Create stick rule",
		},
		{
			name:             "NewStickRuleBackendUpdate",
			factory:          NewStickRuleBackendUpdate,
			wantType:         OperationUpdate,
			wantDescContains: "Update stick rule",
		},
		{
			name:             "NewStickRuleBackendDelete",
			factory:          NewStickRuleBackendDelete,
			wantType:         OperationDelete,
			wantDescContains: "Delete stick rule",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			op := tt.factory("api", rule, 0)

			assert.Equal(t, tt.wantType, op.Type())
			assert.Equal(t, "stick_rule", op.Section())
			assert.Contains(t, op.Describe(), tt.wantDescContains)
		})
	}
}

func TestHTTPAfterResponseRuleFactoryFunctions(t *testing.T) {
	rule := &models.HTTPAfterResponseRule{Type: "set-header"}

	tests := []struct {
		name             string
		factory          func(string, *models.HTTPAfterResponseRule, int) Operation
		wantType         OperationType
		wantDescContains string
	}{
		{
			name:             "NewHTTPAfterResponseRuleBackendCreate",
			factory:          NewHTTPAfterResponseRuleBackendCreate,
			wantType:         OperationCreate,
			wantDescContains: "Create HTTP after response rule",
		},
		{
			name:             "NewHTTPAfterResponseRuleBackendUpdate",
			factory:          NewHTTPAfterResponseRuleBackendUpdate,
			wantType:         OperationUpdate,
			wantDescContains: "Update HTTP after response rule",
		},
		{
			name:             "NewHTTPAfterResponseRuleBackendDelete",
			factory:          NewHTTPAfterResponseRuleBackendDelete,
			wantType:         OperationDelete,
			wantDescContains: "Delete HTTP after response rule",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			op := tt.factory("api", rule, 0)

			assert.Equal(t, tt.wantType, op.Type())
			assert.Equal(t, "http_after_response_rule", op.Section())
			assert.Contains(t, op.Describe(), tt.wantDescContains)
		})
	}
}

func TestServerSwitchingRuleFactoryFunctions(t *testing.T) {
	rule := &models.ServerSwitchingRule{TargetServer: "srv1"}

	tests := []struct {
		name             string
		factory          func(string, *models.ServerSwitchingRule, int) Operation
		wantType         OperationType
		wantDescContains string
	}{
		{
			name:             "NewServerSwitchingRuleBackendCreate",
			factory:          NewServerSwitchingRuleBackendCreate,
			wantType:         OperationCreate,
			wantDescContains: "Create server switching rule",
		},
		{
			name:             "NewServerSwitchingRuleBackendUpdate",
			factory:          NewServerSwitchingRuleBackendUpdate,
			wantType:         OperationUpdate,
			wantDescContains: "Update server switching rule",
		},
		{
			name:             "NewServerSwitchingRuleBackendDelete",
			factory:          NewServerSwitchingRuleBackendDelete,
			wantType:         OperationDelete,
			wantDescContains: "Delete server switching rule",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			op := tt.factory("api", rule, 0)

			assert.Equal(t, tt.wantType, op.Type())
			assert.Equal(t, "server_switching_rule", op.Section())
			assert.Contains(t, op.Describe(), tt.wantDescContains)
		})
	}
}

func TestHTTPCheckFactoryFunctions(t *testing.T) {
	check := &models.HTTPCheck{Type: "send"}

	tests := []struct {
		name             string
		factory          func(string, *models.HTTPCheck, int) Operation
		wantType         OperationType
		wantDescContains string
	}{
		{
			name:             "NewHTTPCheckBackendCreate",
			factory:          NewHTTPCheckBackendCreate,
			wantType:         OperationCreate,
			wantDescContains: "Create HTTP check",
		},
		{
			name:             "NewHTTPCheckBackendUpdate",
			factory:          NewHTTPCheckBackendUpdate,
			wantType:         OperationUpdate,
			wantDescContains: "Update HTTP check",
		},
		{
			name:             "NewHTTPCheckBackendDelete",
			factory:          NewHTTPCheckBackendDelete,
			wantType:         OperationDelete,
			wantDescContains: "Delete HTTP check",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			op := tt.factory("api", check, 0)

			assert.Equal(t, tt.wantType, op.Type())
			assert.Equal(t, "http_check", op.Section())
			assert.Contains(t, op.Describe(), tt.wantDescContains)
		})
	}
}

func TestTCPCheckFactoryFunctions(t *testing.T) {
	check := &models.TCPCheck{Action: "connect"}

	tests := []struct {
		name             string
		factory          func(string, *models.TCPCheck, int) Operation
		wantType         OperationType
		wantDescContains string
	}{
		{
			name:             "NewTCPCheckBackendCreate",
			factory:          NewTCPCheckBackendCreate,
			wantType:         OperationCreate,
			wantDescContains: "Create TCP check",
		},
		{
			name:             "NewTCPCheckBackendUpdate",
			factory:          NewTCPCheckBackendUpdate,
			wantType:         OperationUpdate,
			wantDescContains: "Update TCP check",
		},
		{
			name:             "NewTCPCheckBackendDelete",
			factory:          NewTCPCheckBackendDelete,
			wantType:         OperationDelete,
			wantDescContains: "Delete TCP check",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			op := tt.factory("api", check, 0)

			assert.Equal(t, tt.wantType, op.Type())
			assert.Equal(t, "tcp_check", op.Section())
			assert.Contains(t, op.Describe(), tt.wantDescContains)
		})
	}
}

func TestCaptureFactoryFunctions(t *testing.T) {
	capture := &models.Capture{Type: "request"}

	tests := []struct {
		name             string
		factory          func(string, *models.Capture, int) Operation
		wantType         OperationType
		wantDescContains string
	}{
		{
			name:             "NewCaptureFrontendCreate",
			factory:          NewCaptureFrontendCreate,
			wantType:         OperationCreate,
			wantDescContains: "Create capture",
		},
		{
			name:             "NewCaptureFrontendUpdate",
			factory:          NewCaptureFrontendUpdate,
			wantType:         OperationUpdate,
			wantDescContains: "Update capture",
		},
		{
			name:             "NewCaptureFrontendDelete",
			factory:          NewCaptureFrontendDelete,
			wantType:         OperationDelete,
			wantDescContains: "Delete capture",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			op := tt.factory("http", capture, 0)

			assert.Equal(t, tt.wantType, op.Type())
			assert.Equal(t, "capture", op.Section())
			assert.Contains(t, op.Describe(), tt.wantDescContains)
		})
	}
}

func TestDescribeHelperFunctions(t *testing.T) {
	t.Run("bindIdentifier", func(t *testing.T) {
		ptrInt64 := func(i int64) *int64 { return &i }

		tests := []struct {
			name         string
			opType       OperationType
			bind         *models.Bind
			frontendName string
			wantContains []string
		}{
			{
				name:   "bind with SSL and certificate",
				opType: OperationCreate,
				bind: &models.Bind{
					Name:    "https-bind",
					Address: "*",
					Port:    ptrInt64(443),
					BindParams: models.BindParams{
						Ssl:            true,
						SslCertificate: "/etc/ssl/cert.pem",
					},
				},
				frontendName: "https",
				wantContains: []string{"Create bind", "*:443", "ssl", "crt /etc/ssl/cert.pem"},
			},
			{
				name:   "bind without SSL",
				opType: OperationUpdate,
				bind: &models.Bind{
					Name:    "http-bind",
					Address: "*",
					Port:    ptrInt64(80),
				},
				frontendName: "http",
				wantContains: []string{"Update bind", "*:80"},
			},
			{
				name:   "bind with empty name",
				opType: OperationDelete,
				bind: &models.Bind{
					Address: "192.168.1.1",
					Port:    ptrInt64(8080),
				},
				frontendName: "http",
				wantContains: []string{"Delete bind", "192.168.1.1:8080"},
			},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				got := DescribeNamedChild(tt.opType, "bind", bindIdentifier(tt.bind), "frontend", tt.frontendName)()
				for _, want := range tt.wantContains {
					assert.Contains(t, got, want)
				}
			})
		}
	})

	t.Run("DescribeTypedChild_logTarget", func(t *testing.T) {
		logTarget := &models.LogTarget{Address: "127.0.0.1", Facility: "local0"}

		desc := DescribeTypedChild(OperationCreate, "log target", logTargetIdentifier(logTarget), "at index 0", "frontend", "http")()
		assert.Contains(t, desc, "Create log target")
		assert.Contains(t, desc, "frontend 'http'")
	})

	t.Run("DescribeTypedChild_filter", func(t *testing.T) {
		filter := &models.Filter{Type: "trace"}

		desc := DescribeTypedChild(OperationUpdate, "filter", filterIdentifier(filter), "at index 0", "backend", "api")()
		assert.Contains(t, desc, "Update filter")
		assert.Contains(t, desc, "trace")
		assert.Contains(t, desc, "backend 'api'")
	})

	t.Run("DescribeTypedChild_capture", func(t *testing.T) {
		capture := &models.Capture{Type: "request"}

		desc := DescribeTypedChild(OperationDelete, "capture", captureIdentifier(capture), "at index 0", "frontend", "http")()
		assert.Contains(t, desc, "Delete capture")
		assert.Contains(t, desc, "request")
		assert.Contains(t, desc, "frontend 'http'")
	})

	t.Run("DescribeTypedChild_tcpRequestRule", func(t *testing.T) {
		rule := &models.TCPRequestRule{Type: "inspect-delay"}

		desc := DescribeTypedChild(OperationCreate, "TCP request rule", tcpRequestRuleIdentifier(rule), "at index 0", "frontend", "tcp")()
		assert.Contains(t, desc, "Create TCP request rule")
		assert.Contains(t, desc, "inspect-delay")
		assert.Contains(t, desc, "frontend 'tcp'")
	})

	t.Run("DescribeTypedChild_tcpResponseRule", func(t *testing.T) {
		rule := &models.TCPResponseRule{Type: "content"}

		desc := DescribeTypedChild(OperationUpdate, "TCP response rule", tcpResponseRuleIdentifier(rule), "at index 0", "backend", "api")()
		assert.Contains(t, desc, "Update TCP response rule")
		assert.Contains(t, desc, "content")
		assert.Contains(t, desc, "backend 'api'")
	})

	t.Run("DescribeTypedChild_httpCheck", func(t *testing.T) {
		check := &models.HTTPCheck{Type: "send"}

		desc := DescribeTypedChild(OperationDelete, "HTTP check", httpCheckIdentifier(check), "at index 0", "backend", "api")()
		assert.Contains(t, desc, "Delete HTTP check")
		assert.Contains(t, desc, "send")
		assert.Contains(t, desc, "backend 'api'")
	})

	t.Run("DescribeTypedChild_tcpCheck", func(t *testing.T) {
		check := &models.TCPCheck{Action: "connect"}

		desc := DescribeTypedChild(OperationCreate, "TCP check", tcpCheckIdentifier(check), "at index 0", "backend", "api")()
		assert.Contains(t, desc, "Create TCP check")
		assert.Contains(t, desc, "connect")
		assert.Contains(t, desc, "backend 'api'")
	})

	t.Run("DescribeTypedChild_stickRule", func(t *testing.T) {
		rule := &models.StickRule{Type: "store-request"}

		desc := DescribeTypedChild(OperationUpdate, "stick rule", stickRuleIdentifier(rule), "at index 0", "backend", "api")()
		assert.Contains(t, desc, "Update stick rule")
		assert.Contains(t, desc, "store-request")
		assert.Contains(t, desc, "backend 'api'")
	})

	t.Run("DescribeTypedChild_serverSwitchingRule", func(t *testing.T) {
		rule := &models.ServerSwitchingRule{TargetServer: "srv1"}

		desc := DescribeTypedChild(OperationDelete, "server switching rule", serverSwitchingRuleIdentifier(rule), "at index 0", "backend", "api")()
		assert.Contains(t, desc, "Delete server switching rule")
		assert.Contains(t, desc, "srv1")
		assert.Contains(t, desc, "backend 'api'")
	})

	t.Run("DescribeTypedChild_httpAfterResponseRule", func(t *testing.T) {
		rule := &models.HTTPAfterResponseRule{Type: "set-header"}

		desc := DescribeTypedChild(OperationCreate, "HTTP after response rule", httpAfterResponseRuleIdentifier(rule), "at index 0", "backend", "api")()
		assert.Contains(t, desc, "Create HTTP after response rule")
		assert.Contains(t, desc, "set-header")
		assert.Contains(t, desc, "backend 'api'")
	})

	// Test DescribeTypedChild with all operation types
	t.Run("DescribeTypedChild_httpRequestRule_all_ops", func(t *testing.T) {
		rule := &models.HTTPRequestRule{Type: "add-header"}

		tests := []struct {
			opType       OperationType
			wantContains string
		}{
			{OperationCreate, "Create HTTP request rule"},
			{OperationUpdate, "Update HTTP request rule"},
			{OperationDelete, "Delete HTTP request rule"},
			{OperationType(99), "Process HTTP request rule"},
		}

		for _, tt := range tests {
			desc := DescribeTypedChild(tt.opType, "HTTP request rule", httpRequestRuleIdentifier(rule), "at index 0", "frontend", "http")()
			assert.Contains(t, desc, tt.wantContains)
			assert.Contains(t, desc, "add-header")
		}
	})

	// Test DescribeTypedChild with empty identifier - uses fallback
	t.Run("DescribeTypedChild_httpRequestRule_empty_type", func(t *testing.T) {
		rule := &models.HTTPRequestRule{}
		desc := DescribeTypedChild(OperationCreate, "HTTP request rule", httpRequestRuleIdentifier(rule), "at index 5", "frontend", "http")()
		assert.Contains(t, desc, "at index 5")
	})

	// Test DescribeTypedChild for HTTP response rule
	t.Run("DescribeTypedChild_httpResponseRule_all_ops", func(t *testing.T) {
		rule := &models.HTTPResponseRule{Type: "set-header"}

		tests := []struct {
			opType       OperationType
			wantContains string
		}{
			{OperationCreate, "Create HTTP response rule"},
			{OperationUpdate, "Update HTTP response rule"},
			{OperationDelete, "Delete HTTP response rule"},
			{OperationType(99), "Process HTTP response rule"},
		}

		for _, tt := range tests {
			desc := DescribeTypedChild(tt.opType, "HTTP response rule", httpResponseRuleIdentifier(rule), "", "backend", "api")()
			assert.Contains(t, desc, tt.wantContains)
		}
	})

	// Test DescribeTypedChild for backend switching rule
	t.Run("DescribeTypedChild_backendSwitchingRule_all_ops", func(t *testing.T) {
		rule := &models.BackendSwitchingRule{Name: "api_backend"}

		tests := []struct {
			opType       OperationType
			wantContains string
		}{
			{OperationCreate, "Create backend switching rule"},
			{OperationUpdate, "Update backend switching rule"},
			{OperationDelete, "Delete backend switching rule"},
			{OperationType(99), "Process backend switching rule"},
		}

		for _, tt := range tests {
			desc := DescribeTypedChild(tt.opType, "backend switching rule", backendSwitchingRuleIdentifier(rule), "at index 0", "frontend", "http")()
			assert.Contains(t, desc, tt.wantContains)
			assert.Contains(t, desc, "api_backend")
		}
	})

	// Test DescribeTypedChild with empty identifier - uses index fallback
	t.Run("DescribeTypedChild_backendSwitchingRule_empty_name", func(t *testing.T) {
		rule := &models.BackendSwitchingRule{}
		desc := DescribeTypedChild(OperationCreate, "backend switching rule", backendSwitchingRuleIdentifier(rule), "at index 3", "frontend", "http")()
		assert.Contains(t, desc, "at index 3")
	})

	// Test empty identifier branches - fallback to index
	t.Run("DescribeTypedChild_logTarget_empty", func(t *testing.T) {
		logTarget := &models.LogTarget{}
		desc := DescribeTypedChild(OperationCreate, "log target", logTargetIdentifier(logTarget), "at index 5", "frontend", "http")()
		assert.Contains(t, desc, "at index 5")
	})

	t.Run("DescribeTypedChild_filter_empty", func(t *testing.T) {
		filter := &models.Filter{}
		desc := DescribeTypedChild(OperationCreate, "filter", filterIdentifier(filter), "at index 3", "backend", "api")()
		assert.Contains(t, desc, "at index 3")
	})

	t.Run("DescribeTypedChild_capture_empty", func(t *testing.T) {
		capture := &models.Capture{}
		desc := DescribeTypedChild(OperationCreate, "capture", captureIdentifier(capture), "at index 2", "frontend", "http")()
		assert.Contains(t, desc, "at index 2")
	})

	t.Run("DescribeTypedChild_tcpRequestRule_empty", func(t *testing.T) {
		rule := &models.TCPRequestRule{}
		desc := DescribeTypedChild(OperationCreate, "TCP request rule", tcpRequestRuleIdentifier(rule), "at index 4", "frontend", "tcp")()
		assert.Contains(t, desc, "at index 4")
	})

	t.Run("DescribeTypedChild_tcpResponseRule_empty", func(t *testing.T) {
		rule := &models.TCPResponseRule{}
		desc := DescribeTypedChild(OperationCreate, "TCP response rule", tcpResponseRuleIdentifier(rule), "at index 1", "backend", "api")()
		assert.Contains(t, desc, "at index 1")
	})

	t.Run("DescribeTypedChild_httpCheck_empty", func(t *testing.T) {
		check := &models.HTTPCheck{}
		desc := DescribeTypedChild(OperationCreate, "HTTP check", httpCheckIdentifier(check), "at index 6", "backend", "api")()
		assert.Contains(t, desc, "at index 6")
	})

	t.Run("DescribeTypedChild_tcpCheck_empty", func(t *testing.T) {
		check := &models.TCPCheck{}
		desc := DescribeTypedChild(OperationCreate, "TCP check", tcpCheckIdentifier(check), "at index 7", "backend", "api")()
		assert.Contains(t, desc, "at index 7")
	})

	t.Run("DescribeTypedChild_stickRule_empty", func(t *testing.T) {
		rule := &models.StickRule{}
		desc := DescribeTypedChild(OperationCreate, "stick rule", stickRuleIdentifier(rule), "at index 8", "backend", "api")()
		assert.Contains(t, desc, "at index 8")
	})

	t.Run("DescribeTypedChild_serverSwitchingRule_empty", func(t *testing.T) {
		rule := &models.ServerSwitchingRule{}
		desc := DescribeTypedChild(OperationCreate, "server switching rule", serverSwitchingRuleIdentifier(rule), "at index 9", "backend", "api")()
		assert.Contains(t, desc, "at index 9")
	})

	t.Run("DescribeTypedChild_httpAfterResponseRule_empty", func(t *testing.T) {
		rule := &models.HTTPAfterResponseRule{}
		desc := DescribeTypedChild(OperationCreate, "HTTP after response rule", httpAfterResponseRuleIdentifier(rule), "at index 10", "backend", "api")()
		assert.Contains(t, desc, "at index 10")
	})

	// Test DescribeNamedChild for bind with unknown operation type
	t.Run("bindIdentifier_unknown_op", func(t *testing.T) {
		ptrInt64 := func(i int64) *int64 { return &i }
		bind := &models.Bind{
			Address: "*",
			Port:    ptrInt64(80),
		}
		desc := DescribeNamedChild(OperationType(99), "bind", bindIdentifier(bind), "frontend", "http")()
		assert.Contains(t, desc, "Process bind")
	})
}

func TestNameExtractors(t *testing.T) {
	t.Run("BackendName", func(t *testing.T) {
		b := &models.Backend{BackendBase: models.BackendBase{Name: "my-backend"}}
		assert.Equal(t, "my-backend", BackendName(b))
	})

	t.Run("FrontendName", func(t *testing.T) {
		f := &models.Frontend{FrontendBase: models.FrontendBase{Name: "my-frontend"}}
		assert.Equal(t, "my-frontend", FrontendName(f))
	})

	t.Run("DefaultsName", func(t *testing.T) {
		d := &models.Defaults{DefaultsBase: models.DefaultsBase{Name: "my-defaults"}}
		assert.Equal(t, "my-defaults", DefaultsName(d))
	})

	t.Run("HTTPErrorsSectionName", func(t *testing.T) {
		h := &models.HTTPErrorsSection{Name: "errors"}
		assert.Equal(t, "errors", HTTPErrorsSectionName(h))
	})

	t.Run("LogForwardName", func(t *testing.T) {
		l := &models.LogForward{LogForwardBase: models.LogForwardBase{Name: "logs"}}
		assert.Equal(t, "logs", LogForwardName(l))
	})

	t.Run("MailersSectionName", func(t *testing.T) {
		m := &models.MailersSection{MailersSectionBase: models.MailersSectionBase{Name: "mailers"}}
		assert.Equal(t, "mailers", MailersSectionName(m))
	})

	t.Run("PeerSectionName", func(t *testing.T) {
		p := &models.PeerSection{PeerSectionBase: models.PeerSectionBase{Name: "peers"}}
		assert.Equal(t, "peers", PeerSectionName(p))
	})

	t.Run("ProgramName", func(t *testing.T) {
		p := &models.Program{Name: "prog"}
		assert.Equal(t, "prog", ProgramName(p))
	})

	t.Run("ResolverName", func(t *testing.T) {
		r := &models.Resolver{ResolverBase: models.ResolverBase{Name: "resolver"}}
		assert.Equal(t, "resolver", ResolverName(r))
	})

	t.Run("RingName", func(t *testing.T) {
		r := &models.Ring{RingBase: models.RingBase{Name: "ring"}}
		assert.Equal(t, "ring", RingName(r))
	})

	t.Run("CrtStoreName", func(t *testing.T) {
		c := &models.CrtStore{CrtStoreBase: models.CrtStoreBase{Name: "store"}}
		assert.Equal(t, "store", CrtStoreName(c))
	})

	t.Run("UserlistName", func(t *testing.T) {
		u := &models.Userlist{UserlistBase: models.UserlistBase{Name: "users"}}
		assert.Equal(t, "users", UserlistName(u))
	})

	t.Run("FCGIAppName", func(t *testing.T) {
		f := &models.FCGIApp{FCGIAppBase: models.FCGIAppBase{Name: "fcgi"}}
		assert.Equal(t, "fcgi", FCGIAppName(f))
	})

	t.Run("UserName", func(t *testing.T) {
		u := &models.User{Username: "admin"}
		assert.Equal(t, "admin", UserName(u))
	})

	t.Run("MailerEntryName", func(t *testing.T) {
		m := &models.MailerEntry{Name: "smtp"}
		assert.Equal(t, "smtp", MailerEntryName(m))
	})

	t.Run("PeerEntryName", func(t *testing.T) {
		p := &models.PeerEntry{Name: "peer1"}
		assert.Equal(t, "peer1", PeerEntryName(p))
	})

	t.Run("NameserverName", func(t *testing.T) {
		n := &models.Nameserver{Name: "dns1"}
		assert.Equal(t, "dns1", NameserverName(n))
	})
}

func TestOpVerb(t *testing.T) {
	tests := []struct {
		opType OperationType
		want   string
	}{
		{OperationCreate, "Create"},
		{OperationUpdate, "Update"},
		{OperationDelete, "Delete"},
		{OperationType(99), "Process"}, // Unknown operation type
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			assert.Equal(t, tt.want, opVerb(tt.opType))
		})
	}
}
