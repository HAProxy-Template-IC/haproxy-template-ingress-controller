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

package executors

import (
	"context"
	"net/http"

	"github.com/haproxytech/client-native/v6/models"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/client"
	v30 "gitlab.com/haproxy-haptic/haptic/pkg/generated/dataplaneapi/v30"
	v30ee "gitlab.com/haproxy-haptic/haptic/pkg/generated/dataplaneapi/v30ee"
	v31 "gitlab.com/haproxy-haptic/haptic/pkg/generated/dataplaneapi/v31"
	v31ee "gitlab.com/haproxy-haptic/haptic/pkg/generated/dataplaneapi/v31ee"
	v32 "gitlab.com/haproxy-haptic/haptic/pkg/generated/dataplaneapi/v32"
	v32ee "gitlab.com/haproxy-haptic/haptic/pkg/generated/dataplaneapi/v32ee"
)

// HTTPRequestRuleFrontendCreate returns an executor for creating HTTP request rules in frontends.
func HTTPRequestRuleFrontendCreate() func(ctx context.Context, c *client.DataplaneClient, txID string, parent string, index int, model *models.HTTPRequestRule) error {
	return func(ctx context.Context, c *client.DataplaneClient, txID string, parent string, index int, model *models.HTTPRequestRule) error {
		clientset := c.Clientset()

		resp, err := client.DispatchCreateChild(ctx, c, parent, index, model,
			func(p string, idx int, m v32.HttpRequestRule) (*http.Response, error) {
				params := &v32.CreateHTTPRequestRuleFrontendParams{TransactionId: &txID}
				return clientset.V32().CreateHTTPRequestRuleFrontend(ctx, p, idx, params, m)
			},
			func(p string, idx int, m v31.HttpRequestRule) (*http.Response, error) {
				params := &v31.CreateHTTPRequestRuleFrontendParams{TransactionId: &txID}
				return clientset.V31().CreateHTTPRequestRuleFrontend(ctx, p, idx, params, m)
			},
			func(p string, idx int, m v30.HttpRequestRule) (*http.Response, error) {
				params := &v30.CreateHTTPRequestRuleFrontendParams{TransactionId: &txID}
				return clientset.V30().CreateHTTPRequestRuleFrontend(ctx, p, idx, params, m)
			},
			func(p string, idx int, m v32ee.HttpRequestRule) (*http.Response, error) {
				params := &v32ee.CreateHTTPRequestRuleFrontendParams{TransactionId: &txID}
				return clientset.V32EE().CreateHTTPRequestRuleFrontend(ctx, p, idx, params, m)
			},
			func(p string, idx int, m v31ee.HttpRequestRule) (*http.Response, error) {
				params := &v31ee.CreateHTTPRequestRuleFrontendParams{TransactionId: &txID}
				return clientset.V31EE().CreateHTTPRequestRuleFrontend(ctx, p, idx, params, m)
			},
			func(p string, idx int, m v30ee.HttpRequestRule) (*http.Response, error) {
				params := &v30ee.CreateHTTPRequestRuleFrontendParams{TransactionId: &txID}
				return clientset.V30EE().CreateHTTPRequestRuleFrontend(ctx, p, idx, params, m)
			},
		)
		return dispatchAndCheck(resp, err, "HTTP request rule creation in frontend")
	}
}

