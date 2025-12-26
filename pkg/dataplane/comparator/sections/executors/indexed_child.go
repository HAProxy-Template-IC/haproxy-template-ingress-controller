// Package executors provides pre-built executor functions for HAProxy configuration operations.
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

// =============================================================================
// ACL Executors (Frontend)
// =============================================================================

// ACLFrontendCreate returns an executor for creating ACLs in frontends.
func ACLFrontendCreate() func(ctx context.Context, c *client.DataplaneClient, txID string, parent string, index int, model *models.ACL) error {
	return func(ctx context.Context, c *client.DataplaneClient, txID string, parent string, index int, model *models.ACL) error {
		clientset := c.Clientset()

		resp, err := client.DispatchCreateChild(ctx, c, parent, index, model,
			func(p string, idx int, m v32.Acl) (*http.Response, error) {
				params := &v32.CreateAclFrontendParams{TransactionId: &txID}
				return clientset.V32().CreateAclFrontend(ctx, p, idx, params, m)
			},
			func(p string, idx int, m v31.Acl) (*http.Response, error) {
				params := &v31.CreateAclFrontendParams{TransactionId: &txID}
				return clientset.V31().CreateAclFrontend(ctx, p, idx, params, m)
			},
			func(p string, idx int, m v30.Acl) (*http.Response, error) {
				params := &v30.CreateAclFrontendParams{TransactionId: &txID}
				return clientset.V30().CreateAclFrontend(ctx, p, idx, params, m)
			},
			func(p string, idx int, m v32ee.Acl) (*http.Response, error) {
				params := &v32ee.CreateAclFrontendParams{TransactionId: &txID}
				return clientset.V32EE().CreateAclFrontend(ctx, p, idx, params, m)
			},
			func(p string, idx int, m v31ee.Acl) (*http.Response, error) {
				params := &v31ee.CreateAclFrontendParams{TransactionId: &txID}
				return clientset.V31EE().CreateAclFrontend(ctx, p, idx, params, m)
			},
			func(p string, idx int, m v30ee.Acl) (*http.Response, error) {
				params := &v30ee.CreateAclFrontendParams{TransactionId: &txID}
				return clientset.V30EE().CreateAclFrontend(ctx, p, idx, params, m)
			},
		)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		return client.CheckResponse(resp, "ACL creation in frontend")
	}
}

// ACLFrontendUpdate returns an executor for updating ACLs in frontends.
func ACLFrontendUpdate() func(ctx context.Context, c *client.DataplaneClient, txID string, parent string, index int, model *models.ACL) error {
	return func(ctx context.Context, c *client.DataplaneClient, txID string, parent string, index int, model *models.ACL) error {
		clientset := c.Clientset()

		resp, err := client.DispatchReplaceChild(ctx, c, parent, index, model,
			func(p string, idx int, m v32.Acl) (*http.Response, error) {
				params := &v32.ReplaceAclFrontendParams{TransactionId: &txID}
				return clientset.V32().ReplaceAclFrontend(ctx, p, idx, params, m)
			},
			func(p string, idx int, m v31.Acl) (*http.Response, error) {
				params := &v31.ReplaceAclFrontendParams{TransactionId: &txID}
				return clientset.V31().ReplaceAclFrontend(ctx, p, idx, params, m)
			},
			func(p string, idx int, m v30.Acl) (*http.Response, error) {
				params := &v30.ReplaceAclFrontendParams{TransactionId: &txID}
				return clientset.V30().ReplaceAclFrontend(ctx, p, idx, params, m)
			},
			func(p string, idx int, m v32ee.Acl) (*http.Response, error) {
				params := &v32ee.ReplaceAclFrontendParams{TransactionId: &txID}
				return clientset.V32EE().ReplaceAclFrontend(ctx, p, idx, params, m)
			},
			func(p string, idx int, m v31ee.Acl) (*http.Response, error) {
				params := &v31ee.ReplaceAclFrontendParams{TransactionId: &txID}
				return clientset.V31EE().ReplaceAclFrontend(ctx, p, idx, params, m)
			},
			func(p string, idx int, m v30ee.Acl) (*http.Response, error) {
				params := &v30ee.ReplaceAclFrontendParams{TransactionId: &txID}
				return clientset.V30EE().ReplaceAclFrontend(ctx, p, idx, params, m)
			},
		)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		return client.CheckResponse(resp, "ACL update in frontend")
	}
}

// ACLFrontendDelete returns an executor for deleting ACLs from frontends.
func ACLFrontendDelete() func(ctx context.Context, c *client.DataplaneClient, txID string, parent string, index int, _ *models.ACL) error {
	return func(ctx context.Context, c *client.DataplaneClient, txID string, parent string, index int, _ *models.ACL) error {
		clientset := c.Clientset()

		resp, err := client.DispatchDeleteChild(ctx, c, parent, index,
			func(p string, idx int) (*http.Response, error) {
				params := &v32.DeleteAclFrontendParams{TransactionId: &txID}
				return clientset.V32().DeleteAclFrontend(ctx, p, idx, params)
			},
			func(p string, idx int) (*http.Response, error) {
				params := &v31.DeleteAclFrontendParams{TransactionId: &txID}
				return clientset.V31().DeleteAclFrontend(ctx, p, idx, params)
			},
			func(p string, idx int) (*http.Response, error) {
				params := &v30.DeleteAclFrontendParams{TransactionId: &txID}
				return clientset.V30().DeleteAclFrontend(ctx, p, idx, params)
			},
			func(p string, idx int) (*http.Response, error) {
				params := &v32ee.DeleteAclFrontendParams{TransactionId: &txID}
				return clientset.V32EE().DeleteAclFrontend(ctx, p, idx, params)
			},
			func(p string, idx int) (*http.Response, error) {
				params := &v31ee.DeleteAclFrontendParams{TransactionId: &txID}
				return clientset.V31EE().DeleteAclFrontend(ctx, p, idx, params)
			},
			func(p string, idx int) (*http.Response, error) {
				params := &v30ee.DeleteAclFrontendParams{TransactionId: &txID}
				return clientset.V30EE().DeleteAclFrontend(ctx, p, idx, params)
			},
		)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		return client.CheckResponse(resp, "ACL deletion from frontend")
	}
}

// =============================================================================
// ACL Executors (Backend)
// =============================================================================

// ACLBackendCreate returns an executor for creating ACLs in backends.
func ACLBackendCreate() func(ctx context.Context, c *client.DataplaneClient, txID string, parent string, index int, model *models.ACL) error {
	return func(ctx context.Context, c *client.DataplaneClient, txID string, parent string, index int, model *models.ACL) error {
		clientset := c.Clientset()

		resp, err := client.DispatchCreateChild(ctx, c, parent, index, model,
			func(p string, idx int, m v32.Acl) (*http.Response, error) {
				params := &v32.CreateAclBackendParams{TransactionId: &txID}
				return clientset.V32().CreateAclBackend(ctx, p, idx, params, m)
			},
			func(p string, idx int, m v31.Acl) (*http.Response, error) {
				params := &v31.CreateAclBackendParams{TransactionId: &txID}
				return clientset.V31().CreateAclBackend(ctx, p, idx, params, m)
			},
			func(p string, idx int, m v30.Acl) (*http.Response, error) {
				params := &v30.CreateAclBackendParams{TransactionId: &txID}
				return clientset.V30().CreateAclBackend(ctx, p, idx, params, m)
			},
			func(p string, idx int, m v32ee.Acl) (*http.Response, error) {
				params := &v32ee.CreateAclBackendParams{TransactionId: &txID}
				return clientset.V32EE().CreateAclBackend(ctx, p, idx, params, m)
			},
			func(p string, idx int, m v31ee.Acl) (*http.Response, error) {
				params := &v31ee.CreateAclBackendParams{TransactionId: &txID}
				return clientset.V31EE().CreateAclBackend(ctx, p, idx, params, m)
			},
			func(p string, idx int, m v30ee.Acl) (*http.Response, error) {
				params := &v30ee.CreateAclBackendParams{TransactionId: &txID}
				return clientset.V30EE().CreateAclBackend(ctx, p, idx, params, m)
			},
		)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		return client.CheckResponse(resp, "ACL creation in backend")
	}
}

// ACLBackendUpdate returns an executor for updating ACLs in backends.
func ACLBackendUpdate() func(ctx context.Context, c *client.DataplaneClient, txID string, parent string, index int, model *models.ACL) error {
	return func(ctx context.Context, c *client.DataplaneClient, txID string, parent string, index int, model *models.ACL) error {
		clientset := c.Clientset()

		resp, err := client.DispatchReplaceChild(ctx, c, parent, index, model,
			func(p string, idx int, m v32.Acl) (*http.Response, error) {
				params := &v32.ReplaceAclBackendParams{TransactionId: &txID}
				return clientset.V32().ReplaceAclBackend(ctx, p, idx, params, m)
			},
			func(p string, idx int, m v31.Acl) (*http.Response, error) {
				params := &v31.ReplaceAclBackendParams{TransactionId: &txID}
				return clientset.V31().ReplaceAclBackend(ctx, p, idx, params, m)
			},
			func(p string, idx int, m v30.Acl) (*http.Response, error) {
				params := &v30.ReplaceAclBackendParams{TransactionId: &txID}
				return clientset.V30().ReplaceAclBackend(ctx, p, idx, params, m)
			},
			func(p string, idx int, m v32ee.Acl) (*http.Response, error) {
				params := &v32ee.ReplaceAclBackendParams{TransactionId: &txID}
				return clientset.V32EE().ReplaceAclBackend(ctx, p, idx, params, m)
			},
			func(p string, idx int, m v31ee.Acl) (*http.Response, error) {
				params := &v31ee.ReplaceAclBackendParams{TransactionId: &txID}
				return clientset.V31EE().ReplaceAclBackend(ctx, p, idx, params, m)
			},
			func(p string, idx int, m v30ee.Acl) (*http.Response, error) {
				params := &v30ee.ReplaceAclBackendParams{TransactionId: &txID}
				return clientset.V30EE().ReplaceAclBackend(ctx, p, idx, params, m)
			},
		)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		return client.CheckResponse(resp, "ACL update in backend")
	}
}

// ACLBackendDelete returns an executor for deleting ACLs from backends.
func ACLBackendDelete() func(ctx context.Context, c *client.DataplaneClient, txID string, parent string, index int, _ *models.ACL) error {
	return func(ctx context.Context, c *client.DataplaneClient, txID string, parent string, index int, _ *models.ACL) error {
		clientset := c.Clientset()

		resp, err := client.DispatchDeleteChild(ctx, c, parent, index,
			func(p string, idx int) (*http.Response, error) {
				params := &v32.DeleteAclBackendParams{TransactionId: &txID}
				return clientset.V32().DeleteAclBackend(ctx, p, idx, params)
			},
			func(p string, idx int) (*http.Response, error) {
				params := &v31.DeleteAclBackendParams{TransactionId: &txID}
				return clientset.V31().DeleteAclBackend(ctx, p, idx, params)
			},
			func(p string, idx int) (*http.Response, error) {
				params := &v30.DeleteAclBackendParams{TransactionId: &txID}
				return clientset.V30().DeleteAclBackend(ctx, p, idx, params)
			},
			func(p string, idx int) (*http.Response, error) {
				params := &v32ee.DeleteAclBackendParams{TransactionId: &txID}
				return clientset.V32EE().DeleteAclBackend(ctx, p, idx, params)
			},
			func(p string, idx int) (*http.Response, error) {
				params := &v31ee.DeleteAclBackendParams{TransactionId: &txID}
				return clientset.V31EE().DeleteAclBackend(ctx, p, idx, params)
			},
			func(p string, idx int) (*http.Response, error) {
				params := &v30ee.DeleteAclBackendParams{TransactionId: &txID}
				return clientset.V30EE().DeleteAclBackend(ctx, p, idx, params)
			},
		)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		return client.CheckResponse(resp, "ACL deletion from backend")
	}
}

// =============================================================================
// HTTP Request Rule Executors (Frontend)
// =============================================================================

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
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		return client.CheckResponse(resp, "HTTP request rule creation in frontend")
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
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		return client.CheckResponse(resp, "HTTP request rule update in frontend")
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
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		return client.CheckResponse(resp, "HTTP request rule deletion from frontend")
	}
}

// =============================================================================
// HTTP Request Rule Executors (Backend)
// =============================================================================

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
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		return client.CheckResponse(resp, "HTTP request rule creation in backend")
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
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		return client.CheckResponse(resp, "HTTP request rule update in backend")
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
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		return client.CheckResponse(resp, "HTTP request rule deletion from backend")
	}
}

// =============================================================================
// HTTP Response Rule Executors (Frontend)
// =============================================================================

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
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		return client.CheckResponse(resp, "HTTP response rule creation in frontend")
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
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		return client.CheckResponse(resp, "HTTP response rule update in frontend")
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
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		return client.CheckResponse(resp, "HTTP response rule deletion from frontend")
	}
}

