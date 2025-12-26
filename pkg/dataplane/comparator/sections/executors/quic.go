// Package executors provides pre-built executor functions for HAProxy configuration operations.
package executors

import (
	"context"
	"fmt"
	"net/http"

	"github.com/haproxytech/client-native/v6/models"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/client"
	v31 "gitlab.com/haproxy-haptic/haptic/pkg/generated/dataplaneapi/v31"
	v31ee "gitlab.com/haproxy-haptic/haptic/pkg/generated/dataplaneapi/v31ee"
	v32 "gitlab.com/haproxy-haptic/haptic/pkg/generated/dataplaneapi/v32"
	v32ee "gitlab.com/haproxy-haptic/haptic/pkg/generated/dataplaneapi/v32ee"
)

// =============================================================================
// QUIC Initial Rule Executors (Frontend) - v3.1+ only
// =============================================================================

// QUICInitialRuleFrontendCreate returns an executor for creating QUIC initial rules in frontends.
// QUIC initial rules are only available in HAProxy DataPlane API v3.1+.
func QUICInitialRuleFrontendCreate() func(ctx context.Context, c *client.DataplaneClient, txID string, parent string, index int, model *models.QUICInitialRule) error {
	return func(ctx context.Context, c *client.DataplaneClient, txID string, parent string, index int, model *models.QUICInitialRule) error {
		clientset := c.Clientset()

		resp, err := DispatchCreateChild31Plus(ctx, c, parent, index, model,
			func(p string, idx int, m v32.QUICInitialRule) (*http.Response, error) {
				params := &v32.CreateQUICInitialRuleFrontendParams{TransactionId: &txID}
				return clientset.V32().CreateQUICInitialRuleFrontend(ctx, p, idx, params, m)
			},
			func(p string, idx int, m v31.QUICInitialRule) (*http.Response, error) {
				params := &v31.CreateQUICInitialRuleFrontendParams{TransactionId: &txID}
				return clientset.V31().CreateQUICInitialRuleFrontend(ctx, p, idx, params, m)
			},
			func(p string, idx int, m v32ee.QUICInitialRule) (*http.Response, error) {
				params := &v32ee.CreateQUICInitialRuleFrontendParams{TransactionId: &txID}
				return clientset.V32EE().CreateQUICInitialRuleFrontend(ctx, p, idx, params, m)
			},
			func(p string, idx int, m v31ee.QUICInitialRule) (*http.Response, error) {
				params := &v31ee.CreateQUICInitialRuleFrontendParams{TransactionId: &txID}
				return clientset.V31EE().CreateQUICInitialRuleFrontend(ctx, p, idx, params, m)
			},
		)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		return client.CheckResponse(resp, "QUIC initial rule creation in frontend")
	}
}

// QUICInitialRuleFrontendUpdate returns an executor for updating QUIC initial rules in frontends.
// QUIC initial rules are only available in HAProxy DataPlane API v3.1+.
func QUICInitialRuleFrontendUpdate() func(ctx context.Context, c *client.DataplaneClient, txID string, parent string, index int, model *models.QUICInitialRule) error {
	return func(ctx context.Context, c *client.DataplaneClient, txID string, parent string, index int, model *models.QUICInitialRule) error {
		clientset := c.Clientset()

		resp, err := DispatchReplaceChild31Plus(ctx, c, parent, index, model,
			func(p string, idx int, m v32.QUICInitialRule) (*http.Response, error) {
				params := &v32.ReplaceQUICInitialRuleFrontendParams{TransactionId: &txID}
				return clientset.V32().ReplaceQUICInitialRuleFrontend(ctx, p, idx, params, m)
			},
			func(p string, idx int, m v31.QUICInitialRule) (*http.Response, error) {
				params := &v31.ReplaceQUICInitialRuleFrontendParams{TransactionId: &txID}
				return clientset.V31().ReplaceQUICInitialRuleFrontend(ctx, p, idx, params, m)
			},
			func(p string, idx int, m v32ee.QUICInitialRule) (*http.Response, error) {
				params := &v32ee.ReplaceQUICInitialRuleFrontendParams{TransactionId: &txID}
				return clientset.V32EE().ReplaceQUICInitialRuleFrontend(ctx, p, idx, params, m)
			},
			func(p string, idx int, m v31ee.QUICInitialRule) (*http.Response, error) {
				params := &v31ee.ReplaceQUICInitialRuleFrontendParams{TransactionId: &txID}
				return clientset.V31EE().ReplaceQUICInitialRuleFrontend(ctx, p, idx, params, m)
			},
		)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		return client.CheckResponse(resp, "QUIC initial rule update in frontend")
	}
}