// HTTPRequestRuleFrontendUpdate returns an executor for updating HTTP request rules in frontends.
func HTTPRequestRuleFrontendUpdate() func(ctx context.Context, c *client.DataplaneClient, txID string, parent string, index int, model *models.HTTPRequestRule) error {
	return func(ctx context.Context, c *client.DataplaneClient, txID string, parent string, index int, model *models.HTTPRequestRule) error {
		clientset := c.Clientset()

		resp, err := client.DispatchReplaceChild(ctx, c, parent, index, model,
			func(p string, idx int, m v32.HttpRequestRule) (*http.Response, error) {
				params := &v32.ReplaceHTTPRequestRuleFrontendParams{TransactionId: &txID}
				return clientset.V32().ReplaceHTTPRequestRuleFrontend(ctx, p, idx, params, m)
			},
			func(p string, idx int, m v31.HttpRequestRule) (*http.Response, error) {
				params := &v31.ReplaceHTTPRequestRuleFrontendParams{TransactionId: &txID}
				return clientset.V31().ReplaceHTTPRequestRuleFrontend(ctx, p, idx, params, m)
			},
			func(p string, idx int, m v30.HttpRequestRule) (*http.Response, error) {
				params := &v30.ReplaceHTTPRequestRuleFrontendParams{TransactionId: &txID}
				return clientset.V30().ReplaceHTTPRequestRuleFrontend(ctx, p, idx, params, m)
			},
			func(p string, idx int, m v32ee.HttpRequestRule) (*http.Response, error) {
				params := &v32ee.ReplaceHTTPRequestRuleFrontendParams{TransactionId: &txID}
				return clientset.V32EE().ReplaceHTTPRequestRuleFrontend(ctx, p, idx, params, m)
			},
			func(p string, idx int, m v31ee.HttpRequestRule) (*http.Response, error) {
				params := &v31ee.ReplaceHTTPRequestRuleFrontendParams{TransactionId: &txID}
				return clientset.V31EE().ReplaceHTTPRequestRuleFrontend(ctx, p, idx, params, m)
			},
			func(p string, idx int, m v30ee.HttpRequestRule) (*http.Response, error) {
				params := &v30ee.ReplaceHTTPRequestRuleFrontendParams{TransactionId: &txID}
				return clientset.V30EE().ReplaceHTTPRequestRuleFrontend(ctx, p, idx, params, m)
			},
		)
		return dispatchAndCheck(resp, err, "HTTP request rule update in frontend")
	}
}

// HTTPRequestRuleFrontendDelete returns an executor for deleting HTTP request rules from frontends.
func HTTPRequestRuleFrontendDelete() func(ctx context.Context, c *client.DataplaneClient, txID string, parent string, index int, _ *models.HTTPRequestRule) error {
	return func(ctx context.Context, c *client.DataplaneClient, txID string, parent string, index int, _ *models.HTTPRequestRule) error {
		clientset := c.Clientset()

		resp, err := client.DispatchDeleteChild(ctx, c, parent, index,
			func(p string, idx int) (*http.Response, error) {
				params := &v32.DeleteHTTPRequestRuleFrontendParams{TransactionId: &txID}
				return clientset.V32().DeleteHTTPRequestRuleFrontend(ctx, p, idx, params)
			},
			func(p string, idx int) (*http.Response, error) {
				params := &v31.DeleteHTTPRequestRuleFrontendParams{TransactionId: &txID}
				return clientset.V31().DeleteHTTPRequestRuleFrontend(ctx, p, idx, params)
			},
			func(p string, idx int) (*http.Response, error) {
				params := &v30.DeleteHTTPRequestRuleFrontendParams{TransactionId: &txID}
				return clientset.V30().DeleteHTTPRequestRuleFrontend(ctx, p, idx, params)
			},
			func(p string, idx int) (*http.Response, error) {
				params := &v32ee.DeleteHTTPRequestRuleFrontendParams{TransactionId: &txID}
				return clientset.V32EE().DeleteHTTPRequestRuleFrontend(ctx, p, idx, params)
			},
			func(p string, idx int) (*http.Response, error) {
				params := &v31ee.DeleteHTTPRequestRuleFrontendParams{TransactionId: &txID}
				return clientset.V31EE().DeleteHTTPRequestRuleFrontend(ctx, p, idx, params)
			},
			func(p string, idx int) (*http.Response, error) {
				params := &v30ee.DeleteHTTPRequestRuleFrontendParams{TransactionId: &txID}
				return clientset.V30EE().DeleteHTTPRequestRuleFrontend(ctx, p, idx, params)
			},
		)
		return dispatchAndCheck(resp, err, "HTTP request rule deletion from frontend")
	}
}