// =============================================================================
// HTTP Response Rule Executors (Backend)
// =============================================================================

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
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		return client.CheckResponse(resp, "HTTP response rule creation in backend")
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
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		return client.CheckResponse(resp, "HTTP response rule update in backend")
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
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		return client.CheckResponse(resp, "HTTP response rule deletion from backend")
	}
}

// =============================================================================
// Backend Switching Rule Executors
// =============================================================================

// BackendSwitchingRuleCreate returns an executor for creating backend switching rules.
func BackendSwitchingRuleCreate() func(ctx context.Context, c *client.DataplaneClient, txID string, parent string, index int, model *models.BackendSwitchingRule) error {
	return func(ctx context.Context, c *client.DataplaneClient, txID string, parent string, index int, model *models.BackendSwitchingRule) error {
		clientset := c.Clientset()

		resp, err := client.DispatchCreateChild(ctx, c, parent, index, model,
			func(p string, idx int, m v32.BackendSwitchingRule) (*http.Response, error) {
				params := &v32.CreateBackendSwitchingRuleParams{TransactionId: &txID}
				return clientset.V32().CreateBackendSwitchingRule(ctx, p, idx, params, m)
			},
			func(p string, idx int, m v31.BackendSwitchingRule) (*http.Response, error) {
				params := &v31.CreateBackendSwitchingRuleParams{TransactionId: &txID}
				return clientset.V31().CreateBackendSwitchingRule(ctx, p, idx, params, m)
			},
			func(p string, idx int, m v30.BackendSwitchingRule) (*http.Response, error) {
				params := &v30.CreateBackendSwitchingRuleParams{TransactionId: &txID}
				return clientset.V30().CreateBackendSwitchingRule(ctx, p, idx, params, m)
			},
			func(p string, idx int, m v32ee.BackendSwitchingRule) (*http.Response, error) {
				params := &v32ee.CreateBackendSwitchingRuleParams{TransactionId: &txID}
				return clientset.V32EE().CreateBackendSwitchingRule(ctx, p, idx, params, m)
			},
			func(p string, idx int, m v31ee.BackendSwitchingRule) (*http.Response, error) {
				params := &v31ee.CreateBackendSwitchingRuleParams{TransactionId: &txID}
				return clientset.V31EE().CreateBackendSwitchingRule(ctx, p, idx, params, m)
			},
			func(p string, idx int, m v30ee.BackendSwitchingRule) (*http.Response, error) {
				params := &v30ee.CreateBackendSwitchingRuleParams{TransactionId: &txID}
				return clientset.V30EE().CreateBackendSwitchingRule(ctx, p, idx, params, m)
			},
		)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		return client.CheckResponse(resp, "backend switching rule creation")
	}
}

// BackendSwitchingRuleUpdate returns an executor for updating backend switching rules.
func BackendSwitchingRuleUpdate() func(ctx context.Context, c *client.DataplaneClient, txID string, parent string, index int, model *models.BackendSwitchingRule) error {
	return func(ctx context.Context, c *client.DataplaneClient, txID string, parent string, index int, model *models.BackendSwitchingRule) error {
		clientset := c.Clientset()

		resp, err := client.DispatchReplaceChild(ctx, c, parent, index, model,
			func(p string, idx int, m v32.BackendSwitchingRule) (*http.Response, error) {
				params := &v32.ReplaceBackendSwitchingRuleParams{TransactionId: &txID}
				return clientset.V32().ReplaceBackendSwitchingRule(ctx, p, idx, params, m)
			},
			func(p string, idx int, m v31.BackendSwitchingRule) (*http.Response, error) {
				params := &v31.ReplaceBackendSwitchingRuleParams{TransactionId: &txID}
				return clientset.V31().ReplaceBackendSwitchingRule(ctx, p, idx, params, m)
			},
			func(p string, idx int, m v30.BackendSwitchingRule) (*http.Response, error) {
				params := &v30.ReplaceBackendSwitchingRuleParams{TransactionId: &txID}
				return clientset.V30().ReplaceBackendSwitchingRule(ctx, p, idx, params, m)
			},
			func(p string, idx int, m v32ee.BackendSwitchingRule) (*http.Response, error) {
				params := &v32ee.ReplaceBackendSwitchingRuleParams{TransactionId: &txID}
				return clientset.V32EE().ReplaceBackendSwitchingRule(ctx, p, idx, params, m)
			},
			func(p string, idx int, m v31ee.BackendSwitchingRule) (*http.Response, error) {
				params := &v31ee.ReplaceBackendSwitchingRuleParams{TransactionId: &txID}
				return clientset.V31EE().ReplaceBackendSwitchingRule(ctx, p, idx, params, m)
			},
			func(p string, idx int, m v30ee.BackendSwitchingRule) (*http.Response, error) {
				params := &v30ee.ReplaceBackendSwitchingRuleParams{TransactionId: &txID}
				return clientset.V30EE().ReplaceBackendSwitchingRule(ctx, p, idx, params, m)
			},
		)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		return client.CheckResponse(resp, "backend switching rule update")
	}
}

// BackendSwitchingRuleDelete returns an executor for deleting backend switching rules.
func BackendSwitchingRuleDelete() func(ctx context.Context, c *client.DataplaneClient, txID string, parent string, index int, _ *models.BackendSwitchingRule) error {
	return func(ctx context.Context, c *client.DataplaneClient, txID string, parent string, index int, _ *models.BackendSwitchingRule) error {
		clientset := c.Clientset()

		resp, err := client.DispatchDeleteChild(ctx, c, parent, index,
			func(p string, idx int) (*http.Response, error) {
				params := &v32.DeleteBackendSwitchingRuleParams{TransactionId: &txID}
				return clientset.V32().DeleteBackendSwitchingRule(ctx, p, idx, params)
			},
			func(p string, idx int) (*http.Response, error) {
				params := &v31.DeleteBackendSwitchingRuleParams{TransactionId: &txID}
				return clientset.V31().DeleteBackendSwitchingRule(ctx, p, idx, params)
			},
			func(p string, idx int) (*http.Response, error) {
				params := &v30.DeleteBackendSwitchingRuleParams{TransactionId: &txID}
				return clientset.V30().DeleteBackendSwitchingRule(ctx, p, idx, params)
			},
			func(p string, idx int) (*http.Response, error) {
				params := &v32ee.DeleteBackendSwitchingRuleParams{TransactionId: &txID}
				return clientset.V32EE().DeleteBackendSwitchingRule(ctx, p, idx, params)
			},
			func(p string, idx int) (*http.Response, error) {
				params := &v31ee.DeleteBackendSwitchingRuleParams{TransactionId: &txID}
				return clientset.V31EE().DeleteBackendSwitchingRule(ctx, p, idx, params)
			},
			func(p string, idx int) (*http.Response, error) {
				params := &v30ee.DeleteBackendSwitchingRuleParams{TransactionId: &txID}
				return clientset.V30EE().DeleteBackendSwitchingRule(ctx, p, idx, params)
			},
		)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		return client.CheckResponse(resp, "backend switching rule deletion")
	}
}

// =============================================================================
// Filter Executors (Frontend)
// =============================================================================

// FilterFrontendCreate returns an executor for creating filters in frontends.
func FilterFrontendCreate() func(ctx context.Context, c *client.DataplaneClient, txID string, parent string, index int, model *models.Filter) error {
	return func(ctx context.Context, c *client.DataplaneClient, txID string, parent string, index int, model *models.Filter) error {
		clientset := c.Clientset()

		resp, err := client.DispatchCreateChild(ctx, c, parent, index, model,
			func(p string, idx int, m v32.Filter) (*http.Response, error) {
				params := &v32.CreateFilterFrontendParams{TransactionId: &txID}
				return clientset.V32().CreateFilterFrontend(ctx, p, idx, params, m)
			},
			func(p string, idx int, m v31.Filter) (*http.Response, error) {
				params := &v31.CreateFilterFrontendParams{TransactionId: &txID}
				return clientset.V31().CreateFilterFrontend(ctx, p, idx, params, m)
			},
			func(p string, idx int, m v30.Filter) (*http.Response, error) {
				params := &v30.CreateFilterFrontendParams{TransactionId: &txID}
				return clientset.V30().CreateFilterFrontend(ctx, p, idx, params, m)
			},
			func(p string, idx int, m v32ee.Filter) (*http.Response, error) {
				params := &v32ee.CreateFilterFrontendParams{TransactionId: &txID}
				return clientset.V32EE().CreateFilterFrontend(ctx, p, idx, params, m)
			},
			func(p string, idx int, m v31ee.Filter) (*http.Response, error) {
				params := &v31ee.CreateFilterFrontendParams{TransactionId: &txID}
				return clientset.V31EE().CreateFilterFrontend(ctx, p, idx, params, m)
			},
			func(p string, idx int, m v30ee.Filter) (*http.Response, error) {
				params := &v30ee.CreateFilterFrontendParams{TransactionId: &txID}
				return clientset.V30EE().CreateFilterFrontend(ctx, p, idx, params, m)
			},
		)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		return client.CheckResponse(resp, "filter creation in frontend")
	}
}

// FilterFrontendUpdate returns an executor for updating filters in frontends.
func FilterFrontendUpdate() func(ctx context.Context, c *client.DataplaneClient, txID string, parent string, index int, model *models.Filter) error {
	return func(ctx context.Context, c *client.DataplaneClient, txID string, parent string, index int, model *models.Filter) error {
		clientset := c.Clientset()

		resp, err := client.DispatchReplaceChild(ctx, c, parent, index, model,
			func(p string, idx int, m v32.Filter) (*http.Response, error) {
				params := &v32.ReplaceFilterFrontendParams{TransactionId: &txID}
				return clientset.V32().ReplaceFilterFrontend(ctx, p, idx, params, m)
			},
			func(p string, idx int, m v31.Filter) (*http.Response, error) {
				params := &v31.ReplaceFilterFrontendParams{TransactionId: &txID}
				return clientset.V31().ReplaceFilterFrontend(ctx, p, idx, params, m)
			},
			func(p string, idx int, m v30.Filter) (*http.Response, error) {
				params := &v30.ReplaceFilterFrontendParams{TransactionId: &txID}
				return clientset.V30().ReplaceFilterFrontend(ctx, p, idx, params, m)
			},
			func(p string, idx int, m v32ee.Filter) (*http.Response, error) {
				params := &v32ee.ReplaceFilterFrontendParams{TransactionId: &txID}
				return clientset.V32EE().ReplaceFilterFrontend(ctx, p, idx, params, m)
			},
			func(p string, idx int, m v31ee.Filter) (*http.Response, error) {
				params := &v31ee.ReplaceFilterFrontendParams{TransactionId: &txID}
				return clientset.V31EE().ReplaceFilterFrontend(ctx, p, idx, params, m)
			},
			func(p string, idx int, m v30ee.Filter) (*http.Response, error) {
				params := &v30ee.ReplaceFilterFrontendParams{TransactionId: &txID}
				return clientset.V30EE().ReplaceFilterFrontend(ctx, p, idx, params, m)
			},
		)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		return client.CheckResponse(resp, "filter update in frontend")
	}
}

// FilterFrontendDelete returns an executor for deleting filters from frontends.
func FilterFrontendDelete() func(ctx context.Context, c *client.DataplaneClient, txID string, parent string, index int, _ *models.Filter) error {
	return func(ctx context.Context, c *client.DataplaneClient, txID string, parent string, index int, _ *models.Filter) error {
		clientset := c.Clientset()

		resp, err := client.DispatchDeleteChild(ctx, c, parent, index,
			func(p string, idx int) (*http.Response, error) {
				params := &v32.DeleteFilterFrontendParams{TransactionId: &txID}
				return clientset.V32().DeleteFilterFrontend(ctx, p, idx, params)
			},
			func(p string, idx int) (*http.Response, error) {
				params := &v31.DeleteFilterFrontendParams{TransactionId: &txID}
				return clientset.V31().DeleteFilterFrontend(ctx, p, idx, params)
			},
			func(p string, idx int) (*http.Response, error) {
				params := &v30.DeleteFilterFrontendParams{TransactionId: &txID}
				return clientset.V30().DeleteFilterFrontend(ctx, p, idx, params)
			},
			func(p string, idx int) (*http.Response, error) {
				params := &v32ee.DeleteFilterFrontendParams{TransactionId: &txID}
				return clientset.V32EE().DeleteFilterFrontend(ctx, p, idx, params)
			},
			func(p string, idx int) (*http.Response, error) {
				params := &v31ee.DeleteFilterFrontendParams{TransactionId: &txID}
				return clientset.V31EE().DeleteFilterFrontend(ctx, p, idx, params)
			},
			func(p string, idx int) (*http.Response, error) {
				params := &v30ee.DeleteFilterFrontendParams{TransactionId: &txID}
				return clientset.V30EE().DeleteFilterFrontend(ctx, p, idx, params)
			},
		)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		return client.CheckResponse(resp, "filter deletion from frontend")
	}
}

