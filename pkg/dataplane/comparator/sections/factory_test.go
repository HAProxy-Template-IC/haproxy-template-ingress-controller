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
			name:             "BackendOps.Create",
			factory:          BackendOps.Create,
			wantType:         OperationCreate,
			wantSection:      "backend",
			wantDescContains: "Create backend 'api-backend'",
		},
		{
			name:             "BackendOps.Update",
			factory:          BackendOps.Update,
			wantType:         OperationUpdate,
			wantSection:      "backend",
			wantDescContains: "Update backend 'api-backend'",
		},
		{
			name:             "BackendOps.Delete",
			factory:          BackendOps.Delete,
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
			name:             "FrontendOps.Create",
			factory:          FrontendOps.Create,
			wantType:         OperationCreate,
			wantSection:      "frontend",
			wantDescContains: "Create frontend 'http-frontend'",
		},
		{
			name:             "FrontendOps.Update",
			factory:          FrontendOps.Update,
			wantType:         OperationUpdate,
			wantSection:      "frontend",
			wantDescContains: "Update frontend 'http-frontend'",
		},
		{
			name:             "FrontendOps.Delete",
			factory:          FrontendOps.Delete,
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
			name:             "DefaultsOps.Create",
			factory:          DefaultsOps.Create,
			wantType:         OperationCreate,
			wantSection:      "defaults",
			wantDescContains: "Create defaults section 'http-defaults'",
		},
		{
			name:             "DefaultsOps.Update",
			factory:          DefaultsOps.Update,
			wantType:         OperationUpdate,
			wantSection:      "defaults",
			wantDescContains: "Update defaults section 'http-defaults'",
		},
		{
			name:             "DefaultsOps.Delete",
			factory:          DefaultsOps.Delete,
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
				name:             "ACLFrontendOps.Create",
				factory:          ACLFrontendOps.Create,
				wantType:         OperationCreate,
				wantDescContains: "Create ACL 'is_api' in frontend 'http'",
			},
			{
				name:             "ACLFrontendOps.Update",
				factory:          ACLFrontendOps.Update,
				wantType:         OperationUpdate,
				wantDescContains: "Update ACL 'is_api' in frontend 'http'",
			},
			{
				name:             "ACLFrontendOps.Delete",
				factory:          ACLFrontendOps.Delete,
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
				name:             "ACLBackendOps.Create",
				factory:          ACLBackendOps.Create,
				wantType:         OperationCreate,
				wantDescContains: "Create ACL 'is_api' in backend 'api'",
			},
			{
				name:             "ACLBackendOps.Update",
				factory:          ACLBackendOps.Update,
				wantType:         OperationUpdate,
				wantDescContains: "Update ACL 'is_api' in backend 'api'",
			},
			{
				name:             "ACLBackendOps.Delete",
				factory:          ACLBackendOps.Delete,
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
			name:             "BindFrontendOps.Create",
			factory:          BindFrontendOps.Create,
			wantType:         OperationCreate,
			wantDescContains: "Create bind 'http-bind' in frontend 'http'",
		},
		{
			name:             "BindFrontendOps.Update",
			factory:          BindFrontendOps.Update,
			wantType:         OperationUpdate,
			wantDescContains: "Update bind 'http-bind' in frontend 'http'",
		},
		{
			name:             "BindFrontendOps.Delete",
			factory:          BindFrontendOps.Delete,
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
				name:             "HTTPRequestRuleFrontendOps.Create",
				factory:          HTTPRequestRuleFrontendOps.Create,
				wantType:         OperationCreate,
				wantDescContains: "Create HTTP request rule at index 5 in frontend 'http'",
			},
			{
				name:             "HTTPRequestRuleFrontendOps.Update",
				factory:          HTTPRequestRuleFrontendOps.Update,
				wantType:         OperationUpdate,
				wantDescContains: "Update HTTP request rule at index 5 in frontend 'http'",
			},
			{
				name:             "HTTPRequestRuleFrontendOps.Delete",
				factory:          HTTPRequestRuleFrontendOps.Delete,
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
				name:             "HTTPRequestRuleBackendOps.Create",
				factory:          HTTPRequestRuleBackendOps.Create,
				wantType:         OperationCreate,
				wantDescContains: "Create HTTP request rule at index 3 in backend 'api'",
			},
			{
				name:             "HTTPRequestRuleBackendOps.Update",
				factory:          HTTPRequestRuleBackendOps.Update,
				wantType:         OperationUpdate,
				wantDescContains: "Update HTTP request rule at index 3 in backend 'api'",
			},
			{
				name:             "HTTPRequestRuleBackendOps.Delete",
				factory:          HTTPRequestRuleBackendOps.Delete,
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
			name:             "BackendSwitchingRuleFrontendOps.Create",
			factory:          BackendSwitchingRuleFrontendOps.Create,
			wantType:         OperationCreate,
			wantDescContains: "Create backend switching rule at index 0 in frontend 'http'",
		},
		{
			name:             "BackendSwitchingRuleFrontendOps.Update",
			factory:          BackendSwitchingRuleFrontendOps.Update,
			wantType:         OperationUpdate,
			wantDescContains: "Update backend switching rule at index 0 in frontend 'http'",
		},
		{
			name:             "BackendSwitchingRuleFrontendOps.Delete",
			factory:          BackendSwitchingRuleFrontendOps.Delete,
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
			name:             "UserOps.Create",
			factory:          UserOps.Create,
			wantType:         OperationCreate,
			wantDescContains: "Create user 'admin' in userlist 'admins'",
		},
		{
			name:             "UserOps.Update",
			factory:          UserOps.Update,
			wantType:         OperationUpdate,
			wantDescContains: "Update user 'admin' in userlist 'admins'",
		},
		{
			name:             "UserOps.Delete",
			factory:          UserOps.Delete,
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
			name:             "CacheOps.Create",
			factory:          CacheOps.Create,
			wantType:         OperationCreate,
			wantDescContains: "Create cache 'my-cache'",
		},
		{
			name:             "CacheOps.Update",
			factory:          CacheOps.Update,
			wantType:         OperationUpdate,
			wantDescContains: "Update cache 'my-cache'",
		},
		{
			name:             "CacheOps.Delete",
			factory:          CacheOps.Delete,
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
			name:             "ResolverOps.Create",
			factory:          ResolverOps.Create,
			wantType:         OperationCreate,
			wantDescContains: "Create resolver 'dns-resolver'",
		},
		{
			name:             "ResolverOps.Update",
			factory:          ResolverOps.Update,
			wantType:         OperationUpdate,
			wantDescContains: "Update resolver 'dns-resolver'",
		},
		{
			name:             "ResolverOps.Delete",
			factory:          ResolverOps.Delete,
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
			name:             "NameserverOps.Create",
			factory:          NameserverOps.Create,
			wantType:         OperationCreate,
			wantDescContains: "Create nameserver 'ns1' in resolvers section 'dns'",
		},
		{
			name:             "NameserverOps.Update",
			factory:          NameserverOps.Update,
			wantType:         OperationUpdate,
			wantDescContains: "Update nameserver 'ns1' in resolvers section 'dns'",
		},
		{
			name:             "NameserverOps.Delete",
			factory:          NameserverOps.Delete,
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
				name:             "HTTPResponseRuleFrontendOps.Create",
				factory:          HTTPResponseRuleFrontendOps.Create,
				wantType:         OperationCreate,
				wantDescContains: "Create HTTP response rule",
			},
			{
				name:             "HTTPResponseRuleFrontendOps.Update",
				factory:          HTTPResponseRuleFrontendOps.Update,
				wantType:         OperationUpdate,
				wantDescContains: "Update HTTP response rule",
			},
			{
				name:             "HTTPResponseRuleFrontendOps.Delete",
				factory:          HTTPResponseRuleFrontendOps.Delete,
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
				name:             "HTTPResponseRuleBackendOps.Create",
				factory:          HTTPResponseRuleBackendOps.Create,
				wantType:         OperationCreate,
				wantDescContains: "Create HTTP response rule",
			},
			{
				name:             "HTTPResponseRuleBackendOps.Update",
				factory:          HTTPResponseRuleBackendOps.Update,
				wantType:         OperationUpdate,
				wantDescContains: "Update HTTP response rule",
			},
			{
				name:             "HTTPResponseRuleBackendOps.Delete",
				factory:          HTTPResponseRuleBackendOps.Delete,
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
				name:             "FilterFrontendOps.Create",
				factory:          FilterFrontendOps.Create,
				wantType:         OperationCreate,
				wantDescContains: "Create filter",
			},
			{
				name:             "FilterFrontendOps.Update",
				factory:          FilterFrontendOps.Update,
				wantType:         OperationUpdate,
				wantDescContains: "Update filter",
			},
			{
				name:             "FilterFrontendOps.Delete",
				factory:          FilterFrontendOps.Delete,
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
				name:             "FilterBackendOps.Create",
				factory:          FilterBackendOps.Create,
				wantType:         OperationCreate,
				wantDescContains: "Create filter",
			},
			{
				name:             "FilterBackendOps.Update",
				factory:          FilterBackendOps.Update,
				wantType:         OperationUpdate,
				wantDescContains: "Update filter",
			},
			{
				name:             "FilterBackendOps.Delete",
				factory:          FilterBackendOps.Delete,
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
				name:             "LogTargetFrontendOps.Create",
				factory:          LogTargetFrontendOps.Create,
				wantType:         OperationCreate,
				wantDescContains: "Create log target",
			},
			{
				name:             "LogTargetFrontendOps.Update",
				factory:          LogTargetFrontendOps.Update,
				wantType:         OperationUpdate,
				wantDescContains: "Update log target",
			},
			{
				name:             "LogTargetFrontendOps.Delete",
				factory:          LogTargetFrontendOps.Delete,
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
				name:             "LogTargetBackendOps.Create",
				factory:          LogTargetBackendOps.Create,
				wantType:         OperationCreate,
				wantDescContains: "Create log target",
			},
			{
				name:             "LogTargetBackendOps.Update",
				factory:          LogTargetBackendOps.Update,
				wantType:         OperationUpdate,
				wantDescContains: "Update log target",
			},
			{
				name:             "LogTargetBackendOps.Delete",
				factory:          LogTargetBackendOps.Delete,
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
		factory          func(string, string, *models.ServerTemplate) Operation
		wantType         OperationType
		wantDescContains string
	}{
		{
			name:             "ServerTemplateOps.Create",
			factory:          ServerTemplateOps.Create,
			wantType:         OperationCreate,
			wantDescContains: "Create server template 'web'",
		},
		{
			name:             "ServerTemplateOps.Update",
			factory:          ServerTemplateOps.Update,
			wantType:         OperationUpdate,
			wantDescContains: "Update server template 'web'",
		},
		{
			name:             "ServerTemplateOps.Delete",
			factory:          ServerTemplateOps.Delete,
			wantType:         OperationDelete,
			wantDescContains: "Delete server template 'web'",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			op := tt.factory("api", serverTemplate.Prefix, serverTemplate)

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
			name:             "MailerEntryOps.Create",
			factory:          MailerEntryOps.Create,
			wantType:         OperationCreate,
			wantDescContains: "Create mailer entry 'smtp1'",
		},
		{
			name:             "MailerEntryOps.Update",
			factory:          MailerEntryOps.Update,
			wantType:         OperationUpdate,
			wantDescContains: "Update mailer entry 'smtp1'",
		},
		{
			name:             "MailerEntryOps.Delete",
			factory:          MailerEntryOps.Delete,
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
			name:             "PeerEntryOps.Create",
			factory:          PeerEntryOps.Create,
			wantType:         OperationCreate,
			wantDescContains: "Create peer entry 'peer1'",
		},
		{
			name:             "PeerEntryOps.Update",
			factory:          PeerEntryOps.Update,
			wantType:         OperationUpdate,
			wantDescContains: "Update peer entry 'peer1'",
		},
		{
			name:             "PeerEntryOps.Delete",
			factory:          PeerEntryOps.Delete,
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
			name:             "HTTPErrorsOps.Create",
			factory:          HTTPErrorsOps.Create,
			wantType:         OperationCreate,
			wantDescContains: "Create http-errors section 'custom-errors'",
		},
		{
			name:             "HTTPErrorsOps.Update",
			factory:          HTTPErrorsOps.Update,
			wantType:         OperationUpdate,
			wantDescContains: "Update http-errors section 'custom-errors'",
		},
		{
			name:             "HTTPErrorsOps.Delete",
			factory:          HTTPErrorsOps.Delete,
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
			name:             "LogForwardOps.Create",
			factory:          LogForwardOps.Create,
			wantType:         OperationCreate,
			wantDescContains: "Create log-forward 'syslogs'",
		},
		{
			name:             "LogForwardOps.Update",
			factory:          LogForwardOps.Update,
			wantType:         OperationUpdate,
			wantDescContains: "Update log-forward 'syslogs'",
		},
		{
			name:             "LogForwardOps.Delete",
			factory:          LogForwardOps.Delete,
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
			name:             "MailersOps.Create",
			factory:          MailersOps.Create,
			wantType:         OperationCreate,
			wantDescContains: "Create mailers 'mailers1'",
		},
		{
			name:             "MailersOps.Update",
			factory:          MailersOps.Update,
			wantType:         OperationUpdate,
			wantDescContains: "Update mailers 'mailers1'",
		},
		{
			name:             "MailersOps.Delete",
			factory:          MailersOps.Delete,
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
			name:             "PeerSectionOps.Create",
			factory:          PeerSectionOps.Create,
			wantType:         OperationCreate,
			wantDescContains: "Create peer section 'mypeers'",
		},
		{
			name:             "PeerSectionOps.Update",
			factory:          PeerSectionOps.Update,
			wantType:         OperationUpdate,
			wantDescContains: "Update peer section 'mypeers'",
		},
		{
			name:             "PeerSectionOps.Delete",
			factory:          PeerSectionOps.Delete,
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
			name:             "ProgramOps.Create",
			factory:          ProgramOps.Create,
			wantType:         OperationCreate,
			wantDescContains: "Create program 'myprogram'",
		},
		{
			name:             "ProgramOps.Update",
			factory:          ProgramOps.Update,
			wantType:         OperationUpdate,
			wantDescContains: "Update program 'myprogram'",
		},
		{
			name:             "ProgramOps.Delete",
			factory:          ProgramOps.Delete,
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
			name:             "RingOps.Create",
			factory:          RingOps.Create,
			wantType:         OperationCreate,
			wantDescContains: "Create ring 'myring'",
		},
		{
			name:             "RingOps.Update",
			factory:          RingOps.Update,
			wantType:         OperationUpdate,
			wantDescContains: "Update ring 'myring'",
		},
		{
			name:             "RingOps.Delete",
			factory:          RingOps.Delete,
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
			name:             "CrtStoreOps.Create",
			factory:          CrtStoreOps.Create,
			wantType:         OperationCreate,
			wantDescContains: "Create crt-store 'my-certs'",
		},
		{
			name:             "CrtStoreOps.Update",
			factory:          CrtStoreOps.Update,
			wantType:         OperationUpdate,
			wantDescContains: "Update crt-store 'my-certs'",
		},
		{
			name:             "CrtStoreOps.Delete",
			factory:          CrtStoreOps.Delete,
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
			name:             "UserlistOps.Create",
			factory:          UserlistOps.Create,
			wantType:         OperationCreate,
			wantDescContains: "Create userlist 'admins'",
		},
		{
			name:             "UserlistOps.Delete",
			factory:          UserlistOps.Delete,
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
			name:             "FcgiAppOps.Create",
			factory:          FcgiAppOps.Create,
			wantType:         OperationCreate,
			wantDescContains: "Create fcgi-app 'php-fpm'",
		},
		{
			name:             "FcgiAppOps.Update",
			factory:          FcgiAppOps.Update,
			wantType:         OperationUpdate,
			wantDescContains: "Update fcgi-app 'php-fpm'",
		},
		{
			name:             "FcgiAppOps.Delete",
			factory:          FcgiAppOps.Delete,
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
				name:             "TCPRequestRuleFrontendOps.Create",
				factory:          TCPRequestRuleFrontendOps.Create,
				wantType:         OperationCreate,
				wantDescContains: "Create TCP request rule",
			},
			{
				name:             "TCPRequestRuleFrontendOps.Update",
				factory:          TCPRequestRuleFrontendOps.Update,
				wantType:         OperationUpdate,
				wantDescContains: "Update TCP request rule",
			},
			{
				name:             "TCPRequestRuleFrontendOps.Delete",
				factory:          TCPRequestRuleFrontendOps.Delete,
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
				name:             "TCPRequestRuleBackendOps.Create",
				factory:          TCPRequestRuleBackendOps.Create,
				wantType:         OperationCreate,
				wantDescContains: "Create TCP request rule",
			},
			{
				name:             "TCPRequestRuleBackendOps.Update",
				factory:          TCPRequestRuleBackendOps.Update,
				wantType:         OperationUpdate,
				wantDescContains: "Update TCP request rule",
			},
			{
				name:             "TCPRequestRuleBackendOps.Delete",
				factory:          TCPRequestRuleBackendOps.Delete,
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
			name:             "TCPResponseRuleBackendOps.Create",
			factory:          TCPResponseRuleBackendOps.Create,
			wantType:         OperationCreate,
			wantDescContains: "Create TCP response rule",
		},
		{
			name:             "TCPResponseRuleBackendOps.Update",
			factory:          TCPResponseRuleBackendOps.Update,
			wantType:         OperationUpdate,
			wantDescContains: "Update TCP response rule",
		},
		{
			name:             "TCPResponseRuleBackendOps.Delete",
			factory:          TCPResponseRuleBackendOps.Delete,
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
			name:             "StickRuleBackendOps.Create",
			factory:          StickRuleBackendOps.Create,
			wantType:         OperationCreate,
			wantDescContains: "Create stick rule",
		},
		{
			name:             "StickRuleBackendOps.Update",
			factory:          StickRuleBackendOps.Update,
			wantType:         OperationUpdate,
			wantDescContains: "Update stick rule",
		},
		{
			name:             "StickRuleBackendOps.Delete",
			factory:          StickRuleBackendOps.Delete,
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
			name:             "HTTPAfterResponseRuleBackendOps.Create",
			factory:          HTTPAfterResponseRuleBackendOps.Create,
			wantType:         OperationCreate,
			wantDescContains: "Create HTTP after response rule",
		},
		{
			name:             "HTTPAfterResponseRuleBackendOps.Update",
			factory:          HTTPAfterResponseRuleBackendOps.Update,
			wantType:         OperationUpdate,
			wantDescContains: "Update HTTP after response rule",
		},
		{
			name:             "HTTPAfterResponseRuleBackendOps.Delete",
			factory:          HTTPAfterResponseRuleBackendOps.Delete,
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
			name:             "ServerSwitchingRuleBackendOps.Create",
			factory:          ServerSwitchingRuleBackendOps.Create,
			wantType:         OperationCreate,
			wantDescContains: "Create server switching rule",
		},
		{
			name:             "ServerSwitchingRuleBackendOps.Update",
			factory:          ServerSwitchingRuleBackendOps.Update,
			wantType:         OperationUpdate,
			wantDescContains: "Update server switching rule",
		},
		{
			name:             "ServerSwitchingRuleBackendOps.Delete",
			factory:          ServerSwitchingRuleBackendOps.Delete,
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
			name:             "HTTPCheckBackendOps.Create",
			factory:          HTTPCheckBackendOps.Create,
			wantType:         OperationCreate,
			wantDescContains: "Create HTTP check",
		},
		{
			name:             "HTTPCheckBackendOps.Update",
			factory:          HTTPCheckBackendOps.Update,
			wantType:         OperationUpdate,
			wantDescContains: "Update HTTP check",
		},
		{
			name:             "HTTPCheckBackendOps.Delete",
			factory:          HTTPCheckBackendOps.Delete,
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
			name:             "TCPCheckBackendOps.Create",
			factory:          TCPCheckBackendOps.Create,
			wantType:         OperationCreate,
			wantDescContains: "Create TCP check",
		},
		{
			name:             "TCPCheckBackendOps.Update",
			factory:          TCPCheckBackendOps.Update,
			wantType:         OperationUpdate,
			wantDescContains: "Update TCP check",
		},
		{
			name:             "TCPCheckBackendOps.Delete",
			factory:          TCPCheckBackendOps.Delete,
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
			name:             "CaptureFrontendOps.Create",
			factory:          CaptureFrontendOps.Create,
			wantType:         OperationCreate,
			wantDescContains: "Create capture",
		},
		{
			name:             "CaptureFrontendOps.Update",
			factory:          CaptureFrontendOps.Update,
			wantType:         OperationUpdate,
			wantDescContains: "Update capture",
		},
		{
			name:             "CaptureFrontendOps.Delete",
			factory:          CaptureFrontendOps.Delete,
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
				got := describeNamedChild(tt.opType, "bind", bindIdentifier(tt.bind), "frontend", tt.frontendName)()
				for _, want := range tt.wantContains {
					assert.Contains(t, got, want)
				}
			})
		}
	})

	t.Run("DescribeTypedChild_logTarget", func(t *testing.T) {
		logTarget := &models.LogTarget{Address: "127.0.0.1", Facility: "local0"}

		desc := describeTypedChild(OperationCreate, "log target", logTargetIdentifier(logTarget), "at index 0", "frontend", "http")()
		assert.Contains(t, desc, "Create log target")
		assert.Contains(t, desc, "frontend 'http'")
	})

	t.Run("DescribeTypedChild_filter", func(t *testing.T) {
		filter := &models.Filter{Type: "trace"}

		desc := describeTypedChild(OperationUpdate, "filter", filterIdentifier(filter), "at index 0", "backend", "api")()
		assert.Contains(t, desc, "Update filter")
		assert.Contains(t, desc, "trace")
		assert.Contains(t, desc, "backend 'api'")
	})

	t.Run("DescribeTypedChild_capture", func(t *testing.T) {
		capture := &models.Capture{Type: "request"}

		desc := describeTypedChild(OperationDelete, "capture", captureIdentifier(capture), "at index 0", "frontend", "http")()
		assert.Contains(t, desc, "Delete capture")
		assert.Contains(t, desc, "request")
		assert.Contains(t, desc, "frontend 'http'")
	})

	t.Run("DescribeTypedChild_tcpRequestRule", func(t *testing.T) {
		rule := &models.TCPRequestRule{Type: "inspect-delay"}

		desc := describeTypedChild(OperationCreate, "TCP request rule", tcpRequestRuleIdentifier(rule), "at index 0", "frontend", "tcp")()
		assert.Contains(t, desc, "Create TCP request rule")
		assert.Contains(t, desc, "inspect-delay")
		assert.Contains(t, desc, "frontend 'tcp'")
	})

	t.Run("DescribeTypedChild_tcpResponseRule", func(t *testing.T) {
		rule := &models.TCPResponseRule{Type: "content"}

		desc := describeTypedChild(OperationUpdate, "TCP response rule", tcpResponseRuleIdentifier(rule), "at index 0", "backend", "api")()
		assert.Contains(t, desc, "Update TCP response rule")
		assert.Contains(t, desc, "content")
		assert.Contains(t, desc, "backend 'api'")
	})

	t.Run("DescribeTypedChild_httpCheck", func(t *testing.T) {
		check := &models.HTTPCheck{Type: "send"}

		desc := describeTypedChild(OperationDelete, "HTTP check", httpCheckIdentifier(check), "at index 0", "backend", "api")()
		assert.Contains(t, desc, "Delete HTTP check")
		assert.Contains(t, desc, "send")
		assert.Contains(t, desc, "backend 'api'")
	})

	t.Run("DescribeTypedChild_tcpCheck", func(t *testing.T) {
		check := &models.TCPCheck{Action: "connect"}

		desc := describeTypedChild(OperationCreate, "TCP check", tcpCheckIdentifier(check), "at index 0", "backend", "api")()
		assert.Contains(t, desc, "Create TCP check")
		assert.Contains(t, desc, "connect")
		assert.Contains(t, desc, "backend 'api'")
	})

	t.Run("DescribeTypedChild_stickRule", func(t *testing.T) {
		rule := &models.StickRule{Type: "store-request"}

		desc := describeTypedChild(OperationUpdate, "stick rule", stickRuleIdentifier(rule), "at index 0", "backend", "api")()
		assert.Contains(t, desc, "Update stick rule")
		assert.Contains(t, desc, "store-request")
		assert.Contains(t, desc, "backend 'api'")
	})

	t.Run("DescribeTypedChild_serverSwitchingRule", func(t *testing.T) {
		rule := &models.ServerSwitchingRule{TargetServer: "srv1"}

		desc := describeTypedChild(OperationDelete, "server switching rule", serverSwitchingRuleIdentifier(rule), "at index 0", "backend", "api")()
		assert.Contains(t, desc, "Delete server switching rule")
		assert.Contains(t, desc, "srv1")
		assert.Contains(t, desc, "backend 'api'")
	})

	t.Run("DescribeTypedChild_httpAfterResponseRule", func(t *testing.T) {
		rule := &models.HTTPAfterResponseRule{Type: "set-header"}

		desc := describeTypedChild(OperationCreate, "HTTP after response rule", httpAfterResponseRuleIdentifier(rule), "at index 0", "backend", "api")()
		assert.Contains(t, desc, "Create HTTP after response rule")
		assert.Contains(t, desc, "set-header")
		assert.Contains(t, desc, "backend 'api'")
	})

	// Test describeTypedChild with all operation types
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
			desc := describeTypedChild(tt.opType, "HTTP request rule", httpRequestRuleIdentifier(rule), "at index 0", "frontend", "http")()
			assert.Contains(t, desc, tt.wantContains)
			assert.Contains(t, desc, "add-header")
		}
	})

	// Test describeTypedChild with empty identifier - uses fallback
	t.Run("DescribeTypedChild_httpRequestRule_empty_type", func(t *testing.T) {
		rule := &models.HTTPRequestRule{}
		desc := describeTypedChild(OperationCreate, "HTTP request rule", httpRequestRuleIdentifier(rule), "at index 5", "frontend", "http")()
		assert.Contains(t, desc, "at index 5")
	})

	// Test describeTypedChild for HTTP response rule
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
			desc := describeTypedChild(tt.opType, "HTTP response rule", httpResponseRuleIdentifier(rule), "", "backend", "api")()
			assert.Contains(t, desc, tt.wantContains)
		}
	})

	// Test describeTypedChild for backend switching rule
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
			desc := describeTypedChild(tt.opType, "backend switching rule", backendSwitchingRuleIdentifier(rule), "at index 0", "frontend", "http")()
			assert.Contains(t, desc, tt.wantContains)
			assert.Contains(t, desc, "api_backend")
		}
	})

	// Test describeTypedChild with empty identifier - uses index fallback
	t.Run("DescribeTypedChild_backendSwitchingRule_empty_name", func(t *testing.T) {
		rule := &models.BackendSwitchingRule{}
		desc := describeTypedChild(OperationCreate, "backend switching rule", backendSwitchingRuleIdentifier(rule), "at index 3", "frontend", "http")()
		assert.Contains(t, desc, "at index 3")
	})

	// Test empty identifier branches - fallback to index
	t.Run("DescribeTypedChild_logTarget_empty", func(t *testing.T) {
		logTarget := &models.LogTarget{}
		desc := describeTypedChild(OperationCreate, "log target", logTargetIdentifier(logTarget), "at index 5", "frontend", "http")()
		assert.Contains(t, desc, "at index 5")
	})

	t.Run("DescribeTypedChild_filter_empty", func(t *testing.T) {
		filter := &models.Filter{}
		desc := describeTypedChild(OperationCreate, "filter", filterIdentifier(filter), "at index 3", "backend", "api")()
		assert.Contains(t, desc, "at index 3")
	})

	t.Run("DescribeTypedChild_capture_empty", func(t *testing.T) {
		capture := &models.Capture{}
		desc := describeTypedChild(OperationCreate, "capture", captureIdentifier(capture), "at index 2", "frontend", "http")()
		assert.Contains(t, desc, "at index 2")
	})

	t.Run("DescribeTypedChild_tcpRequestRule_empty", func(t *testing.T) {
		rule := &models.TCPRequestRule{}
		desc := describeTypedChild(OperationCreate, "TCP request rule", tcpRequestRuleIdentifier(rule), "at index 4", "frontend", "tcp")()
		assert.Contains(t, desc, "at index 4")
	})

	t.Run("DescribeTypedChild_tcpResponseRule_empty", func(t *testing.T) {
		rule := &models.TCPResponseRule{}
		desc := describeTypedChild(OperationCreate, "TCP response rule", tcpResponseRuleIdentifier(rule), "at index 1", "backend", "api")()
		assert.Contains(t, desc, "at index 1")
	})

	t.Run("DescribeTypedChild_httpCheck_empty", func(t *testing.T) {
		check := &models.HTTPCheck{}
		desc := describeTypedChild(OperationCreate, "HTTP check", httpCheckIdentifier(check), "at index 6", "backend", "api")()
		assert.Contains(t, desc, "at index 6")
	})

	t.Run("DescribeTypedChild_tcpCheck_empty", func(t *testing.T) {
		check := &models.TCPCheck{}
		desc := describeTypedChild(OperationCreate, "TCP check", tcpCheckIdentifier(check), "at index 7", "backend", "api")()
		assert.Contains(t, desc, "at index 7")
	})

	t.Run("DescribeTypedChild_stickRule_empty", func(t *testing.T) {
		rule := &models.StickRule{}
		desc := describeTypedChild(OperationCreate, "stick rule", stickRuleIdentifier(rule), "at index 8", "backend", "api")()
		assert.Contains(t, desc, "at index 8")
	})

	t.Run("DescribeTypedChild_serverSwitchingRule_empty", func(t *testing.T) {
		rule := &models.ServerSwitchingRule{}
		desc := describeTypedChild(OperationCreate, "server switching rule", serverSwitchingRuleIdentifier(rule), "at index 9", "backend", "api")()
		assert.Contains(t, desc, "at index 9")
	})

	t.Run("DescribeTypedChild_httpAfterResponseRule_empty", func(t *testing.T) {
		rule := &models.HTTPAfterResponseRule{}
		desc := describeTypedChild(OperationCreate, "HTTP after response rule", httpAfterResponseRuleIdentifier(rule), "at index 10", "backend", "api")()
		assert.Contains(t, desc, "at index 10")
	})

	// Test describeNamedChild for bind with unknown operation type
	t.Run("bindIdentifier_unknown_op", func(t *testing.T) {
		ptrInt64 := func(i int64) *int64 { return &i }
		bind := &models.Bind{
			Address: "*",
			Port:    ptrInt64(80),
		}
		desc := describeNamedChild(OperationType(99), "bind", bindIdentifier(bind), "frontend", "http")()
		assert.Contains(t, desc, "Process bind")
	})
}