// HTTPRequestRuleBackendCreate returns an executor for creating HTTP request rules in backends.
func HTTPRequestRuleBackendCreate() func(ctx context.Context, c *client.DataplaneClient, txID string, parent string, index int, model *models.HTTPRequestRule) error {
	return func(ctx context.Context, c *client.DataplaneClient, txID string, parent string, index int, model *models.HTTPRequestRule) error {
		clientset := c.Clientset()

		resp, err := client.DispatchCreateChild(ctx, c, parent, index, model,
			func(p string, idx int, m v32.HttpRequestRule) (*http.Response, error) {
				params := &v32.CreateHTTPRequestRuleBackendParams{TransactionId: &txID}
				return clientset.V32().CreateHTTPRequestRuleBackend(ctx, p, idx, params, m)
			},
			func(p string, idx int, m v31.HttpRequestRule) (*http.Response, error) {
				params := &v31.CreateHTTPRequestRuleBackendParams{TransactionId: &txID}
				return clientset.V31().CreateHTTPRequestRuleBackend(ctx, p, idx, params, m)
			},
			func(p string, idx int, m v30.HttpRequestRule) (*http.Response, error) {
				params := &v30.CreateHTTPRequestRuleBackendParams{TransactionId: &txID}
				return clientset.V30().CreateHTTPRequestRuleBackend(ctx, p, idx, params, m)
			},
			func(p string, idx int, m v32ee.HttpRequestRule) (*http.Response, error) {
				params := &v32ee.CreateHTTPRequestRuleBackendParams{TransactionId: &txID}
				return clientset.V32EE().CreateHTTPRequestRuleBackend(ctx, p, idx, params, m)
			},
			func(p string, idx int, m v31ee.HttpRequestRule) (*http.Response, error) {
				params := &v31ee.CreateHTTPRequestRuleBackendParams{TransactionId: &txID}
				return clientset.V31EE().CreateHTTPRequestRuleBackend(ctx, p, idx, params, m)
			},
			func(p string, idx int, m v30ee.HttpRequestRule) (*http.Response, error) {
				params := &v30ee.CreateHTTPRequestRuleBackendParams{TransactionId: &txID}
				return clientset.V30EE().CreateHTTPRequestRuleBackend(ctx, p, idx, params, m)
			},
		)
		return dispatchAndCheck(resp, err, "HTTP request rule creation in backend")
	}
}

// HTTPRequestRuleBackendUpdate returns an executor for updating HTTP request rules in backends.
func HTTPRequestRuleBackendUpdate() func(ctx context.Context, c *client.DataplaneClient, txID string, parent string, index int, model *models.HTTPRequestRule) error {
	return func(ctx context.Context, c *client.DataplaneClient, txID string, parent string, index int, model *models.HTTPRequestRule) error {
		clientset := c.Clientset()

		resp, err := client.DispatchReplaceChild(ctx, c, parent, index, model,
			func(p string, idx int, m v32.HttpRequestRule) (*http.Response, error) {
				params := &v32.ReplaceHTTPRequestRuleBackendParams{TransactionId: &txID}
				return clientset.V32().ReplaceHTTPRequestRuleBackend(ctx, p, idx, params, m)
			},
			func(p string, idx int, m v31.HttpRequestRule) (*http.Response, error) {
				params := &v31.ReplaceHTTPRequestRuleBackendParams{TransactionId: &txID}
				return clientset.V31().ReplaceHTTPRequestRuleBackend(ctx, p, idx, params, m)
			},
			func(p string, idx int, m v30.HttpRequestRule) (*http.Response, error) {
				params := &v30.ReplaceHTTPRequestRuleBackendParams{TransactionId: &txID}
				return clientset.V30().ReplaceHTTPRequestRuleBackend(ctx, p, idx, params, m)
			},
			func(p string, idx int, m v32ee.HttpRequestRule) (*http.Response, error) {
				params := &v32ee.ReplaceHTTPRequestRuleBackendParams{TransactionId: &txID}
				return clientset.V32EE().ReplaceHTTPRequestRuleBackend(ctx, p, idx, params, m)
			},
			func(p string, idx int, m v31ee.HttpRequestRule) (*http.Response, error) {
				params := &v31ee.ReplaceHTTPRequestRuleBackendParams{TransactionId: &txID}
				return clientset.V31EE().ReplaceHTTPRequestRuleBackend(ctx, p, idx, params, m)
			},
			func(p string, idx int, m v30ee.HttpRequestRule) (*http.Response, error) {
				params := &v30ee.ReplaceHTTPRequestRuleBackendParams{TransactionId: &txID}
				return clientset.V30EE().ReplaceHTTPRequestRuleBackend(ctx, p, idx, params, m)
			},
		)
		return dispatchAndCheck(resp, err, "HTTP request rule update in backend")
	}
}