// =============================================================================
// Filter Executors (Backend)
// =============================================================================

// FilterBackendCreate returns an executor for creating filters in backends.
func FilterBackendCreate() func(ctx context.Context, c *client.DataplaneClient, txID string, parent string, index int, model *models.Filter) error {
	return func(ctx context.Context, c *client.DataplaneClient, txID string, parent string, index int, model *models.Filter) error {
		clientset := c.Clientset()

		resp, err := client.DispatchCreateChild(ctx, c, parent, index, model,
			func(p string, idx int, m v32.Filter) (*http.Response, error) {
				params := &v32.CreateFilterBackendParams{TransactionId: &txID}
				return clientset.V32().CreateFilterBackend(ctx, p, idx, params, m)
			},
			func(p string, idx int, m v31.Filter) (*http.Response, error) {
				params := &v31.CreateFilterBackendParams{TransactionId: &txID}
				return clientset.V31().CreateFilterBackend(ctx, p, idx, params, m)
			},
			func(p string, idx int, m v30.Filter) (*http.Response, error) {
				params := &v30.CreateFilterBackendParams{TransactionId: &txID}
				return clientset.V30().CreateFilterBackend(ctx, p, idx, params, m)
			},
			func(p string, idx int, m v32ee.Filter) (*http.Response, error) {
				params := &v32ee.CreateFilterBackendParams{TransactionId: &txID}
				return clientset.V32EE().CreateFilterBackend(ctx, p, idx, params, m)
			},
			func(p string, idx int, m v31ee.Filter) (*http.Response, error) {
				params := &v31ee.CreateFilterBackendParams{TransactionId: &txID}
				return clientset.V31EE().CreateFilterBackend(ctx, p, idx, params, m)
			},
			func(p string, idx int, m v30ee.Filter) (*http.Response, error) {
				params := &v30ee.CreateFilterBackendParams{TransactionId: &txID}
				return clientset.V30EE().CreateFilterBackend(ctx, p, idx, params, m)
			},
		)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		return client.CheckResponse(resp, "filter creation in backend")
	}
}

// FilterBackendUpdate returns an executor for updating filters in backends.
func FilterBackendUpdate() func(ctx context.Context, c *client.DataplaneClient, txID string, parent string, index int, model *models.Filter) error {
	return func(ctx context.Context, c *client.DataplaneClient, txID string, parent string, index int, model *models.Filter) error {
		clientset := c.Clientset()

		resp, err := client.DispatchReplaceChild(ctx, c, parent, index, model,
			func(p string, idx int, m v32.Filter) (*http.Response, error) {
				params := &v32.ReplaceFilterBackendParams{TransactionId: &txID}
				return clientset.V32().ReplaceFilterBackend(ctx, p, idx, params, m)
			},
			func(p string, idx int, m v31.Filter) (*http.Response, error) {
				params := &v31.ReplaceFilterBackendParams{TransactionId: &txID}
				return clientset.V31().ReplaceFilterBackend(ctx, p, idx, params, m)
			},
			func(p string, idx int, m v30.Filter) (*http.Response, error) {
				params := &v30.ReplaceFilterBackendParams{TransactionId: &txID}
				return clientset.V30().ReplaceFilterBackend(ctx, p, idx, params, m)
			},
			func(p string, idx int, m v32ee.Filter) (*http.Response, error) {
				params := &v32ee.ReplaceFilterBackendParams{TransactionId: &txID}
				return clientset.V32EE().ReplaceFilterBackend(ctx, p, idx, params, m)
			},
			func(p string, idx int, m v31ee.Filter) (*http.Response, error) {
				params := &v31ee.ReplaceFilterBackendParams{TransactionId: &txID}
				return clientset.V31EE().ReplaceFilterBackend(ctx, p, idx, params, m)
			},
			func(p string, idx int, m v30ee.Filter) (*http.Response, error) {
				params := &v30ee.ReplaceFilterBackendParams{TransactionId: &txID}
				return clientset.V30EE().ReplaceFilterBackend(ctx, p, idx, params, m)
			},
		)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		return client.CheckResponse(resp, "filter update in backend")
	}
}

// FilterBackendDelete returns an executor for deleting filters from backends.
func FilterBackendDelete() func(ctx context.Context, c *client.DataplaneClient, txID string, parent string, index int, _ *models.Filter) error {
	return func(ctx context.Context, c *client.DataplaneClient, txID string, parent string, index int, _ *models.Filter) error {
		clientset := c.Clientset()

		resp, err := client.DispatchDeleteChild(ctx, c, parent, index,
			func(p string, idx int) (*http.Response, error) {
				params := &v32.DeleteFilterBackendParams{TransactionId: &txID}
				return clientset.V32().DeleteFilterBackend(ctx, p, idx, params)
			},
			func(p string, idx int) (*http.Response, error) {
				params := &v31.DeleteFilterBackendParams{TransactionId: &txID}
				return clientset.V31().DeleteFilterBackend(ctx, p, idx, params)
			},
			func(p string, idx int) (*http.Response, error) {
				params := &v30.DeleteFilterBackendParams{TransactionId: &txID}
				return clientset.V30().DeleteFilterBackend(ctx, p, idx, params)
			},
			func(p string, idx int) (*http.Response, error) {
				params := &v32ee.DeleteFilterBackendParams{TransactionId: &txID}
				return clientset.V32EE().DeleteFilterBackend(ctx, p, idx, params)
			},
			func(p string, idx int) (*http.Response, error) {
				params := &v31ee.DeleteFilterBackendParams{TransactionId: &txID}
				return clientset.V31EE().DeleteFilterBackend(ctx, p, idx, params)
			},
			func(p string, idx int) (*http.Response, error) {
				params := &v30ee.DeleteFilterBackendParams{TransactionId: &txID}
				return clientset.V30EE().DeleteFilterBackend(ctx, p, idx, params)
			},
		)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		return client.CheckResponse(resp, "filter deletion from backend")
	}
}

// =============================================================================
// Log Target Executors (Frontend)
// =============================================================================

// LogTargetFrontendCreate returns an executor for creating log targets in frontends.
func LogTargetFrontendCreate() func(ctx context.Context, c *client.DataplaneClient, txID string, parent string, index int, model *models.LogTarget) error {
	return func(ctx context.Context, c *client.DataplaneClient, txID string, parent string, index int, model *models.LogTarget) error {
		clientset := c.Clientset()

		resp, err := client.DispatchCreateChild(ctx, c, parent, index, model,
			func(p string, idx int, m v32.LogTarget) (*http.Response, error) {
				params := &v32.CreateLogTargetFrontendParams{TransactionId: &txID}
				return clientset.V32().CreateLogTargetFrontend(ctx, p, idx, params, m)
			},
			func(p string, idx int, m v31.LogTarget) (*http.Response, error) {
				params := &v31.CreateLogTargetFrontendParams{TransactionId: &txID}
				return clientset.V31().CreateLogTargetFrontend(ctx, p, idx, params, m)
			},
			func(p string, idx int, m v30.LogTarget) (*http.Response, error) {
				params := &v30.CreateLogTargetFrontendParams{TransactionId: &txID}
				return clientset.V30().CreateLogTargetFrontend(ctx, p, idx, params, m)
			},
			func(p string, idx int, m v32ee.LogTarget) (*http.Response, error) {
				params := &v32ee.CreateLogTargetFrontendParams{TransactionId: &txID}
				return clientset.V32EE().CreateLogTargetFrontend(ctx, p, idx, params, m)
			},
			func(p string, idx int, m v31ee.LogTarget) (*http.Response, error) {
				params := &v31ee.CreateLogTargetFrontendParams{TransactionId: &txID}
				return clientset.V31EE().CreateLogTargetFrontend(ctx, p, idx, params, m)
			},
			func(p string, idx int, m v30ee.LogTarget) (*http.Response, error) {
				params := &v30ee.CreateLogTargetFrontendParams{TransactionId: &txID}
				return clientset.V30EE().CreateLogTargetFrontend(ctx, p, idx, params, m)
			},
		)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		return client.CheckResponse(resp, "log target creation in frontend")
	}
}

// LogTargetFrontendUpdate returns an executor for updating log targets in frontends.
func LogTargetFrontendUpdate() func(ctx context.Context, c *client.DataplaneClient, txID string, parent string, index int, model *models.LogTarget) error {
	return func(ctx context.Context, c *client.DataplaneClient, txID string, parent string, index int, model *models.LogTarget) error {
		clientset := c.Clientset()

		resp, err := client.DispatchReplaceChild(ctx, c, parent, index, model,
			func(p string, idx int, m v32.LogTarget) (*http.Response, error) {
				params := &v32.ReplaceLogTargetFrontendParams{TransactionId: &txID}
				return clientset.V32().ReplaceLogTargetFrontend(ctx, p, idx, params, m)
			},
			func(p string, idx int, m v31.LogTarget) (*http.Response, error) {
				params := &v31.ReplaceLogTargetFrontendParams{TransactionId: &txID}
				return clientset.V31().ReplaceLogTargetFrontend(ctx, p, idx, params, m)
			},
			func(p string, idx int, m v30.LogTarget) (*http.Response, error) {
				params := &v30.ReplaceLogTargetFrontendParams{TransactionId: &txID}
				return clientset.V30().ReplaceLogTargetFrontend(ctx, p, idx, params, m)
			},
			func(p string, idx int, m v32ee.LogTarget) (*http.Response, error) {
				params := &v32ee.ReplaceLogTargetFrontendParams{TransactionId: &txID}
				return clientset.V32EE().ReplaceLogTargetFrontend(ctx, p, idx, params, m)
			},
			func(p string, idx int, m v31ee.LogTarget) (*http.Response, error) {
				params := &v31ee.ReplaceLogTargetFrontendParams{TransactionId: &txID}
				return clientset.V31EE().ReplaceLogTargetFrontend(ctx, p, idx, params, m)
			},
			func(p string, idx int, m v30ee.LogTarget) (*http.Response, error) {
				params := &v30ee.ReplaceLogTargetFrontendParams{TransactionId: &txID}
				return clientset.V30EE().ReplaceLogTargetFrontend(ctx, p, idx, params, m)
			},
		)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		return client.CheckResponse(resp, "log target update in frontend")
	}
}

// LogTargetFrontendDelete returns an executor for deleting log targets from frontends.
func LogTargetFrontendDelete() func(ctx context.Context, c *client.DataplaneClient, txID string, parent string, index int, _ *models.LogTarget) error {
	return func(ctx context.Context, c *client.DataplaneClient, txID string, parent string, index int, _ *models.LogTarget) error {
		clientset := c.Clientset()

		resp, err := client.DispatchDeleteChild(ctx, c, parent, index,
			func(p string, idx int) (*http.Response, error) {
				params := &v32.DeleteLogTargetFrontendParams{TransactionId: &txID}
				return clientset.V32().DeleteLogTargetFrontend(ctx, p, idx, params)
			},
			func(p string, idx int) (*http.Response, error) {
				params := &v31.DeleteLogTargetFrontendParams{TransactionId: &txID}
				return clientset.V31().DeleteLogTargetFrontend(ctx, p, idx, params)
			},
			func(p string, idx int) (*http.Response, error) {
				params := &v30.DeleteLogTargetFrontendParams{TransactionId: &txID}
				return clientset.V30().DeleteLogTargetFrontend(ctx, p, idx, params)
			},
			func(p string, idx int) (*http.Response, error) {
				params := &v32ee.DeleteLogTargetFrontendParams{TransactionId: &txID}
				return clientset.V32EE().DeleteLogTargetFrontend(ctx, p, idx, params)
			},
			func(p string, idx int) (*http.Response, error) {
				params := &v31ee.DeleteLogTargetFrontendParams{TransactionId: &txID}
				return clientset.V31EE().DeleteLogTargetFrontend(ctx, p, idx, params)
			},
			func(p string, idx int) (*http.Response, error) {
				params := &v30ee.DeleteLogTargetFrontendParams{TransactionId: &txID}
				return clientset.V30EE().DeleteLogTargetFrontend(ctx, p, idx, params)
			},
		)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		return client.CheckResponse(resp, "log target deletion from frontend")
	}
}

// =============================================================================
// Log Target Executors (Backend)
// =============================================================================

