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
	v33 "gitlab.com/haproxy-haptic/haptic/pkg/generated/dataplaneapi/v33"
)

// GlobalUpdate returns an executor for updating the global section.
// The global section is a singleton - it always exists and can only be updated.
func GlobalUpdate() func(ctx context.Context, c *client.DataplaneClient, txID string, model *models.Global) error {
	return func(ctx context.Context, c *client.DataplaneClient, txID string, model *models.Global) error {
		clientset := c.Clientset()

		// Global uses DispatchUpdate with empty name since it's a singleton
		resp, err := client.DispatchUpdate(ctx, c, "", model,
			func(_ string, m v33.Global) (*http.Response, error) {
				params := &v33.ReplaceGlobalParams{TransactionId: &txID}
				return clientset.V33().ReplaceGlobal(ctx, params, m)
			},
			func(_ string, m v32.Global) (*http.Response, error) {
				params := &v32.ReplaceGlobalParams{TransactionId: &txID}
				return clientset.V32().ReplaceGlobal(ctx, params, m)
			},
			func(_ string, m v31.Global) (*http.Response, error) {
				params := &v31.ReplaceGlobalParams{TransactionId: &txID}
				return clientset.V31().ReplaceGlobal(ctx, params, m)
			},
			func(_ string, m v30.Global) (*http.Response, error) {
				params := &v30.ReplaceGlobalParams{TransactionId: &txID}
				return clientset.V30().ReplaceGlobal(ctx, params, m)
			},
			func(_ string, m v32ee.Global) (*http.Response, error) {
				params := &v32ee.ReplaceGlobalParams{TransactionId: &txID}
				return clientset.V32EE().ReplaceGlobal(ctx, params, m)
			},
			func(_ string, m v31ee.Global) (*http.Response, error) {
				params := &v31ee.ReplaceGlobalParams{TransactionId: &txID}
				return clientset.V31EE().ReplaceGlobal(ctx, params, m)
			},
			func(_ string, m v30ee.Global) (*http.Response, error) {
				params := &v30ee.ReplaceGlobalParams{TransactionId: &txID}
				return clientset.V30EE().ReplaceGlobal(ctx, params, m)
			},
		)
		return dispatchAndCheck(resp, err, "global section update")
	}
}