// HTTPRequestRuleBackendDelete returns an executor for deleting HTTP request rules from backends.
func HTTPRequestRuleBackendDelete() func(ctx context.Context, c *client.DataplaneClient, txID string, parent string, index int, _ *models.HTTPRequestRule) error {
	return func(ctx context.Context, c *client.DataplaneClient, txID string, parent string, index int, _ *models.HTTPRequestRule) error {
		clientset := c.Clientset()

		resp, err := client.DispatchDeleteChild(ctx, c, parent, index,
			func(p string, idx int) (*http.Response, error) {
				params := &v32.DeleteHTTPRequestRuleBackendParams{TransactionId: &txID}
				return clientset.V32().DeleteHTTPRequestRuleBackend(ctx, p, idx, params)
			},
			func(p string, idx int) (*http.Response, error) {
				params := &v31.DeleteHTTPRequestRuleBackendParams{TransactionId: &txID}
				return clientset.V31().DeleteHTTPRequestRuleBackend(ctx, p, idx, params)
			},
			func(p string, idx int) (*http.Response, error) {
				params := &v30.DeleteHTTPRequestRuleBackendParams{TransactionId: &txID}
				return clientset.V30().DeleteHTTPRequestRuleBackend(ctx, p, idx, params)
			},
			func(p string, idx int) (*http.Response, error) {
				params := &v32ee.DeleteHTTPRequestRuleBackendParams{TransactionId: &txID}
				return clientset.V32EE().DeleteHTTPRequestRuleBackend(ctx, p, idx, params)
			},
			func(p string, idx int) (*http.Response, error) {
				params := &v31ee.DeleteHTTPRequestRuleBackendParams{TransactionId: &txID}
				return clientset.V31EE().DeleteHTTPRequestRuleBackend(ctx, p, idx, params)
			},
			func(p string, idx int) (*http.Response, error) {
				params := &v30ee.DeleteHTTPRequestRuleBackendParams{TransactionId: &txID}
				return clientset.V30EE().DeleteHTTPRequestRuleBackend(ctx, p, idx, params)
			},
		)
		return dispatchAndCheck(resp, err, "HTTP request rule deletion from backend")
	}
}

// HTTPResponseRuleFrontendCreate returns an executor for creating HTTP response rules in frontends.
func HTTPResponseRuleFrontendCreate() func(ctx context.Context, c *client.DataplaneClient, txID string, parent string, index int, model *models.HTTPResponseRule) error {
	return func(ctx context.Context, c *client.DataplaneClient, txID string, parent string, index int, model *models.HTTPResponseRule) error {
		clientset := c.Clientset()

		resp, err := client.DispatchCreateChild(ctx, c, parent, index, model,
			func(p string, idx int, m v32.HttpResponseRule) (*http.Response, error) {
				params := &v32.CreateHTTPResponseRuleFrontendParams{TransactionId: &txID}
				return clientset.V32().CreateHTTPResponseRuleFrontend(ctx, p, idx, params, m)
			},
			func(p string, idx int, m v31.HttpResponseRule) (*http.Response, error) {
				params := &v31.CreateHTTPResponseRuleFrontendParams{TransactionId: &txID}
				return clientset.V31().CreateHTTPResponseRuleFrontend(ctx, p, idx, params, m)
			},
			func(p string, idx int, m v30.HttpResponseRule) (*http.Response, error) {
				params := &v30.CreateHTTPResponseRuleFrontendParams{TransactionId: &txID}
				return clientset.V30().CreateHTTPResponseRuleFrontend(ctx, p, idx, params, m)
			},
			func(p string, idx int, m v32ee.HttpResponseRule) (*http.Response, error) {
				params := &v32ee.CreateHTTPResponseRuleFrontendParams{TransactionId: &txID}
				return clientset.V32EE().CreateHTTPResponseRuleFrontend(ctx, p, idx, params, m)
			},
			func(p string, idx int, m v31ee.HttpResponseRule) (*http.Response, error) {
				params := &v31ee.CreateHTTPResponseRuleFrontendParams{TransactionId: &txID}
				return clientset.V31EE().CreateHTTPResponseRuleFrontend(ctx, p, idx, params, m)
			},
			func(p string, idx int, m v30ee.HttpResponseRule) (*http.Response, error) {
				params := &v30ee.CreateHTTPResponseRuleFrontendParams{TransactionId: &txID}
				return clientset.V30EE().CreateHTTPResponseRuleFrontend(ctx, p, idx, params, m)
			},
		)
		return dispatchAndCheck(resp, err, "HTTP response rule creation in frontend")
	}
}