// LogTargetBackendCreate returns an executor for creating log targets in backends.
func LogTargetBackendCreate() func(ctx context.Context, c *client.DataplaneClient, txID string, parent string, index int, model *models.LogTarget) error {
	return func(ctx context.Context, c *client.DataplaneClient, txID string, parent string, index int, model *models.LogTarget) error {
		clientset := c.Clientset()

		resp, err := client.DispatchCreateChild(ctx, c, parent, index, model,
			func(p string, idx int, m v32.LogTarget) (*http.Response, error) {
				params := &v32.CreateLogTargetBackendParams{TransactionId: &txID}
				return clientset.V32().CreateLogTargetBackend(ctx, p, idx, params, m)
			},
			func(p string, idx int, m v31.LogTarget) (*http.Response, error) {
				params := &v31.CreateLogTargetBackendParams{TransactionId: &txID}
				return clientset.V31().CreateLogTargetBackend(ctx, p, idx, params, m)
			},
			func(p string, idx int, m v30.LogTarget) (*http.Response, error) {
				params := &v30.CreateLogTargetBackendParams{TransactionId: &txID}
				return clientset.V30().CreateLogTargetBackend(ctx, p, idx, params, m)
			},
			func(p string, idx int, m v32ee.LogTarget) (*http.Response, error) {
				params := &v32ee.CreateLogTargetBackendParams{TransactionId: &txID}
				return clientset.V32EE().CreateLogTargetBackend(ctx, p, idx, params, m)
			},
			func(p string, idx int, m v31ee.LogTarget) (*http.Response, error) {
				params := &v31ee.CreateLogTargetBackendParams{TransactionId: &txID}
				return clientset.V31EE().CreateLogTargetBackend(ctx, p, idx, params, m)
			},
			func(p string, idx int, m v30ee.LogTarget) (*http.Response, error) {
				params := &v30ee.CreateLogTargetBackendParams{TransactionId: &txID}
				return clientset.V30EE().CreateLogTargetBackend(ctx, p, idx, params, m)
			},
		)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		return client.CheckResponse(resp, "log target creation in backend")
	}
}

// LogTargetBackendUpdate returns an executor for updating log targets in backends.
func LogTargetBackendUpdate() func(ctx context.Context, c *client.DataplaneClient, txID string, parent string, index int, model *models.LogTarget) error {
	return func(ctx context.Context, c *client.DataplaneClient, txID string, parent string, index int, model *models.LogTarget) error {
		clientset := c.Clientset()

		resp, err := client.DispatchReplaceChild(ctx, c, parent, index, model,
			func(p string, idx int, m v32.LogTarget) (*http.Response, error) {
				params := &v32.ReplaceLogTargetBackendParams{TransactionId: &txID}
				return clientset.V32().ReplaceLogTargetBackend(ctx, p, idx, params, m)
			},
			func(p string, idx int, m v31.LogTarget) (*http.Response, error) {
				params := &v31.ReplaceLogTargetBackendParams{TransactionId: &txID}
				return clientset.V31().ReplaceLogTargetBackend(ctx, p, idx, params, m)
			},
			func(p string, idx int, m v30.LogTarget) (*http.Response, error) {
				params := &v30.ReplaceLogTargetBackendParams{TransactionId: &txID}
				return clientset.V30().ReplaceLogTargetBackend(ctx, p, idx, params, m)
			},
			func(p string, idx int, m v32ee.LogTarget) (*http.Response, error) {
				params := &v32ee.ReplaceLogTargetBackendParams{TransactionId: &txID}
				return clientset.V32EE().ReplaceLogTargetBackend(ctx, p, idx, params, m)
			},
			func(p string, idx int, m v31ee.LogTarget) (*http.Response, error) {
				params := &v31ee.ReplaceLogTargetBackendParams{TransactionId: &txID}
				return clientset.V31EE().ReplaceLogTargetBackend(ctx, p, idx, params, m)
			},
			func(p string, idx int, m v30ee.LogTarget) (*http.Response, error) {
				params := &v30ee.ReplaceLogTargetBackendParams{TransactionId: &txID}
				return clientset.V30EE().ReplaceLogTargetBackend(ctx, p, idx, params, m)
			},
		)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		return client.CheckResponse(resp, "log target update in backend")
	}
}

// LogTargetBackendDelete returns an executor for deleting log targets from backends.
func LogTargetBackendDelete() func(ctx context.Context, c *client.DataplaneClient, txID string, parent string, index int, _ *models.LogTarget) error {
	return func(ctx context.Context, c *client.DataplaneClient, txID string, parent string, index int, _ *models.LogTarget) error {
		clientset := c.Clientset()

		resp, err := client.DispatchDeleteChild(ctx, c, parent, index,
			func(p string, idx int) (*http.Response, error) {
				params := &v32.DeleteLogTargetBackendParams{TransactionId: &txID}
				return clientset.V32().DeleteLogTargetBackend(ctx, p, idx, params)
			},
			func(p string, idx int) (*http.Response, error) {
				params := &v31.DeleteLogTargetBackendParams{TransactionId: &txID}
				return clientset.V31().DeleteLogTargetBackend(ctx, p, idx, params)
			},
			func(p string, idx int) (*http.Response, error) {
				params := &v30.DeleteLogTargetBackendParams{TransactionId: &txID}
				return clientset.V30().DeleteLogTargetBackend(ctx, p, idx, params)
			},
			func(p string, idx int) (*http.Response, error) {
				params := &v32ee.DeleteLogTargetBackendParams{TransactionId: &txID}
				return clientset.V32EE().DeleteLogTargetBackend(ctx, p, idx, params)
			},
			func(p string, idx int) (*http.Response, error) {
				params := &v31ee.DeleteLogTargetBackendParams{TransactionId: &txID}
				return clientset.V31EE().DeleteLogTargetBackend(ctx, p, idx, params)
			},
			func(p string, idx int) (*http.Response, error) {
				params := &v30ee.DeleteLogTargetBackendParams{TransactionId: &txID}
				return clientset.V30EE().DeleteLogTargetBackend(ctx, p, idx, params)
			},
		)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		return client.CheckResponse(resp, "log target deletion from backend")
	}
}

// =============================================================================
// TCP Request Rule Executors (Frontend)
// =============================================================================

// TCPRequestRuleFrontendCreate returns an executor for creating TCP request rules in frontends.
func TCPRequestRuleFrontendCreate() func(ctx context.Context, c *client.DataplaneClient, txID string, parent string, index int, model *models.TCPRequestRule) error {
	return func(ctx context.Context, c *client.DataplaneClient, txID string, parent string, index int, model *models.TCPRequestRule) error {
		clientset := c.Clientset()

		resp, err := client.DispatchCreateChild(ctx, c, parent, index, model,
			func(p string, idx int, m v32.TcpRequestRule) (*http.Response, error) {
				params := &v32.CreateTCPRequestRuleFrontendParams{TransactionId: &txID}
				return clientset.V32().CreateTCPRequestRuleFrontend(ctx, p, idx, params, m)
			},
			func(p string, idx int, m v31.TcpRequestRule) (*http.Response, error) {
				params := &v31.CreateTCPRequestRuleFrontendParams{TransactionId: &txID}
				return clientset.V31().CreateTCPRequestRuleFrontend(ctx, p, idx, params, m)
			},
			func(p string, idx int, m v30.TcpRequestRule) (*http.Response, error) {
				params := &v30.CreateTCPRequestRuleFrontendParams{TransactionId: &txID}
				return clientset.V30().CreateTCPRequestRuleFrontend(ctx, p, idx, params, m)
			},
			func(p string, idx int, m v32ee.TcpRequestRule) (*http.Response, error) {
				params := &v32ee.CreateTCPRequestRuleFrontendParams{TransactionId: &txID}
				return clientset.V32EE().CreateTCPRequestRuleFrontend(ctx, p, idx, params, m)
			},
			func(p string, idx int, m v31ee.TcpRequestRule) (*http.Response, error) {
				params := &v31ee.CreateTCPRequestRuleFrontendParams{TransactionId: &txID}
				return clientset.V31EE().CreateTCPRequestRuleFrontend(ctx, p, idx, params, m)
			},
			func(p string, idx int, m v30ee.TcpRequestRule) (*http.Response, error) {
				params := &v30ee.CreateTCPRequestRuleFrontendParams{TransactionId: &txID}
				return clientset.V30EE().CreateTCPRequestRuleFrontend(ctx, p, idx, params, m)
			},
		)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		return client.CheckResponse(resp, "TCP request rule creation in frontend")
	}
}

// TCPRequestRuleFrontendUpdate returns an executor for updating TCP request rules in frontends.
func TCPRequestRuleFrontendUpdate() func(ctx context.Context, c *client.DataplaneClient, txID string, parent string, index int, model *models.TCPRequestRule) error {
	return func(ctx context.Context, c *client.DataplaneClient, txID string, parent string, index int, model *models.TCPRequestRule) error {
		clientset := c.Clientset()

		resp, err := client.DispatchReplaceChild(ctx, c, parent, index, model,
			func(p string, idx int, m v32.TcpRequestRule) (*http.Response, error) {
				params := &v32.ReplaceTCPRequestRuleFrontendParams{TransactionId: &txID}
				return clientset.V32().ReplaceTCPRequestRuleFrontend(ctx, p, idx, params, m)
			},
			func(p string, idx int, m v31.TcpRequestRule) (*http.Response, error) {
				params := &v31.ReplaceTCPRequestRuleFrontendParams{TransactionId: &txID}
				return clientset.V31().ReplaceTCPRequestRuleFrontend(ctx, p, idx, params, m)
			},
			func(p string, idx int, m v30.TcpRequestRule) (*http.Response, error) {
				params := &v30.ReplaceTCPRequestRuleFrontendParams{TransactionId: &txID}
				return clientset.V30().ReplaceTCPRequestRuleFrontend(ctx, p, idx, params, m)
			},
			func(p string, idx int, m v32ee.TcpRequestRule) (*http.Response, error) {
				params := &v32ee.ReplaceTCPRequestRuleFrontendParams{TransactionId: &txID}
				return clientset.V32EE().ReplaceTCPRequestRuleFrontend(ctx, p, idx, params, m)
			},
			func(p string, idx int, m v31ee.TcpRequestRule) (*http.Response, error) {
				params := &v31ee.ReplaceTCPRequestRuleFrontendParams{TransactionId: &txID}
				return clientset.V31EE().ReplaceTCPRequestRuleFrontend(ctx, p, idx, params, m)
			},
			func(p string, idx int, m v30ee.TcpRequestRule) (*http.Response, error) {
				params := &v30ee.ReplaceTCPRequestRuleFrontendParams{TransactionId: &txID}
				return clientset.V30EE().ReplaceTCPRequestRuleFrontend(ctx, p, idx, params, m)
			},
		)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		return client.CheckResponse(resp, "TCP request rule update in frontend")
	}
}

// TCPRequestRuleFrontendDelete returns an executor for deleting TCP request rules from frontends.
func TCPRequestRuleFrontendDelete() func(ctx context.Context, c *client.DataplaneClient, txID string, parent string, index int, _ *models.TCPRequestRule) error {
	return func(ctx context.Context, c *client.DataplaneClient, txID string, parent string, index int, _ *models.TCPRequestRule) error {
		clientset := c.Clientset()

		resp, err := client.DispatchDeleteChild(ctx, c, parent, index,
			func(p string, idx int) (*http.Response, error) {
				params := &v32.DeleteTCPRequestRuleFrontendParams{TransactionId: &txID}
				return clientset.V32().DeleteTCPRequestRuleFrontend(ctx, p, idx, params)
			},
			func(p string, idx int) (*http.Response, error) {
				params := &v31.DeleteTCPRequestRuleFrontendParams{TransactionId: &txID}
				return clientset.V31().DeleteTCPRequestRuleFrontend(ctx, p, idx, params)
			},
			func(p string, idx int) (*http.Response, error) {
				params := &v30.DeleteTCPRequestRuleFrontendParams{TransactionId: &txID}
				return clientset.V30().DeleteTCPRequestRuleFrontend(ctx, p, idx, params)
			},
			func(p string, idx int) (*http.Response, error) {
				params := &v32ee.DeleteTCPRequestRuleFrontendParams{TransactionId: &txID}
				return clientset.V32EE().DeleteTCPRequestRuleFrontend(ctx, p, idx, params)
			},
			func(p string, idx int) (*http.Response, error) {
				params := &v31ee.DeleteTCPRequestRuleFrontendParams{TransactionId: &txID}
				return clientset.V31EE().DeleteTCPRequestRuleFrontend(ctx, p, idx, params)
			},
			func(p string, idx int) (*http.Response, error) {
				params := &v30ee.DeleteTCPRequestRuleFrontendParams{TransactionId: &txID}
				return clientset.V30EE().DeleteTCPRequestRuleFrontend(ctx, p, idx, params)
			},
		)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		return client.CheckResponse(resp, "TCP request rule deletion from frontend")
	}
}

// =============================================================================
// TCP Request Rule Executors (Backend)
// =============================================================================