// QUICInitialRuleFrontendDelete returns an executor for deleting QUIC initial rules from frontends.
// QUIC initial rules are only available in HAProxy DataPlane API v3.1+.
func QUICInitialRuleFrontendDelete() func(ctx context.Context, c *client.DataplaneClient, txID string, parent string, index int, _ *models.QUICInitialRule) error {
	return func(ctx context.Context, c *client.DataplaneClient, txID string, parent string, index int, _ *models.QUICInitialRule) error {
		clientset := c.Clientset()

		resp, err := DispatchDeleteChild31Plus(ctx, c, parent, index,
			func(p string, idx int) (*http.Response, error) {
				params := &v32.DeleteQUICInitialRuleFrontendParams{TransactionId: &txID}
				return clientset.V32().DeleteQUICInitialRuleFrontend(ctx, p, idx, params)
			},
			func(p string, idx int) (*http.Response, error) {
				params := &v31.DeleteQUICInitialRuleFrontendParams{TransactionId: &txID}
				return clientset.V31().DeleteQUICInitialRuleFrontend(ctx, p, idx, params)
			},
			func(p string, idx int) (*http.Response, error) {
				params := &v32ee.DeleteQUICInitialRuleFrontendParams{TransactionId: &txID}
				return clientset.V32EE().DeleteQUICInitialRuleFrontend(ctx, p, idx, params)
			},
			func(p string, idx int) (*http.Response, error) {
				params := &v31ee.DeleteQUICInitialRuleFrontendParams{TransactionId: &txID}
				return clientset.V31EE().DeleteQUICInitialRuleFrontend(ctx, p, idx, params)
			},
		)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		return client.CheckResponse(resp, "QUIC initial rule deletion from frontend")
	}
}

// =============================================================================
// QUIC Initial Rule Executors (Defaults) - v3.1+ only
// =============================================================================

// QUICInitialRuleDefaultsCreate returns an executor for creating QUIC initial rules in defaults.
// QUIC initial rules are only available in HAProxy DataPlane API v3.1+.
func QUICInitialRuleDefaultsCreate() func(ctx context.Context, c *client.DataplaneClient, txID string, parent string, index int, model *models.QUICInitialRule) error {
	return func(ctx context.Context, c *client.DataplaneClient, txID string, parent string, index int, model *models.QUICInitialRule) error {
		clientset := c.Clientset()

		resp, err := DispatchCreateChild31Plus(ctx, c, parent, index, model,
			func(p string, idx int, m v32.QUICInitialRule) (*http.Response, error) {
				params := &v32.CreateQUICInitialRuleDefaultsParams{TransactionId: &txID}
				return clientset.V32().CreateQUICInitialRuleDefaults(ctx, p, idx, params, m)
			},
			func(p string, idx int, m v31.QUICInitialRule) (*http.Response, error) {
				params := &v31.CreateQUICInitialRuleDefaultsParams{TransactionId: &txID}
				return clientset.V31().CreateQUICInitialRuleDefaults(ctx, p, idx, params, m)
			},
			func(p string, idx int, m v32ee.QUICInitialRule) (*http.Response, error) {
				params := &v32ee.CreateQUICInitialRuleDefaultsParams{TransactionId: &txID}
				return clientset.V32EE().CreateQUICInitialRuleDefaults(ctx, p, idx, params, m)
			},
			func(p string, idx int, m v31ee.QUICInitialRule) (*http.Response, error) {
				params := &v31ee.CreateQUICInitialRuleDefaultsParams{TransactionId: &txID}
				return clientset.V31EE().CreateQUICInitialRuleDefaults(ctx, p, idx, params, m)
			},
		)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		return client.CheckResponse(resp, "QUIC initial rule creation in defaults")
	}
}

// QUICInitialRuleDefaultsUpdate returns an executor for updating QUIC initial rules in defaults.
// QUIC initial rules are only available in HAProxy DataPlane API v3.1+.
func QUICInitialRuleDefaultsUpdate() func(ctx context.Context, c *client.DataplaneClient, txID string, parent string, index int, model *models.QUICInitialRule) error {
	return func(ctx context.Context, c *client.DataplaneClient, txID string, parent string, index int, model *models.QUICInitialRule) error {
		clientset := c.Clientset()

		resp, err := DispatchReplaceChild31Plus(ctx, c, parent, index, model,
			func(p string, idx int, m v32.QUICInitialRule) (*http.Response, error) {
				params := &v32.ReplaceQUICInitialRuleDefaultsParams{TransactionId: &txID}
				return clientset.V32().ReplaceQUICInitialRuleDefaults(ctx, p, idx, params, m)
			},
			func(p string, idx int, m v31.QUICInitialRule) (*http.Response, error) {
				params := &v31.ReplaceQUICInitialRuleDefaultsParams{TransactionId: &txID}
				return clientset.V31().ReplaceQUICInitialRuleDefaults(ctx, p, idx, params, m)
			},
			func(p string, idx int, m v32ee.QUICInitialRule) (*http.Response, error) {
				params := &v32ee.ReplaceQUICInitialRuleDefaultsParams{TransactionId: &txID}
				return clientset.V32EE().ReplaceQUICInitialRuleDefaults(ctx, p, idx, params, m)
			},
			func(p string, idx int, m v31ee.QUICInitialRule) (*http.Response, error) {
				params := &v31ee.ReplaceQUICInitialRuleDefaultsParams{TransactionId: &txID}
				return clientset.V31EE().ReplaceQUICInitialRuleDefaults(ctx, p, idx, params, m)
			},
		)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		return client.CheckResponse(resp, "QUIC initial rule update in defaults")
	}
}