// HTTPResponseRuleFrontendUpdate returns an executor for updating HTTP response rules in frontends.
func HTTPResponseRuleFrontendUpdate() func(ctx context.Context, c *client.DataplaneClient, txID string, parent string, index int, model *models.HTTPResponseRule) error {
	return func(ctx context.Context, c *client.DataplaneClient, txID string, parent string, index int, model *models.HTTPResponseRule) error {
		clientset := c.Clientset()

		resp, err := client.DispatchReplaceChild(ctx, c, parent, index, model,
			func(p string, idx int, m v32.HttpResponseRule) (*http.Response, error) {
				params := &v32.ReplaceHTTPResponseRuleFrontendParams{TransactionId: &txID}
				return clientset.V32().ReplaceHTTPResponseRuleFrontend(ctx, p, idx, params, m)
			},
			func(p string, idx int, m v31.HttpResponseRule) (*http.Response, error) {
				params := &v31.ReplaceHTTPResponseRuleFrontendParams{TransactionId: &txID}
				return clientset.V31().ReplaceHTTPResponseRuleFrontend(ctx, p, idx, params, m)
			},
			func(p string, idx int, m v30.HttpResponseRule) (*http.Response, error) {
				params := &v30.ReplaceHTTPResponseRuleFrontendParams{TransactionId: &txID}
				return clientset.V30().ReplaceHTTPResponseRuleFrontend(ctx, p, idx, params, m)
			},
			func(p string, idx int, m v32ee.HttpResponseRule) (*http.Response, error) {
				params := &v32ee.ReplaceHTTPResponseRuleFrontendParams{TransactionId: &txID}
				return clientset.V32EE().ReplaceHTTPResponseRuleFrontend(ctx, p, idx, params, m)
			},
			func(p string, idx int, m v31ee.HttpResponseRule) (*http.Response, error) {
				params := &v31ee.ReplaceHTTPResponseRuleFrontendParams{TransactionId: &txID}
				return clientset.V31EE().ReplaceHTTPResponseRuleFrontend(ctx, p, idx, params, m)
			},
			func(p string, idx int, m v30ee.HttpResponseRule) (*http.Response, error) {
				params := &v30ee.ReplaceHTTPResponseRuleFrontendParams{TransactionId: &txID}
				return clientset.V30EE().ReplaceHTTPResponseRuleFrontend(ctx, p, idx, params, m)
			},
		)
		return dispatchAndCheck(resp, err, "HTTP response rule update in frontend")
	}
}