// TCPRequestRuleBackendCreate returns an executor for creating TCP request rules in backends.
func TCPRequestRuleBackendCreate() func(ctx context.Context, c *client.DataplaneClient, txID string, parent string, index int, model *models.TCPRequestRule) error {
	return func(ctx context.Context, c *client.DataplaneClient, txID string, parent string, index int, model *models.TCPRequestRule) error {
		clientset := c.Clientset()

		resp, err := client.DispatchCreateChild(ctx, c, parent, index, model,
			func(p string, idx int, m v32.TcpRequestRule) (*http.Response, error) {
				params := &v32.CreateTCPRequestRuleBackendParams{TransactionId: &txID}
				return clientset.V32().CreateTCPRequestRuleBackend(ctx, p, idx, params, m)
			},
			func(p string, idx int, m v31.TcpRequestRule) (*http.Response, error) {
				params := &v31.CreateTCPRequestRuleBackendParams{TransactionId: &txID}
				return clientset.V31().CreateTCPRequestRuleBackend(ctx, p, idx, params, m)
			},
			func(p string, idx int, m v30.TcpRequestRule) (*http.Response, error) {
				params := &v30.CreateTCPRequestRuleBackendParams{TransactionId: &txID}
				return clientset.V30().CreateTCPRequestRuleBackend(ctx, p, idx, params, m)
			},
			func(p string, idx int, m v32ee.TcpRequestRule) (*http.Response, error) {
				params := &v32ee.CreateTCPRequestRuleBackendParams{TransactionId: &txID}
				return clientset.V32EE().CreateTCPRequestRuleBackend(ctx, p, idx, params, m)
			},
			func(p string, idx int, m v31ee.TcpRequestRule) (*http.Response, error) {
				params := &v31ee.CreateTCPRequestRuleBackendParams{TransactionId: &txID}
				return clientset.V31EE().CreateTCPRequestRuleBackend(ctx, p, idx, params, m)
			},
			func(p string, idx int, m v30ee.TcpRequestRule) (*http.Response, error) {
				params := &v30ee.CreateTCPRequestRuleBackendParams{TransactionId: &txID}
				return clientset.V30EE().CreateTCPRequestRuleBackend(ctx, p, idx, params, m)
			},
		)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		return client.CheckResponse(resp, "TCP request rule creation in backend")
	}
}

// TCPRequestRuleBackendUpdate returns an executor for updating TCP request rules in backends.
func TCPRequestRuleBackendUpdate() func(ctx context.Context, c *client.DataplaneClient, txID string, parent string, index int, model *models.TCPRequestRule) error {
	return func(ctx context.Context, c *client.DataplaneClient, txID string, parent string, index int, model *models.TCPRequestRule) error {
		clientset := c.Clientset()

		resp, err := client.DispatchReplaceChild(ctx, c, parent, index, model,
			func(p string, idx int, m v32.TcpRequestRule) (*http.Response, error) {
				params := &v32.ReplaceTCPRequestRuleBackendParams{TransactionId: &txID}
				return clientset.V32().ReplaceTCPRequestRuleBackend(ctx, p, idx, params, m)
			},
			func(p string, idx int, m v31.TcpRequestRule) (*http.Response, error) {
				params := &v31.ReplaceTCPRequestRuleBackendParams{TransactionId: &txID}
				return clientset.V31().ReplaceTCPRequestRuleBackend(ctx, p, idx, params, m)
			},
			func(p string, idx int, m v30.TcpRequestRule) (*http.Response, error) {
				params := &v30.ReplaceTCPRequestRuleBackendParams{TransactionId: &txID}
				return clientset.V30().ReplaceTCPRequestRuleBackend(ctx, p, idx, params, m)
			},
			func(p string, idx int, m v32ee.TcpRequestRule) (*http.Response, error) {
				params := &v32ee.ReplaceTCPRequestRuleBackendParams{TransactionId: &txID}
				return clientset.V32EE().ReplaceTCPRequestRuleBackend(ctx, p, idx, params, m)
			},
			func(p string, idx int, m v31ee.TcpRequestRule) (*http.Response, error) {
				params := &v31ee.ReplaceTCPRequestRuleBackendParams{TransactionId: &txID}
				return clientset.V31EE().ReplaceTCPRequestRuleBackend(ctx, p, idx, params, m)
			},
			func(p string, idx int, m v30ee.TcpRequestRule) (*http.Response, error) {
				params := &v30ee.ReplaceTCPRequestRuleBackendParams{TransactionId: &txID}
				return clientset.V30EE().ReplaceTCPRequestRuleBackend(ctx, p, idx, params, m)
			},
		)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		return client.CheckResponse(resp, "TCP request rule update in backend")
	}
}

// TCPRequestRuleBackendDelete returns an executor for deleting TCP request rules from backends.
func TCPRequestRuleBackendDelete() func(ctx context.Context, c *client.DataplaneClient, txID string, parent string, index int, _ *models.TCPRequestRule) error {
	return func(ctx context.Context, c *client.DataplaneClient, txID string, parent string, index int, _ *models.TCPRequestRule) error {
		clientset := c.Clientset()

		resp, err := client.DispatchDeleteChild(ctx, c, parent, index,
			func(p string, idx int) (*http.Response, error) {
				params := &v32.DeleteTCPRequestRuleBackendParams{TransactionId: &txID}
				return clientset.V32().DeleteTCPRequestRuleBackend(ctx, p, idx, params)
			},
			func(p string, idx int) (*http.Response, error) {
				params := &v31.DeleteTCPRequestRuleBackendParams{TransactionId: &txID}
				return clientset.V31().DeleteTCPRequestRuleBackend(ctx, p, idx, params)
			},
			func(p string, idx int) (*http.Response, error) {
				params := &v30.DeleteTCPRequestRuleBackendParams{TransactionId: &txID}
				return clientset.V30().DeleteTCPRequestRuleBackend(ctx, p, idx, params)
			},
			func(p string, idx int) (*http.Response, error) {
				params := &v32ee.DeleteTCPRequestRuleBackendParams{TransactionId: &txID}
				return clientset.V32EE().DeleteTCPRequestRuleBackend(ctx, p, idx, params)
			},
			func(p string, idx int) (*http.Response, error) {
				params := &v31ee.DeleteTCPRequestRuleBackendParams{TransactionId: &txID}
				return clientset.V31EE().DeleteTCPRequestRuleBackend(ctx, p, idx, params)
			},
			func(p string, idx int) (*http.Response, error) {
				params := &v30ee.DeleteTCPRequestRuleBackendParams{TransactionId: &txID}
				return clientset.V30EE().DeleteTCPRequestRuleBackend(ctx, p, idx, params)
			},
		)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		return client.CheckResponse(resp, "TCP request rule deletion from backend")
	}
}

// =============================================================================
// TCP Response Rule Executors (Backend only)
// =============================================================================

// TCPResponseRuleBackendCreate returns an executor for creating TCP response rules in backends.
func TCPResponseRuleBackendCreate() func(ctx context.Context, c *client.DataplaneClient, txID string, parent string, index int, model *models.TCPResponseRule) error {
	return func(ctx context.Context, c *client.DataplaneClient, txID string, parent string, index int, model *models.TCPResponseRule) error {
		clientset := c.Clientset()

		resp, err := client.DispatchCreateChild(ctx, c, parent, index, model,
			func(p string, idx int, m v32.TcpResponseRule) (*http.Response, error) {
				params := &v32.CreateTCPResponseRuleBackendParams{TransactionId: &txID}
				return clientset.V32().CreateTCPResponseRuleBackend(ctx, p, idx, params, m)
			},
			func(p string, idx int, m v31.TcpResponseRule) (*http.Response, error) {
				params := &v31.CreateTCPResponseRuleBackendParams{TransactionId: &txID}
				return clientset.V31().CreateTCPResponseRuleBackend(ctx, p, idx, params, m)
			},
			func(p string, idx int, m v30.TcpResponseRule) (*http.Response, error) {
				params := &v30.CreateTCPResponseRuleBackendParams{TransactionId: &txID}
				return clientset.V30().CreateTCPResponseRuleBackend(ctx, p, idx, params, m)
			},
			func(p string, idx int, m v32ee.TcpResponseRule) (*http.Response, error) {
				params := &v32ee.CreateTCPResponseRuleBackendParams{TransactionId: &txID}
				return clientset.V32EE().CreateTCPResponseRuleBackend(ctx, p, idx, params, m)
			},
			func(p string, idx int, m v31ee.TcpResponseRule) (*http.Response, error) {
				params := &v31ee.CreateTCPResponseRuleBackendParams{TransactionId: &txID}
				return clientset.V31EE().CreateTCPResponseRuleBackend(ctx, p, idx, params, m)
			},
			func(p string, idx int, m v30ee.TcpResponseRule) (*http.Response, error) {
				params := &v30ee.CreateTCPResponseRuleBackendParams{TransactionId: &txID}
				return clientset.V30EE().CreateTCPResponseRuleBackend(ctx, p, idx, params, m)
			},
		)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		return client.CheckResponse(resp, "TCP response rule creation in backend")
	}
}

// TCPResponseRuleBackendUpdate returns an executor for updating TCP response rules in backends.
func TCPResponseRuleBackendUpdate() func(ctx context.Context, c *client.DataplaneClient, txID string, parent string, index int, model *models.TCPResponseRule) error {
	return func(ctx context.Context, c *client.DataplaneClient, txID string, parent string, index int, model *models.TCPResponseRule) error {
		clientset := c.Clientset()

		resp, err := client.DispatchReplaceChild(ctx, c, parent, index, model,
			func(p string, idx int, m v32.TcpResponseRule) (*http.Response, error) {
				params := &v32.ReplaceTCPResponseRuleBackendParams{TransactionId: &txID}
				return clientset.V32().ReplaceTCPResponseRuleBackend(ctx, p, idx, params, m)
			},
			func(p string, idx int, m v31.TcpResponseRule) (*http.Response, error) {
				params := &v31.ReplaceTCPResponseRuleBackendParams{TransactionId: &txID}
				return clientset.V31().ReplaceTCPResponseRuleBackend(ctx, p, idx, params, m)
			},
			func(p string, idx int, m v30.TcpResponseRule) (*http.Response, error) {
				params := &v30.ReplaceTCPResponseRuleBackendParams{TransactionId: &txID}
				return clientset.V30().ReplaceTCPResponseRuleBackend(ctx, p, idx, params, m)
			},
			func(p string, idx int, m v32ee.TcpResponseRule) (*http.Response, error) {
				params := &v32ee.ReplaceTCPResponseRuleBackendParams{TransactionId: &txID}
				return clientset.V32EE().ReplaceTCPResponseRuleBackend(ctx, p, idx, params, m)
			},
			func(p string, idx int, m v31ee.TcpResponseRule) (*http.Response, error) {
				params := &v31ee.ReplaceTCPResponseRuleBackendParams{TransactionId: &txID}
				return clientset.V31EE().ReplaceTCPResponseRuleBackend(ctx, p, idx, params, m)
			},
			func(p string, idx int, m v30ee.TcpResponseRule) (*http.Response, error) {
				params := &v30ee.ReplaceTCPResponseRuleBackendParams{TransactionId: &txID}
				return clientset.V30EE().ReplaceTCPResponseRuleBackend(ctx, p, idx, params, m)
			},
		)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		return client.CheckResponse(resp, "TCP response rule update in backend")
	}
}

// TCPResponseRuleBackendDelete returns an executor for deleting TCP response rules from backends.
func TCPResponseRuleBackendDelete() func(ctx context.Context, c *client.DataplaneClient, txID string, parent string, index int, _ *models.TCPResponseRule) error {
	return func(ctx context.Context, c *client.DataplaneClient, txID string, parent string, index int, _ *models.TCPResponseRule) error {
		clientset := c.Clientset()

		resp, err := client.DispatchDeleteChild(ctx, c, parent, index,
			func(p string, idx int) (*http.Response, error) {
				params := &v32.DeleteTCPResponseRuleBackendParams{TransactionId: &txID}
				return clientset.V32().DeleteTCPResponseRuleBackend(ctx, p, idx, params)
			},
			func(p string, idx int) (*http.Response, error) {
				params := &v31.DeleteTCPResponseRuleBackendParams{TransactionId: &txID}
				return clientset.V31().DeleteTCPResponseRuleBackend(ctx, p, idx, params)
			},
			func(p string, idx int) (*http.Response, error) {
				params := &v30.DeleteTCPResponseRuleBackendParams{TransactionId: &txID}
				return clientset.V30().DeleteTCPResponseRuleBackend(ctx, p, idx, params)
			},
			func(p string, idx int) (*http.Response, error) {
				params := &v32ee.DeleteTCPResponseRuleBackendParams{TransactionId: &txID}
				return clientset.V32EE().DeleteTCPResponseRuleBackend(ctx, p, idx, params)
			},
			func(p string, idx int) (*http.Response, error) {
				params := &v31ee.DeleteTCPResponseRuleBackendParams{TransactionId: &txID}
				return clientset.V31EE().DeleteTCPResponseRuleBackend(ctx, p, idx, params)
			},
			func(p string, idx int) (*http.Response, error) {
				params := &v30ee.DeleteTCPResponseRuleBackendParams{TransactionId: &txID}
				return clientset.V30EE().DeleteTCPResponseRuleBackend(ctx, p, idx, params)
			},
		)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		return client.CheckResponse(resp, "TCP response rule deletion from backend")
	}
}

// =============================================================================
// Stick Rule Executors (Backend only)
// =============================================================================