func TestNameExtractors(t *testing.T) {
	t.Run("backendNameFn", func(t *testing.T) {
		b := &models.Backend{BackendBase: models.BackendBase{Name: "my-backend"}}
		assert.Equal(t, "my-backend", backendNameFn(b))
	})

	t.Run("frontendNameFn", func(t *testing.T) {
		f := &models.Frontend{FrontendBase: models.FrontendBase{Name: "my-frontend"}}
		assert.Equal(t, "my-frontend", frontendNameFn(f))
	})

	t.Run("defaultsNameFn", func(t *testing.T) {
		d := &models.Defaults{DefaultsBase: models.DefaultsBase{Name: "my-defaults"}}
		assert.Equal(t, "my-defaults", defaultsNameFn(d))
	})

	t.Run("httpErrorsSectionName", func(t *testing.T) {
		h := &models.HTTPErrorsSection{Name: "errors"}
		assert.Equal(t, "errors", httpErrorsSectionName(h))
	})

	t.Run("logForwardName", func(t *testing.T) {
		l := &models.LogForward{LogForwardBase: models.LogForwardBase{Name: "logs"}}
		assert.Equal(t, "logs", logForwardName(l))
	})

	t.Run("mailersSectionName", func(t *testing.T) {
		m := &models.MailersSection{MailersSectionBase: models.MailersSectionBase{Name: "mailers"}}
		assert.Equal(t, "mailers", mailersSectionName(m))
	})

	t.Run("peerSectionName", func(t *testing.T) {
		p := &models.PeerSection{PeerSectionBase: models.PeerSectionBase{Name: "peers"}}
		assert.Equal(t, "peers", peerSectionName(p))
	})

	t.Run("programNameFn", func(t *testing.T) {
		p := &models.Program{Name: "prog"}
		assert.Equal(t, "prog", programNameFn(p))
	})

	t.Run("resolverNameFn", func(t *testing.T) {
		r := &models.Resolver{ResolverBase: models.ResolverBase{Name: "resolver"}}
		assert.Equal(t, "resolver", resolverNameFn(r))
	})

	t.Run("ringNameFn", func(t *testing.T) {
		r := &models.Ring{RingBase: models.RingBase{Name: "ring"}}
		assert.Equal(t, "ring", ringNameFn(r))
	})

	t.Run("crtStoreName", func(t *testing.T) {
		c := &models.CrtStore{CrtStoreBase: models.CrtStoreBase{Name: "store"}}
		assert.Equal(t, "store", crtStoreName(c))
	})

	t.Run("userlistName", func(t *testing.T) {
		u := &models.Userlist{UserlistBase: models.UserlistBase{Name: "users"}}
		assert.Equal(t, "users", userlistName(u))
	})

	t.Run("fcgiAppName", func(t *testing.T) {
		f := &models.FCGIApp{FCGIAppBase: models.FCGIAppBase{Name: "fcgi"}}
		assert.Equal(t, "fcgi", fcgiAppName(f))
	})

	t.Run("userNameFn", func(t *testing.T) {
		u := &models.User{Username: "admin"}
		assert.Equal(t, "admin", userNameFn(u))
	})

	t.Run("mailerEntryName", func(t *testing.T) {
		m := &models.MailerEntry{Name: "smtp"}
		assert.Equal(t, "smtp", mailerEntryName(m))
	})

	t.Run("peerEntryName", func(t *testing.T) {
		p := &models.PeerEntry{Name: "peer1"}
		assert.Equal(t, "peer1", peerEntryName(p))
	})

	t.Run("nameserverNameFn", func(t *testing.T) {
		n := &models.Nameserver{Name: "dns1"}
		assert.Equal(t, "dns1", nameserverNameFn(n))
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