// HTTPResponseRuleFrontendDelete returns an executor for deleting HTTP response rules from frontends.
func HTTPResponseRuleFrontendDelete() func(ctx context.Context, c *client.DataplaneClient, txID string, parent string, index int, _ *models.HTTPResponseRule) error {
	return func(ctx context.Context, c *client.DataplaneClient, txID string, parent string, index int, _ *models.HTTPResponseRule) error {
		clientset := c.Clientset()

		resp, err := client.DispatchDeleteChild(ctx, c, parent, index,
			func(p string, idx int) (*http.Response, error) {
				params := &v32.DeleteHTTPResponseRuleFrontendParams{TransactionId: &txID}
				return clientset.V32().DeleteHTTPResponseRuleFrontend(ctx, p, idx, params)
			},
			func(p string, idx int) (*http.Response, error) {
				params := &v31.DeleteHTTPResponseRuleFrontendParams{TransactionId: &txID}
				return clientset.V31().DeleteHTTPResponseRuleFrontend(ctx, p, idx, params)
			},
			func(p string, idx int) (*http.Response, error) {
				params := &v30.DeleteHTTPResponseRuleFrontendParams{TransactionId: &txID}
				return clientset.V30().DeleteHTTPResponseRuleFrontend(ctx, p, idx, params)
			},
			func(p string, idx int) (*http.Response, error) {
				params := &v32ee.DeleteHTTPResponseRuleFrontendParams{TransactionId: &txID}
				return clientset.V32EE().DeleteHTTPResponseRuleFrontend(ctx, p, idx, params)
			},
			func(p string, idx int) (*http.Response, error) {
				params := &v31ee.DeleteHTTPResponseRuleFrontendParams{TransactionId: &txID}
				return clientset.V31EE().DeleteHTTPResponseRuleFrontend(ctx, p, idx, params)
			},
			func(p string, idx int) (*http.Response, error) {
				params := &v30ee.DeleteHTTPResponseRuleFrontendParams{TransactionId: &txID}
				return clientset.V30EE().DeleteHTTPResponseRuleFrontend(ctx, p, idx, params)
			},
		)
		return dispatchAndCheck(resp, err, "HTTP response rule deletion from frontend")
	}
}

// HTTPResponseRuleBackendCreate returns an executor for creating HTTP response rules in backends.
func HTTPResponseRuleBackendCreate() func(ctx context.Context, c *client.DataplaneClient, txID string, parent string, index int, model *models.HTTPResponseRule) error {
	return func(ctx context.Context, c *client.DataplaneClient, txID string, parent string, index int, model *models.HTTPResponseRule) error {
		clientset := c.Clientset()

		resp, err := client.DispatchCreateChild(ctx, c, parent, index, model,
			func(p string, idx int, m v32.HttpResponseRule) (*http.Response, error) {
				params := &v32.CreateHTTPResponseRuleBackendParams{TransactionId: &txID}
				return clientset.V32().CreateHTTPResponseRuleBackend(ctx, p, idx, params, m)
			},
			func(p string, idx int, m v31.HttpResponseRule) (*http.Response, error) {
				params := &v31.CreateHTTPResponseRuleBackendParams{TransactionId: &txID}
				return clientset.V31().CreateHTTPResponseRuleBackend(ctx, p, idx, params, m)
			},
			func(p string, idx int, m v30.HttpResponseRule) (*http.Response, error) {
				params := &v30.CreateHTTPResponseRuleBackendParams{TransactionId: &txID}
				return clientset.V30().CreateHTTPResponseRuleBackend(ctx, p, idx, params, m)
			},
			func(p string, idx int, m v32ee.HttpResponseRule) (*http.Response, error) {
				params := &v32ee.CreateHTTPResponseRuleBackendParams{TransactionId: &txID}
				return clientset.V32EE().CreateHTTPResponseRuleBackend(ctx, p, idx, params, m)
			},
			func(p string, idx int, m v31ee.HttpResponseRule) (*http.Response, error) {
				params := &v31ee.CreateHTTPResponseRuleBackendParams{TransactionId: &txID}
				return clientset.V31EE().CreateHTTPResponseRuleBackend(ctx, p, idx, params, m)
			},
			func(p string, idx int, m v30ee.HttpResponseRule) (*http.Response, error) {
				params := &v30ee.CreateHTTPResponseRuleBackendParams{TransactionId: &txID}
				return clientset.V30EE().CreateHTTPResponseRuleBackend(ctx, p, idx, params, m)
			},
		)
		return dispatchAndCheck(resp, err, "HTTP response rule creation in backend")
	}
}