// StickRuleBackendCreate returns an executor for creating stick rules in backends.
func StickRuleBackendCreate() func(ctx context.Context, c *client.DataplaneClient, txID string, parent string, index int, model *models.StickRule) error {
	return func(ctx context.Context, c *client.DataplaneClient, txID string, parent string, index int, model *models.StickRule) error {
		clientset := c.Clientset()

		resp, err := client.DispatchCreateChild(ctx, c, parent, index, model,
			func(p string, idx int, m v32.StickRule) (*http.Response, error) {
				params := &v32.CreateStickRuleParams{TransactionId: &txID}
				return clientset.V32().CreateStickRule(ctx, p, idx, params, m)
			},
			func(p string, idx int, m v31.StickRule) (*http.Response, error) {
				params := &v31.CreateStickRuleParams{TransactionId: &txID}
				return clientset.V31().CreateStickRule(ctx, p, idx, params, m)
			},
			func(p string, idx int, m v30.StickRule) (*http.Response, error) {
				params := &v30.CreateStickRuleParams{TransactionId: &txID}
				return clientset.V30().CreateStickRule(ctx, p, idx, params, m)
			},
			func(p string, idx int, m v32ee.StickRule) (*http.Response, error) {
				params := &v32ee.CreateStickRuleParams{TransactionId: &txID}
				return clientset.V32EE().CreateStickRule(ctx, p, idx, params, m)
			},
			func(p string, idx int, m v31ee.StickRule) (*http.Response, error) {
				params := &v31ee.CreateStickRuleParams{TransactionId: &txID}
				return clientset.V31EE().CreateStickRule(ctx, p, idx, params, m)
			},
			func(p string, idx int, m v30ee.StickRule) (*http.Response, error) {
				params := &v30ee.CreateStickRuleParams{TransactionId: &txID}
				return clientset.V30EE().CreateStickRule(ctx, p, idx, params, m)
			},
		)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		return client.CheckResponse(resp, "stick rule creation in backend")
	}
}

// StickRuleBackendUpdate returns an executor for updating stick rules in backends.
func StickRuleBackendUpdate() func(ctx context.Context, c *client.DataplaneClient, txID string, parent string, index int, model *models.StickRule) error {
	return func(ctx context.Context, c *client.DataplaneClient, txID string, parent string, index int, model *models.StickRule) error {
		clientset := c.Clientset()

		resp, err := client.DispatchReplaceChild(ctx, c, parent, index, model,
			func(p string, idx int, m v32.StickRule) (*http.Response, error) {
				params := &v32.ReplaceStickRuleParams{TransactionId: &txID}
				return clientset.V32().ReplaceStickRule(ctx, p, idx, params, m)
			},
			func(p string, idx int, m v31.StickRule) (*http.Response, error) {
				params := &v31.ReplaceStickRuleParams{TransactionId: &txID}
				return clientset.V31().ReplaceStickRule(ctx, p, idx, params, m)
			},
			func(p string, idx int, m v30.StickRule) (*http.Response, error) {
				params := &v30.ReplaceStickRuleParams{TransactionId: &txID}
				return clientset.V30().ReplaceStickRule(ctx, p, idx, params, m)
			},
			func(p string, idx int, m v32ee.StickRule) (*http.Response, error) {
				params := &v32ee.ReplaceStickRuleParams{TransactionId: &txID}
				return clientset.V32EE().ReplaceStickRule(ctx, p, idx, params, m)
			},
			func(p string, idx int, m v31ee.StickRule) (*http.Response, error) {
				params := &v31ee.ReplaceStickRuleParams{TransactionId: &txID}
				return clientset.V31EE().ReplaceStickRule(ctx, p, idx, params, m)
			},
			func(p string, idx int, m v30ee.StickRule) (*http.Response, error) {
				params := &v30ee.ReplaceStickRuleParams{TransactionId: &txID}
				return clientset.V30EE().ReplaceStickRule(ctx, p, idx, params, m)
			},
		)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		return client.CheckResponse(resp, "stick rule update in backend")
	}
}

// StickRuleBackendDelete returns an executor for deleting stick rules from backends.
func StickRuleBackendDelete() func(ctx context.Context, c *client.DataplaneClient, txID string, parent string, index int, _ *models.StickRule) error {
	return func(ctx context.Context, c *client.DataplaneClient, txID string, parent string, index int, _ *models.StickRule) error {
		clientset := c.Clientset()

		resp, err := client.DispatchDeleteChild(ctx, c, parent, index,
			func(p string, idx int) (*http.Response, error) {
				params := &v32.DeleteStickRuleParams{TransactionId: &txID}
				return clientset.V32().DeleteStickRule(ctx, p, idx, params)
			},
			func(p string, idx int) (*http.Response, error) {
				params := &v31.DeleteStickRuleParams{TransactionId: &txID}
				return clientset.V31().DeleteStickRule(ctx, p, idx, params)
			},
			func(p string, idx int) (*http.Response, error) {
				params := &v30.DeleteStickRuleParams{TransactionId: &txID}
				return clientset.V30().DeleteStickRule(ctx, p, idx, params)
			},
			func(p string, idx int) (*http.Response, error) {
				params := &v32ee.DeleteStickRuleParams{TransactionId: &txID}
				return clientset.V32EE().DeleteStickRule(ctx, p, idx, params)
			},
			func(p string, idx int) (*http.Response, error) {
				params := &v31ee.DeleteStickRuleParams{TransactionId: &txID}
				return clientset.V31EE().DeleteStickRule(ctx, p, idx, params)
			},
			func(p string, idx int) (*http.Response, error) {
				params := &v30ee.DeleteStickRuleParams{TransactionId: &txID}
				return clientset.V30EE().DeleteStickRule(ctx, p, idx, params)
			},
		)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		return client.CheckResponse(resp, "stick rule deletion from backend")
	}
}

// =============================================================================
// HTTP After Response Rule Executors (Backend only)
// =============================================================================

// HTTPAfterResponseRuleBackendCreate returns an executor for creating HTTP after response rules in backends.
func HTTPAfterResponseRuleBackendCreate() func(ctx context.Context, c *client.DataplaneClient, txID string, parent string, index int, model *models.HTTPAfterResponseRule) error {
	return func(ctx context.Context, c *client.DataplaneClient, txID string, parent string, index int, model *models.HTTPAfterResponseRule) error {
		clientset := c.Clientset()

		resp, err := client.DispatchCreateChild(ctx, c, parent, index, model,
			func(p string, idx int, m v32.HttpAfterResponseRule) (*http.Response, error) {
				params := &v32.CreateHTTPAfterResponseRuleBackendParams{TransactionId: &txID}
				return clientset.V32().CreateHTTPAfterResponseRuleBackend(ctx, p, idx, params, m)
			},
			func(p string, idx int, m v31.HttpAfterResponseRule) (*http.Response, error) {
				params := &v31.CreateHTTPAfterResponseRuleBackendParams{TransactionId: &txID}
				return clientset.V31().CreateHTTPAfterResponseRuleBackend(ctx, p, idx, params, m)
			},
			func(p string, idx int, m v30.HttpAfterResponseRule) (*http.Response, error) {
				params := &v30.CreateHTTPAfterResponseRuleBackendParams{TransactionId: &txID}
				return clientset.V30().CreateHTTPAfterResponseRuleBackend(ctx, p, idx, params, m)
			},
			func(p string, idx int, m v32ee.HttpAfterResponseRule) (*http.Response, error) {
				params := &v32ee.CreateHTTPAfterResponseRuleBackendParams{TransactionId: &txID}
				return clientset.V32EE().CreateHTTPAfterResponseRuleBackend(ctx, p, idx, params, m)
			},
			func(p string, idx int, m v31ee.HttpAfterResponseRule) (*http.Response, error) {
				params := &v31ee.CreateHTTPAfterResponseRuleBackendParams{TransactionId: &txID}
				return clientset.V31EE().CreateHTTPAfterResponseRuleBackend(ctx, p, idx, params, m)
			},
			func(p string, idx int, m v30ee.HttpAfterResponseRule) (*http.Response, error) {
				params := &v30ee.CreateHTTPAfterResponseRuleBackendParams{TransactionId: &txID}
				return clientset.V30EE().CreateHTTPAfterResponseRuleBackend(ctx, p, idx, params, m)
			},
		)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		return client.CheckResponse(resp, "HTTP after response rule creation in backend")
	}
}

// HTTPAfterResponseRuleBackendUpdate returns an executor for updating HTTP after response rules in backends.
func HTTPAfterResponseRuleBackendUpdate() func(ctx context.Context, c *client.DataplaneClient, txID string, parent string, index int, model *models.HTTPAfterResponseRule) error {
	return func(ctx context.Context, c *client.DataplaneClient, txID string, parent string, index int, model *models.HTTPAfterResponseRule) error {
		clientset := c.Clientset()

		resp, err := client.DispatchReplaceChild(ctx, c, parent, index, model,
			func(p string, idx int, m v32.HttpAfterResponseRule) (*http.Response, error) {
				params := &v32.ReplaceHTTPAfterResponseRuleBackendParams{TransactionId: &txID}
				return clientset.V32().ReplaceHTTPAfterResponseRuleBackend(ctx, p, idx, params, m)
			},
			func(p string, idx int, m v31.HttpAfterResponseRule) (*http.Response, error) {
				params := &v31.ReplaceHTTPAfterResponseRuleBackendParams{TransactionId: &txID}
				return clientset.V31().ReplaceHTTPAfterResponseRuleBackend(ctx, p, idx, params, m)
			},
			func(p string, idx int, m v30.HttpAfterResponseRule) (*http.Response, error) {
				params := &v30.ReplaceHTTPAfterResponseRuleBackendParams{TransactionId: &txID}
				return clientset.V30().ReplaceHTTPAfterResponseRuleBackend(ctx, p, idx, params, m)
			},
			func(p string, idx int, m v32ee.HttpAfterResponseRule) (*http.Response, error) {
				params := &v32ee.ReplaceHTTPAfterResponseRuleBackendParams{TransactionId: &txID}
				return clientset.V32EE().ReplaceHTTPAfterResponseRuleBackend(ctx, p, idx, params, m)
			},
			func(p string, idx int, m v31ee.HttpAfterResponseRule) (*http.Response, error) {
				params := &v31ee.ReplaceHTTPAfterResponseRuleBackendParams{TransactionId: &txID}
				return clientset.V31EE().ReplaceHTTPAfterResponseRuleBackend(ctx, p, idx, params, m)
			},
			func(p string, idx int, m v30ee.HttpAfterResponseRule) (*http.Response, error) {
				params := &v30ee.ReplaceHTTPAfterResponseRuleBackendParams{TransactionId: &txID}
				return clientset.V30EE().ReplaceHTTPAfterResponseRuleBackend(ctx, p, idx, params, m)
			},
		)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		return client.CheckResponse(resp, "HTTP after response rule update in backend")
	}
}

// HTTPAfterResponseRuleBackendDelete returns an executor for deleting HTTP after response rules from backends.
func HTTPAfterResponseRuleBackendDelete() func(ctx context.Context, c *client.DataplaneClient, txID string, parent string, index int, _ *models.HTTPAfterResponseRule) error {
	return func(ctx context.Context, c *client.DataplaneClient, txID string, parent string, index int, _ *models.HTTPAfterResponseRule) error {
		clientset := c.Clientset()

		resp, err := client.DispatchDeleteChild(ctx, c, parent, index,
			func(p string, idx int) (*http.Response, error) {
				params := &v32.DeleteHTTPAfterResponseRuleBackendParams{TransactionId: &txID}
				return clientset.V32().DeleteHTTPAfterResponseRuleBackend(ctx, p, idx, params)
			},
			func(p string, idx int) (*http.Response, error) {
				params := &v31.DeleteHTTPAfterResponseRuleBackendParams{TransactionId: &txID}
				return clientset.V31().DeleteHTTPAfterResponseRuleBackend(ctx, p, idx, params)
			},
			func(p string, idx int) (*http.Response, error) {
				params := &v30.DeleteHTTPAfterResponseRuleBackendParams{TransactionId: &txID}
				return clientset.V30().DeleteHTTPAfterResponseRuleBackend(ctx, p, idx, params)
			},
			func(p string, idx int) (*http.Response, error) {
				params := &v32ee.DeleteHTTPAfterResponseRuleBackendParams{TransactionId: &txID}
				return clientset.V32EE().DeleteHTTPAfterResponseRuleBackend(ctx, p, idx, params)
			},
			func(p string, idx int) (*http.Response, error) {
				params := &v31ee.DeleteHTTPAfterResponseRuleBackendParams{TransactionId: &txID}
				return clientset.V31EE().DeleteHTTPAfterResponseRuleBackend(ctx, p, idx, params)
			},
			func(p string, idx int) (*http.Response, error) {
				params := &v30ee.DeleteHTTPAfterResponseRuleBackendParams{TransactionId: &txID}
				return clientset.V30EE().DeleteHTTPAfterResponseRuleBackend(ctx, p, idx, params)
			},
		)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		return client.CheckResponse(resp, "HTTP after response rule deletion from backend")
	}
}