// QUICInitialRuleDefaultsDelete returns an executor for deleting QUIC initial rules from defaults.
// QUIC initial rules are only available in HAProxy DataPlane API v3.1+.
func QUICInitialRuleDefaultsDelete() func(ctx context.Context, c *client.DataplaneClient, txID string, parent string, index int, _ *models.QUICInitialRule) error {
	return func(ctx context.Context, c *client.DataplaneClient, txID string, parent string, index int, _ *models.QUICInitialRule) error {
		clientset := c.Clientset()

		resp, err := DispatchDeleteChild31Plus(ctx, c, parent, index,
			func(p string, idx int) (*http.Response, error) {
				params := &v32.DeleteQUICInitialRuleDefaultsParams{TransactionId: &txID}
				return clientset.V32().DeleteQUICInitialRuleDefaults(ctx, p, idx, params)
			},
			func(p string, idx int) (*http.Response, error) {
				params := &v31.DeleteQUICInitialRuleDefaultsParams{TransactionId: &txID}
				return clientset.V31().DeleteQUICInitialRuleDefaults(ctx, p, idx, params)
			},
			func(p string, idx int) (*http.Response, error) {
				params := &v32ee.DeleteQUICInitialRuleDefaultsParams{TransactionId: &txID}
				return clientset.V32EE().DeleteQUICInitialRuleDefaults(ctx, p, idx, params)
			},
			func(p string, idx int) (*http.Response, error) {
				params := &v31ee.DeleteQUICInitialRuleDefaultsParams{TransactionId: &txID}
				return clientset.V31EE().DeleteQUICInitialRuleDefaults(ctx, p, idx, params)
			},
		)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		return client.CheckResponse(resp, "QUIC initial rule deletion from defaults")
	}
}

// =============================================================================
// v3.1+ Child Dispatch Helpers
// These are specialized dispatchers for indexed child resources only available in v3.1+.
// =============================================================================

// DispatchCreateChild31Plus is a generic helper for create operations on v3.1+ only indexed child features.
func DispatchCreateChild31Plus[TUnified any, TV32 any, TV31 any, TV32EE any, TV31EE any](
	ctx context.Context,
	c *client.DataplaneClient,
	parent string,
	index int,
	unifiedModel TUnified,
	v32Call func(string, int, TV32) (*http.Response, error),
	v31Call func(string, int, TV31) (*http.Response, error),
	v32eeCall func(string, int, TV32EE) (*http.Response, error),
	v31eeCall func(string, int, TV31EE) (*http.Response, error),
) (*http.Response, error) {
	return client.DispatchCreateChild(ctx, c, parent, index, unifiedModel,
		v32Call,
		v31Call,
		func(_ string, _ int, _ struct{}) (*http.Response, error) {
			return nil, fmt.Errorf("this feature requires DataPlane API v3.1+")
		},
		v32eeCall,
		v31eeCall,
		func(_ string, _ int, _ struct{}) (*http.Response, error) {
			return nil, fmt.Errorf("this feature requires DataPlane API v3.1+")
		},
	)
}

// DispatchReplaceChild31Plus is a generic helper for replace operations on v3.1+ only indexed child features.
func DispatchReplaceChild31Plus[TUnified any, TV32 any, TV31 any, TV32EE any, TV31EE any](
	ctx context.Context,
	c *client.DataplaneClient,
	parent string,
	index int,
	unifiedModel TUnified,
	v32Call func(string, int, TV32) (*http.Response, error),
	v31Call func(string, int, TV31) (*http.Response, error),
	v32eeCall func(string, int, TV32EE) (*http.Response, error),
	v31eeCall func(string, int, TV31EE) (*http.Response, error),
) (*http.Response, error) {
	return client.DispatchReplaceChild(ctx, c, parent, index, unifiedModel,
		v32Call,
		v31Call,
		func(_ string, _ int, _ struct{}) (*http.Response, error) {
			return nil, fmt.Errorf("this feature requires DataPlane API v3.1+")
		},
		v32eeCall,
		v31eeCall,
		func(_ string, _ int, _ struct{}) (*http.Response, error) {
			return nil, fmt.Errorf("this feature requires DataPlane API v3.1+")
		},
	)
}

// DispatchDeleteChild31Plus is a generic helper for delete operations on v3.1+ only indexed child features.
func DispatchDeleteChild31Plus(
	ctx context.Context,
	c *client.DataplaneClient,
	parent string,
	index int,
	v32Call func(string, int) (*http.Response, error),
	v31Call func(string, int) (*http.Response, error),
	v32eeCall func(string, int) (*http.Response, error),
	v31eeCall func(string, int) (*http.Response, error),
) (*http.Response, error) {
	return client.DispatchDeleteChild(ctx, c, parent, index,
		v32Call,
		v31Call,
		func(_ string, _ int) (*http.Response, error) {
			return nil, fmt.Errorf("this feature requires DataPlane API v3.1+")
		},
		v32eeCall,
		v31eeCall,
		func(_ string, _ int) (*http.Response, error) {
			return nil, fmt.Errorf("this feature requires DataPlane API v3.1+")
		},
	)
}