// HTTPResponseRuleBackendUpdate returns an executor for updating HTTP response rules in backends.
func HTTPResponseRuleBackendUpdate() func(ctx context.Context, c *client.DataplaneClient, txID string, parent string, index int, model *models.HTTPResponseRule) error {
	return func(ctx context.Context, c *client.DataplaneClient, txID string, parent string, index int, model *models.HTTPResponseRule) error {
		clientset := c.Clientset()

		resp, err := client.DispatchReplaceChild(ctx, c, parent, index, model,
			func(p string, idx int, m v32.HttpResponseRule) (*http.Response, error) {
				params := &v32.ReplaceHTTPResponseRuleBackendParams{TransactionId: &txID}
				return clientset.V32().ReplaceHTTPResponseRuleBackend(ctx, p, idx, params, m)
			},
			func(p string, idx int, m v31.HttpResponseRule) (*http.Response, error) {
				params := &v31.ReplaceHTTPResponseRuleBackendParams{TransactionId: &txID}
				return clientset.V31().ReplaceHTTPResponseRuleBackend(ctx, p, idx, params, m)
			},
			func(p string, idx int, m v30.HttpResponseRule) (*http.Response, error) {
				params := &v30.ReplaceHTTPResponseRuleBackendParams{TransactionId: &txID}
				return clientset.V30().ReplaceHTTPResponseRuleBackend(ctx, p, idx, params, m)
			},
			func(p string, idx int, m v32ee.HttpResponseRule) (*http.Response, error) {
				params := &v32ee.ReplaceHTTPResponseRuleBackendParams{TransactionId: &txID}
				return clientset.V32EE().ReplaceHTTPResponseRuleBackend(ctx, p, idx, params, m)
			},
			func(p string, idx int, m v31ee.HttpResponseRule) (*http.Response, error) {
				params := &v31ee.ReplaceHTTPResponseRuleBackendParams{TransactionId: &txID}
				return clientset.V31EE().ReplaceHTTPResponseRuleBackend(ctx, p, idx, params, m)
			},
			func(p string, idx int, m v30ee.HttpResponseRule) (*http.Response, error) {
				params := &v30ee.ReplaceHTTPResponseRuleBackendParams{TransactionId: &txID}
				return clientset.V30EE().ReplaceHTTPResponseRuleBackend(ctx, p, idx, params, m)
			},
		)
		return dispatchAndCheck(resp, err, "HTTP response rule update in backend")
	}
}

// HTTPResponseRuleBackendDelete returns an executor for deleting HTTP response rules from backends.
func HTTPResponseRuleBackendDelete() func(ctx context.Context, c *client.DataplaneClient, txID string, parent string, index int, _ *models.HTTPResponseRule) error {
	return func(ctx context.Context, c *client.DataplaneClient, txID string, parent string, index int, _ *models.HTTPResponseRule) error {
		clientset := c.Clientset()

		resp, err := client.DispatchDeleteChild(ctx, c, parent, index,
			func(p string, idx int) (*http.Response, error) {
				params := &v32.DeleteHTTPResponseRuleBackendParams{TransactionId: &txID}
				return clientset.V32().DeleteHTTPResponseRuleBackend(ctx, p, idx, params)
			},
			func(p string, idx int) (*http.Response, error) {
				params := &v31.DeleteHTTPResponseRuleBackendParams{TransactionId: &txID}
				return clientset.V31().DeleteHTTPResponseRuleBackend(ctx, p, idx, params)
			},
			func(p string, idx int) (*http.Response, error) {
				params := &v30.DeleteHTTPResponseRuleBackendParams{TransactionId: &txID}
				return clientset.V30().DeleteHTTPResponseRuleBackend(ctx, p, idx, params)
			},
			func(p string, idx int) (*http.Response, error) {
				params := &v32ee.DeleteHTTPResponseRuleBackendParams{TransactionId: &txID}
				return clientset.V32EE().DeleteHTTPResponseRuleBackend(ctx, p, idx, params)
			},
			func(p string, idx int) (*http.Response, error) {
				params := &v31ee.DeleteHTTPResponseRuleBackendParams{TransactionId: &txID}
				return clientset.V31EE().DeleteHTTPResponseRuleBackend(ctx, p, idx, params)
			},
			func(p string, idx int) (*http.Response, error) {
				params := &v30ee.DeleteHTTPResponseRuleBackendParams{TransactionId: &txID}
				return clientset.V30EE().DeleteHTTPResponseRuleBackend(ctx, p, idx, params)
			},
		)
		return dispatchAndCheck(resp, err, "HTTP response rule deletion from backend")
	}
}