// =============================================================================
// Server Switching Rule Executors (Backend only)
// =============================================================================

// ServerSwitchingRuleBackendCreate returns an executor for creating server switching rules in backends.
func ServerSwitchingRuleBackendCreate() func(ctx context.Context, c *client.DataplaneClient, txID string, parent string, index int, model *models.ServerSwitchingRule) error {
	return func(ctx context.Context, c *client.DataplaneClient, txID string, parent string, index int, model *models.ServerSwitchingRule) error {
		clientset := c.Clientset()

		resp, err := client.DispatchCreateChild(ctx, c, parent, index, model,
			func(p string, idx int, m v32.ServerSwitchingRule) (*http.Response, error) {
				params := &v32.CreateServerSwitchingRuleParams{TransactionId: &txID}
				return clientset.V32().CreateServerSwitchingRule(ctx, p, idx, params, m)
			},
			func(p string, idx int, m v31.ServerSwitchingRule) (*http.Response, error) {
				params := &v31.CreateServerSwitchingRuleParams{TransactionId: &txID}
				return clientset.V31().CreateServerSwitchingRule(ctx, p, idx, params, m)
			},
			func(p string, idx int, m v30.ServerSwitchingRule) (*http.Response, error) {
				params := &v30.CreateServerSwitchingRuleParams{TransactionId: &txID}
				return clientset.V30().CreateServerSwitchingRule(ctx, p, idx, params, m)
			},
			func(p string, idx int, m v32ee.ServerSwitchingRule) (*http.Response, error) {
				params := &v32ee.CreateServerSwitchingRuleBackendParams{TransactionId: &txID}
				return clientset.V32EE().CreateServerSwitchingRuleBackend(ctx, p, idx, params, m)
			},
			func(p string, idx int, m v31ee.ServerSwitchingRule) (*http.Response, error) {
				params := &v31ee.CreateServerSwitchingRuleParams{TransactionId: &txID}
				return clientset.V31EE().CreateServerSwitchingRule(ctx, p, idx, params, m)
			},
			func(p string, idx int, m v30ee.ServerSwitchingRule) (*http.Response, error) {
				params := &v30ee.CreateServerSwitchingRuleParams{TransactionId: &txID}
				return clientset.V30EE().CreateServerSwitchingRule(ctx, p, idx, params, m)
			},
		)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		return client.CheckResponse(resp, "server switching rule creation in backend")
	}
}

// ServerSwitchingRuleBackendUpdate returns an executor for updating server switching rules in backends.
func ServerSwitchingRuleBackendUpdate() func(ctx context.Context, c *client.DataplaneClient, txID string, parent string, index int, model *models.ServerSwitchingRule) error {
	return func(ctx context.Context, c *client.DataplaneClient, txID string, parent string, index int, model *models.ServerSwitchingRule) error {
		clientset := c.Clientset()

		resp, err := client.DispatchReplaceChild(ctx, c, parent, index, model,
			func(p string, idx int, m v32.ServerSwitchingRule) (*http.Response, error) {
				params := &v32.ReplaceServerSwitchingRuleParams{TransactionId: &txID}
				return clientset.V32().ReplaceServerSwitchingRule(ctx, p, idx, params, m)
			},
			func(p string, idx int, m v31.ServerSwitchingRule) (*http.Response, error) {
				params := &v31.ReplaceServerSwitchingRuleParams{TransactionId: &txID}
				return clientset.V31().ReplaceServerSwitchingRule(ctx, p, idx, params, m)
			},
			func(p string, idx int, m v30.ServerSwitchingRule) (*http.Response, error) {
				params := &v30.ReplaceServerSwitchingRuleParams{TransactionId: &txID}
				return clientset.V30().ReplaceServerSwitchingRule(ctx, p, idx, params, m)
			},
			func(p string, idx int, m v32ee.ServerSwitchingRule) (*http.Response, error) {
				params := &v32ee.ReplaceServerSwitchingRuleBackendParams{TransactionId: &txID}
				return clientset.V32EE().ReplaceServerSwitchingRuleBackend(ctx, p, idx, params, m)
			},
			func(p string, idx int, m v31ee.ServerSwitchingRule) (*http.Response, error) {
				params := &v31ee.ReplaceServerSwitchingRuleParams{TransactionId: &txID}
				return clientset.V31EE().ReplaceServerSwitchingRule(ctx, p, idx, params, m)
			},
			func(p string, idx int, m v30ee.ServerSwitchingRule) (*http.Response, error) {
				params := &v30ee.ReplaceServerSwitchingRuleParams{TransactionId: &txID}
				return clientset.V30EE().ReplaceServerSwitchingRule(ctx, p, idx, params, m)
			},
		)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		return client.CheckResponse(resp, "server switching rule update in backend")
	}
}

// ServerSwitchingRuleBackendDelete returns an executor for deleting server switching rules from backends.
func ServerSwitchingRuleBackendDelete() func(ctx context.Context, c *client.DataplaneClient, txID string, parent string, index int, _ *models.ServerSwitchingRule) error {
	return func(ctx context.Context, c *client.DataplaneClient, txID string, parent string, index int, _ *models.ServerSwitchingRule) error {
		clientset := c.Clientset()

		resp, err := client.DispatchDeleteChild(ctx, c, parent, index,
			func(p string, idx int) (*http.Response, error) {
				params := &v32.DeleteServerSwitchingRuleParams{TransactionId: &txID}
				return clientset.V32().DeleteServerSwitchingRule(ctx, p, idx, params)
			},
			func(p string, idx int) (*http.Response, error) {
				params := &v31.DeleteServerSwitchingRuleParams{TransactionId: &txID}
				return clientset.V31().DeleteServerSwitchingRule(ctx, p, idx, params)
			},
			func(p string, idx int) (*http.Response, error) {
				params := &v30.DeleteServerSwitchingRuleParams{TransactionId: &txID}
				return clientset.V30().DeleteServerSwitchingRule(ctx, p, idx, params)
			},
			func(p string, idx int) (*http.Response, error) {
				params := &v32ee.DeleteServerSwitchingRuleBackendParams{TransactionId: &txID}
				return clientset.V32EE().DeleteServerSwitchingRuleBackend(ctx, p, idx, params)
			},
			func(p string, idx int) (*http.Response, error) {
				params := &v31ee.DeleteServerSwitchingRuleParams{TransactionId: &txID}
				return clientset.V31EE().DeleteServerSwitchingRule(ctx, p, idx, params)
			},
			func(p string, idx int) (*http.Response, error) {
				params := &v30ee.DeleteServerSwitchingRuleParams{TransactionId: &txID}
				return clientset.V30EE().DeleteServerSwitchingRule(ctx, p, idx, params)
			},
		)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		return client.CheckResponse(resp, "server switching rule deletion from backend")
	}
}

// =============================================================================
// HTTP Check Executors (Backend only)
// =============================================================================

// HTTPCheckBackendCreate returns an executor for creating HTTP checks in backends.
func HTTPCheckBackendCreate() func(ctx context.Context, c *client.DataplaneClient, txID string, parent string, index int, model *models.HTTPCheck) error {
	return func(ctx context.Context, c *client.DataplaneClient, txID string, parent string, index int, model *models.HTTPCheck) error {
		clientset := c.Clientset()

		resp, err := client.DispatchCreateChild(ctx, c, parent, index, model,
			func(p string, idx int, m v32.HttpCheck) (*http.Response, error) {
				params := &v32.CreateHTTPCheckBackendParams{TransactionId: &txID}
				return clientset.V32().CreateHTTPCheckBackend(ctx, p, idx, params, m)
			},
			func(p string, idx int, m v31.HttpCheck) (*http.Response, error) {
				params := &v31.CreateHTTPCheckBackendParams{TransactionId: &txID}
				return clientset.V31().CreateHTTPCheckBackend(ctx, p, idx, params, m)
			},
			func(p string, idx int, m v30.HttpCheck) (*http.Response, error) {
				params := &v30.CreateHTTPCheckBackendParams{TransactionId: &txID}
				return clientset.V30().CreateHTTPCheckBackend(ctx, p, idx, params, m)
			},
			func(p string, idx int, m v32ee.HttpCheck) (*http.Response, error) {
				params := &v32ee.CreateHTTPCheckBackendParams{TransactionId: &txID}
				return clientset.V32EE().CreateHTTPCheckBackend(ctx, p, idx, params, m)
			},
			func(p string, idx int, m v31ee.HttpCheck) (*http.Response, error) {
				params := &v31ee.CreateHTTPCheckBackendParams{TransactionId: &txID}
				return clientset.V31EE().CreateHTTPCheckBackend(ctx, p, idx, params, m)
			},
			func(p string, idx int, m v30ee.HttpCheck) (*http.Response, error) {
				params := &v30ee.CreateHTTPCheckBackendParams{TransactionId: &txID}
				return clientset.V30EE().CreateHTTPCheckBackend(ctx, p, idx, params, m)
			},
		)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		return client.CheckResponse(resp, "HTTP check creation in backend")
	}
}

// HTTPCheckBackendUpdate returns an executor for updating HTTP checks in backends.
func HTTPCheckBackendUpdate() func(ctx context.Context, c *client.DataplaneClient, txID string, parent string, index int, model *models.HTTPCheck) error {
	return func(ctx context.Context, c *client.DataplaneClient, txID string, parent string, index int, model *models.HTTPCheck) error {
		clientset := c.Clientset()

		resp, err := client.DispatchReplaceChild(ctx, c, parent, index, model,
			func(p string, idx int, m v32.HttpCheck) (*http.Response, error) {
				params := &v32.ReplaceHTTPCheckBackendParams{TransactionId: &txID}
				return clientset.V32().ReplaceHTTPCheckBackend(ctx, p, idx, params, m)
			},
			func(p string, idx int, m v31.HttpCheck) (*http.Response, error) {
				params := &v31.ReplaceHTTPCheckBackendParams{TransactionId: &txID}
				return clientset.V31().ReplaceHTTPCheckBackend(ctx, p, idx, params, m)
			},
			func(p string, idx int, m v30.HttpCheck) (*http.Response, error) {
				params := &v30.ReplaceHTTPCheckBackendParams{TransactionId: &txID}
				return clientset.V30().ReplaceHTTPCheckBackend(ctx, p, idx, params, m)
			},
			func(p string, idx int, m v32ee.HttpCheck) (*http.Response, error) {
				params := &v32ee.ReplaceHTTPCheckBackendParams{TransactionId: &txID}
				return clientset.V32EE().ReplaceHTTPCheckBackend(ctx, p, idx, params, m)
			},
			func(p string, idx int, m v31ee.HttpCheck) (*http.Response, error) {
				params := &v31ee.ReplaceHTTPCheckBackendParams{TransactionId: &txID}
				return clientset.V31EE().ReplaceHTTPCheckBackend(ctx, p, idx, params, m)
			},
			func(p string, idx int, m v30ee.HttpCheck) (*http.Response, error) {
				params := &v30ee.ReplaceHTTPCheckBackendParams{TransactionId: &txID}
				return clientset.V30EE().ReplaceHTTPCheckBackend(ctx, p, idx, params, m)
			},
		)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		return client.CheckResponse(resp, "HTTP check update in backend")
	}
}

// HTTPCheckBackendDelete returns an executor for deleting HTTP checks from backends.
func HTTPCheckBackendDelete() func(ctx context.Context, c *client.DataplaneClient, txID string, parent string, index int, _ *models.HTTPCheck) error {
	return func(ctx context.Context, c *client.DataplaneClient, txID string, parent string, index int, _ *models.HTTPCheck) error {
		clientset := c.Clientset()

		resp, err := client.DispatchDeleteChild(ctx, c, parent, index,
			func(p string, idx int) (*http.Response, error) {
				params := &v32.DeleteHTTPCheckBackendParams{TransactionId: &txID}
				return clientset.V32().DeleteHTTPCheckBackend(ctx, p, idx, params)
			},
			func(p string, idx int) (*http.Response, error) {
				params := &v31.DeleteHTTPCheckBackendParams{TransactionId: &txID}
				return clientset.V31().DeleteHTTPCheckBackend(ctx, p, idx, params)
			},
			func(p string, idx int) (*http.Response, error) {
				params := &v30.DeleteHTTPCheckBackendParams{TransactionId: &txID}
				return clientset.V30().DeleteHTTPCheckBackend(ctx, p, idx, params)
			},
			func(p string, idx int) (*http.Response, error) {
				params := &v32ee.DeleteHTTPCheckBackendParams{TransactionId: &txID}
				return clientset.V32EE().DeleteHTTPCheckBackend(ctx, p, idx, params)
			},
			func(p string, idx int) (*http.Response, error) {
				params := &v31ee.DeleteHTTPCheckBackendParams{TransactionId: &txID}
				return clientset.V31EE().DeleteHTTPCheckBackend(ctx, p, idx, params)
			},
			func(p string, idx int) (*http.Response, error) {
				params := &v30ee.DeleteHTTPCheckBackendParams{TransactionId: &txID}
				return clientset.V30EE().DeleteHTTPCheckBackend(ctx, p, idx, params)
			},
		)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		return client.CheckResponse(resp, "HTTP check deletion from backend")
	}
}

// =============================================================================
// TCP Check Executors (Backend only)
// =============================================================================

// TCPCheckBackendCreate returns an executor for creating TCP checks in backends.
func TCPCheckBackendCreate() func(ctx context.Context, c *client.DataplaneClient, txID string, parent string, index int, model *models.TCPCheck) error {
	return func(ctx context.Context, c *client.DataplaneClient, txID string, parent string, index int, model *models.TCPCheck) error {
		clientset := c.Clientset()

		resp, err := client.DispatchCreateChild(ctx, c, parent, index, model,
			func(p string, idx int, m v32.TcpCheck) (*http.Response, error) {
				params := &v32.CreateTCPCheckBackendParams{TransactionId: &txID}
				return clientset.V32().CreateTCPCheckBackend(ctx, p, idx, params, m)
			},
			func(p string, idx int, m v31.TcpCheck) (*http.Response, error) {
				params := &v31.CreateTCPCheckBackendParams{TransactionId: &txID}
				return clientset.V31().CreateTCPCheckBackend(ctx, p, idx, params, m)
			},
			func(p string, idx int, m v30.TcpCheck) (*http.Response, error) {
				params := &v30.CreateTCPCheckBackendParams{TransactionId: &txID}
				return clientset.V30().CreateTCPCheckBackend(ctx, p, idx, params, m)
			},
			func(p string, idx int, m v32ee.TcpCheck) (*http.Response, error) {
				params := &v32ee.CreateTCPCheckBackendParams{TransactionId: &txID}
				return clientset.V32EE().CreateTCPCheckBackend(ctx, p, idx, params, m)
			},
			func(p string, idx int, m v31ee.TcpCheck) (*http.Response, error) {
				params := &v31ee.CreateTCPCheckBackendParams{TransactionId: &txID}
				return clientset.V31EE().CreateTCPCheckBackend(ctx, p, idx, params, m)
			},
			func(p string, idx int, m v30ee.TcpCheck) (*http.Response, error) {
				params := &v30ee.CreateTCPCheckBackendParams{TransactionId: &txID}
				return clientset.V30EE().CreateTCPCheckBackend(ctx, p, idx, params, m)
			},
		)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		return client.CheckResponse(resp, "TCP check creation in backend")
	}
}

// TCPCheckBackendUpdate returns an executor for updating TCP checks in backends.
func TCPCheckBackendUpdate() func(ctx context.Context, c *client.DataplaneClient, txID string, parent string, index int, model *models.TCPCheck) error {
	return func(ctx context.Context, c *client.DataplaneClient, txID string, parent string, index int, model *models.TCPCheck) error {
		clientset := c.Clientset()

		resp, err := client.DispatchReplaceChild(ctx, c, parent, index, model,
			func(p string, idx int, m v32.TcpCheck) (*http.Response, error) {
				params := &v32.ReplaceTCPCheckBackendParams{TransactionId: &txID}
				return clientset.V32().ReplaceTCPCheckBackend(ctx, p, idx, params, m)
			},
			func(p string, idx int, m v31.TcpCheck) (*http.Response, error) {
				params := &v31.ReplaceTCPCheckBackendParams{TransactionId: &txID}
				return clientset.V31().ReplaceTCPCheckBackend(ctx, p, idx, params, m)
			},
			func(p string, idx int, m v30.TcpCheck) (*http.Response, error) {
				params := &v30.ReplaceTCPCheckBackendParams{TransactionId: &txID}
				return clientset.V30().ReplaceTCPCheckBackend(ctx, p, idx, params, m)
			},
			func(p string, idx int, m v32ee.TcpCheck) (*http.Response, error) {
				params := &v32ee.ReplaceTCPCheckBackendParams{TransactionId: &txID}
				return clientset.V32EE().ReplaceTCPCheckBackend(ctx, p, idx, params, m)
			},
			func(p string, idx int, m v31ee.TcpCheck) (*http.Response, error) {
				params := &v31ee.ReplaceTCPCheckBackendParams{TransactionId: &txID}
				return clientset.V31EE().ReplaceTCPCheckBackend(ctx, p, idx, params, m)
			},
			func(p string, idx int, m v30ee.TcpCheck) (*http.Response, error) {
				params := &v30ee.ReplaceTCPCheckBackendParams{TransactionId: &txID}
				return clientset.V30EE().ReplaceTCPCheckBackend(ctx, p, idx, params, m)
			},
		)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		return client.CheckResponse(resp, "TCP check update in backend")
	}
}

// TCPCheckBackendDelete returns an executor for deleting TCP checks from backends.
func TCPCheckBackendDelete() func(ctx context.Context, c *client.DataplaneClient, txID string, parent string, index int, _ *models.TCPCheck) error {
	return func(ctx context.Context, c *client.DataplaneClient, txID string, parent string, index int, _ *models.TCPCheck) error {
		clientset := c.Clientset()

		resp, err := client.DispatchDeleteChild(ctx, c, parent, index,
			func(p string, idx int) (*http.Response, error) {
				params := &v32.DeleteTCPCheckBackendParams{TransactionId: &txID}
				return clientset.V32().DeleteTCPCheckBackend(ctx, p, idx, params)
			},
			func(p string, idx int) (*http.Response, error) {
				params := &v31.DeleteTCPCheckBackendParams{TransactionId: &txID}
				return clientset.V31().DeleteTCPCheckBackend(ctx, p, idx, params)
			},
			func(p string, idx int) (*http.Response, error) {
				params := &v30.DeleteTCPCheckBackendParams{TransactionId: &txID}
				return clientset.V30().DeleteTCPCheckBackend(ctx, p, idx, params)
			},
			func(p string, idx int) (*http.Response, error) {
				params := &v32ee.DeleteTCPCheckBackendParams{TransactionId: &txID}
				return clientset.V32EE().DeleteTCPCheckBackend(ctx, p, idx, params)
			},
			func(p string, idx int) (*http.Response, error) {
				params := &v31ee.DeleteTCPCheckBackendParams{TransactionId: &txID}
				return clientset.V31EE().DeleteTCPCheckBackend(ctx, p, idx, params)
			},
			func(p string, idx int) (*http.Response, error) {
				params := &v30ee.DeleteTCPCheckBackendParams{TransactionId: &txID}
				return clientset.V30EE().DeleteTCPCheckBackend(ctx, p, idx, params)
			},
		)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		return client.CheckResponse(resp, "TCP check deletion from backend")
	}
}

// =============================================================================
// Declare Capture Executors (Frontend only)
// =============================================================================

// DeclareCaptureFrontendCreate returns an executor for creating declare captures in frontends.
func DeclareCaptureFrontendCreate() func(ctx context.Context, c *client.DataplaneClient, txID string, parent string, index int, model *models.Capture) error {
	return func(ctx context.Context, c *client.DataplaneClient, txID string, parent string, index int, model *models.Capture) error {
		clientset := c.Clientset()

		resp, err := client.DispatchCreateChild(ctx, c, parent, index, model,
			func(p string, idx int, m v32.Capture) (*http.Response, error) {
				params := &v32.CreateDeclareCaptureParams{TransactionId: &txID}
				return clientset.V32().CreateDeclareCapture(ctx, p, idx, params, m)
			},
			func(p string, idx int, m v31.Capture) (*http.Response, error) {
				params := &v31.CreateDeclareCaptureParams{TransactionId: &txID}
				return clientset.V31().CreateDeclareCapture(ctx, p, idx, params, m)
			},
			func(p string, idx int, m v30.Capture) (*http.Response, error) {
				params := &v30.CreateDeclareCaptureParams{TransactionId: &txID}
				return clientset.V30().CreateDeclareCapture(ctx, p, idx, params, m)
			},
			func(p string, idx int, m v32ee.Capture) (*http.Response, error) {
				params := &v32ee.CreateDeclareCaptureParams{TransactionId: &txID}
				return clientset.V32EE().CreateDeclareCapture(ctx, p, idx, params, m)
			},
			func(p string, idx int, m v31ee.Capture) (*http.Response, error) {
				params := &v31ee.CreateDeclareCaptureParams{TransactionId: &txID}
				return clientset.V31EE().CreateDeclareCapture(ctx, p, idx, params, m)
			},
			func(p string, idx int, m v30ee.Capture) (*http.Response, error) {
				params := &v30ee.CreateDeclareCaptureParams{TransactionId: &txID}
				return clientset.V30EE().CreateDeclareCapture(ctx, p, idx, params, m)
			},
		)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		return client.CheckResponse(resp, "declare capture creation in frontend")
	}
}

// DeclareCaptureFrontendUpdate returns an executor for updating declare captures in frontends.
func DeclareCaptureFrontendUpdate() func(ctx context.Context, c *client.DataplaneClient, txID string, parent string, index int, model *models.Capture) error {
	return func(ctx context.Context, c *client.DataplaneClient, txID string, parent string, index int, model *models.Capture) error {
		clientset := c.Clientset()

		resp, err := client.DispatchReplaceChild(ctx, c, parent, index, model,
			func(p string, idx int, m v32.Capture) (*http.Response, error) {
				params := &v32.ReplaceDeclareCaptureParams{TransactionId: &txID}
				return clientset.V32().ReplaceDeclareCapture(ctx, p, idx, params, m)
			},
			func(p string, idx int, m v31.Capture) (*http.Response, error) {
				params := &v31.ReplaceDeclareCaptureParams{TransactionId: &txID}
				return clientset.V31().ReplaceDeclareCapture(ctx, p, idx, params, m)
			},
			func(p string, idx int, m v30.Capture) (*http.Response, error) {
				params := &v30.ReplaceDeclareCaptureParams{TransactionId: &txID}
				return clientset.V30().ReplaceDeclareCapture(ctx, p, idx, params, m)
			},
			func(p string, idx int, m v32ee.Capture) (*http.Response, error) {
				params := &v32ee.ReplaceDeclareCaptureParams{TransactionId: &txID}
				return clientset.V32EE().ReplaceDeclareCapture(ctx, p, idx, params, m)
			},
			func(p string, idx int, m v31ee.Capture) (*http.Response, error) {
				params := &v31ee.ReplaceDeclareCaptureParams{TransactionId: &txID}
				return clientset.V31EE().ReplaceDeclareCapture(ctx, p, idx, params, m)
			},
			func(p string, idx int, m v30ee.Capture) (*http.Response, error) {
				params := &v30ee.ReplaceDeclareCaptureParams{TransactionId: &txID}
				return clientset.V30EE().ReplaceDeclareCapture(ctx, p, idx, params, m)
			},
		)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		return client.CheckResponse(resp, "declare capture update in frontend")
	}
}

// DeclareCaptureFrontendDelete returns an executor for deleting declare captures from frontends.
func DeclareCaptureFrontendDelete() func(ctx context.Context, c *client.DataplaneClient, txID string, parent string, index int, _ *models.Capture) error {
	return func(ctx context.Context, c *client.DataplaneClient, txID string, parent string, index int, _ *models.Capture) error {
		clientset := c.Clientset()

		resp, err := client.DispatchDeleteChild(ctx, c, parent, index,
			func(p string, idx int) (*http.Response, error) {
				params := &v32.DeleteDeclareCaptureParams{TransactionId: &txID}
				return clientset.V32().DeleteDeclareCapture(ctx, p, idx, params)
			},
			func(p string, idx int) (*http.Response, error) {
				params := &v31.DeleteDeclareCaptureParams{TransactionId: &txID}
				return clientset.V31().DeleteDeclareCapture(ctx, p, idx, params)
			},
			func(p string, idx int) (*http.Response, error) {
				params := &v30.DeleteDeclareCaptureParams{TransactionId: &txID}
				return clientset.V30().DeleteDeclareCapture(ctx, p, idx, params)
			},
			func(p string, idx int) (*http.Response, error) {
				params := &v32ee.DeleteDeclareCaptureParams{TransactionId: &txID}
				return clientset.V32EE().DeleteDeclareCapture(ctx, p, idx, params)
			},
			func(p string, idx int) (*http.Response, error) {
				params := &v31ee.DeleteDeclareCaptureParams{TransactionId: &txID}
				return clientset.V31EE().DeleteDeclareCapture(ctx, p, idx, params)
			},
			func(p string, idx int) (*http.Response, error) {
				params := &v30ee.DeleteDeclareCaptureParams{TransactionId: &txID}
				return clientset.V30EE().DeleteDeclareCapture(ctx, p, idx, params)
			},
		)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		return client.CheckResponse(resp, "declare capture deletion from frontend")
	}
}
