// Package executors provides pre-built executor functions for HAProxy configuration operations.
//
// These functions encapsulate the dispatcher callback boilerplate, providing a clean
// interface between the generic operation types and the versioned DataPlane API clients.
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

// BackendCreate returns an executor for creating backends.
func BackendCreate() func(ctx context.Context, c *client.DataplaneClient, txID string, model *models.Backend, name string) error {
	return func(ctx context.Context, c *client.DataplaneClient, txID string, model *models.Backend, _ string) error {
		clientset := c.Clientset()

		resp, err := client.DispatchCreate(ctx, c, model,
			func(m v33.Backend) (*http.Response, error) {
				params := &v33.CreateBackendParams{TransactionId: &txID}
				return clientset.V33().CreateBackend(ctx, params, m)
			},
			func(m v32.Backend) (*http.Response, error) {
				params := &v32.CreateBackendParams{TransactionId: &txID}
				return clientset.V32().CreateBackend(ctx, params, m)
			},
			func(m v31.Backend) (*http.Response, error) {
				params := &v31.CreateBackendParams{TransactionId: &txID}
				return clientset.V31().CreateBackend(ctx, params, m)
			},
			func(m v30.Backend) (*http.Response, error) {
				params := &v30.CreateBackendParams{TransactionId: &txID}
				return clientset.V30().CreateBackend(ctx, params, m)
			},
			func(m v32ee.Backend) (*http.Response, error) {
				params := &v32ee.CreateBackendParams{TransactionId: &txID}
				return clientset.V32EE().CreateBackend(ctx, params, m)
			},
			func(m v31ee.Backend) (*http.Response, error) {
				params := &v31ee.CreateBackendParams{TransactionId: &txID}
				return clientset.V31EE().CreateBackend(ctx, params, m)
			},
			func(m v30ee.Backend) (*http.Response, error) {
				params := &v30ee.CreateBackendParams{TransactionId: &txID}
				return clientset.V30EE().CreateBackend(ctx, params, m)
			},
		)
		return dispatchAndCheck(resp, err, "backend creation")
	}
}

// BackendUpdate returns an executor for updating backends.
func BackendUpdate() func(ctx context.Context, c *client.DataplaneClient, txID string, model *models.Backend, name string) error {
	return func(ctx context.Context, c *client.DataplaneClient, txID string, model *models.Backend, name string) error {
		clientset := c.Clientset()

		resp, err := client.DispatchUpdate(ctx, c, name, model,
			func(n string, m v33.Backend) (*http.Response, error) {
				params := &v33.ReplaceBackendParams{TransactionId: &txID}
				return clientset.V33().ReplaceBackend(ctx, n, params, m)
			},
			func(n string, m v32.Backend) (*http.Response, error) {
				params := &v32.ReplaceBackendParams{TransactionId: &txID}
				return clientset.V32().ReplaceBackend(ctx, n, params, m)
			},
			func(n string, m v31.Backend) (*http.Response, error) {
				params := &v31.ReplaceBackendParams{TransactionId: &txID}
				return clientset.V31().ReplaceBackend(ctx, n, params, m)
			},
			func(n string, m v30.Backend) (*http.Response, error) {
				params := &v30.ReplaceBackendParams{TransactionId: &txID}
				return clientset.V30().ReplaceBackend(ctx, n, params, m)
			},
			func(n string, m v32ee.Backend) (*http.Response, error) {
				params := &v32ee.ReplaceBackendParams{TransactionId: &txID}
				return clientset.V32EE().ReplaceBackend(ctx, n, params, m)
			},
			func(n string, m v31ee.Backend) (*http.Response, error) {
				params := &v31ee.ReplaceBackendParams{TransactionId: &txID}
				return clientset.V31EE().ReplaceBackend(ctx, n, params, m)
			},
			func(n string, m v30ee.Backend) (*http.Response, error) {
				params := &v30ee.ReplaceBackendParams{TransactionId: &txID}
				return clientset.V30EE().ReplaceBackend(ctx, n, params, m)
			},
		)
		return dispatchAndCheck(resp, err, "backend update")
	}
}

// BackendDelete returns an executor for deleting backends.
func BackendDelete() func(ctx context.Context, c *client.DataplaneClient, txID string, _ *models.Backend, name string) error {
	return func(ctx context.Context, c *client.DataplaneClient, txID string, _ *models.Backend, name string) error {
		clientset := c.Clientset()

		resp, err := client.DispatchDelete(ctx, c, name,
			func(n string) (*http.Response, error) {
				params := &v33.DeleteBackendParams{TransactionId: &txID}
				return clientset.V33().DeleteBackend(ctx, n, params)
			},
			func(n string) (*http.Response, error) {
				params := &v32.DeleteBackendParams{TransactionId: &txID}
				return clientset.V32().DeleteBackend(ctx, n, params)
			},
			func(n string) (*http.Response, error) {
				params := &v31.DeleteBackendParams{TransactionId: &txID}
				return clientset.V31().DeleteBackend(ctx, n, params)
			},
			func(n string) (*http.Response, error) {
				params := &v30.DeleteBackendParams{TransactionId: &txID}
				return clientset.V30().DeleteBackend(ctx, n, params)
			},
			func(n string) (*http.Response, error) {
				params := &v32ee.DeleteBackendParams{TransactionId: &txID}
				return clientset.V32EE().DeleteBackend(ctx, n, params)
			},
			func(n string) (*http.Response, error) {
				params := &v31ee.DeleteBackendParams{TransactionId: &txID}
				return clientset.V31EE().DeleteBackend(ctx, n, params)
			},
			func(n string) (*http.Response, error) {
				params := &v30ee.DeleteBackendParams{TransactionId: &txID}
				return clientset.V30EE().DeleteBackend(ctx, n, params)
			},
		)
		return dispatchAndCheck(resp, err, "backend deletion")
	}
}

// FrontendCreate returns an executor for creating frontends.
func FrontendCreate() func(ctx context.Context, c *client.DataplaneClient, txID string, model *models.Frontend, name string) error {
	return func(ctx context.Context, c *client.DataplaneClient, txID string, model *models.Frontend, _ string) error {
		clientset := c.Clientset()

		resp, err := client.DispatchCreate(ctx, c, model,
			func(m v33.Frontend) (*http.Response, error) {
				params := &v33.CreateFrontendParams{TransactionId: &txID}
				return clientset.V33().CreateFrontend(ctx, params, m)
			},
			func(m v32.Frontend) (*http.Response, error) {
				params := &v32.CreateFrontendParams{TransactionId: &txID}
				return clientset.V32().CreateFrontend(ctx, params, m)
			},
			func(m v31.Frontend) (*http.Response, error) {
				params := &v31.CreateFrontendParams{TransactionId: &txID}
				return clientset.V31().CreateFrontend(ctx, params, m)
			},
			func(m v30.Frontend) (*http.Response, error) {
				params := &v30.CreateFrontendParams{TransactionId: &txID}
				return clientset.V30().CreateFrontend(ctx, params, m)
			},
			func(m v32ee.Frontend) (*http.Response, error) {
				params := &v32ee.CreateFrontendParams{TransactionId: &txID}
				return clientset.V32EE().CreateFrontend(ctx, params, m)
			},
			func(m v31ee.Frontend) (*http.Response, error) {
				params := &v31ee.CreateFrontendParams{TransactionId: &txID}
				return clientset.V31EE().CreateFrontend(ctx, params, m)
			},
			func(m v30ee.Frontend) (*http.Response, error) {
				params := &v30ee.CreateFrontendParams{TransactionId: &txID}
				return clientset.V30EE().CreateFrontend(ctx, params, m)
			},
		)
		return dispatchAndCheck(resp, err, "frontend creation")
	}
}

// FrontendUpdate returns an executor for updating frontends.
func FrontendUpdate() func(ctx context.Context, c *client.DataplaneClient, txID string, model *models.Frontend, name string) error {
	return func(ctx context.Context, c *client.DataplaneClient, txID string, model *models.Frontend, name string) error {
		clientset := c.Clientset()

		resp, err := client.DispatchUpdate(ctx, c, name, model,
			func(n string, m v33.Frontend) (*http.Response, error) {
				params := &v33.ReplaceFrontendParams{TransactionId: &txID}
				return clientset.V33().ReplaceFrontend(ctx, n, params, m)
			},
			func(n string, m v32.Frontend) (*http.Response, error) {
				params := &v32.ReplaceFrontendParams{TransactionId: &txID}
				return clientset.V32().ReplaceFrontend(ctx, n, params, m)
			},
			func(n string, m v31.Frontend) (*http.Response, error) {
				params := &v31.ReplaceFrontendParams{TransactionId: &txID}
				return clientset.V31().ReplaceFrontend(ctx, n, params, m)
			},
			func(n string, m v30.Frontend) (*http.Response, error) {
				params := &v30.ReplaceFrontendParams{TransactionId: &txID}
				return clientset.V30().ReplaceFrontend(ctx, n, params, m)
			},
			func(n string, m v32ee.Frontend) (*http.Response, error) {
				params := &v32ee.ReplaceFrontendParams{TransactionId: &txID}
				return clientset.V32EE().ReplaceFrontend(ctx, n, params, m)
			},
			func(n string, m v31ee.Frontend) (*http.Response, error) {
				params := &v31ee.ReplaceFrontendParams{TransactionId: &txID}
				return clientset.V31EE().ReplaceFrontend(ctx, n, params, m)
			},
			func(n string, m v30ee.Frontend) (*http.Response, error) {
				params := &v30ee.ReplaceFrontendParams{TransactionId: &txID}
				return clientset.V30EE().ReplaceFrontend(ctx, n, params, m)
			},
		)
		return dispatchAndCheck(resp, err, "frontend update")
	}
}

// FrontendDelete returns an executor for deleting frontends.
func FrontendDelete() func(ctx context.Context, c *client.DataplaneClient, txID string, _ *models.Frontend, name string) error {
	return func(ctx context.Context, c *client.DataplaneClient, txID string, _ *models.Frontend, name string) error {
		clientset := c.Clientset()

		resp, err := client.DispatchDelete(ctx, c, name,
			func(n string) (*http.Response, error) {
				params := &v33.DeleteFrontendParams{TransactionId: &txID}
				return clientset.V33().DeleteFrontend(ctx, n, params)
			},
			func(n string) (*http.Response, error) {
				params := &v32.DeleteFrontendParams{TransactionId: &txID}
				return clientset.V32().DeleteFrontend(ctx, n, params)
			},
			func(n string) (*http.Response, error) {
				params := &v31.DeleteFrontendParams{TransactionId: &txID}
				return clientset.V31().DeleteFrontend(ctx, n, params)
			},
			func(n string) (*http.Response, error) {
				params := &v30.DeleteFrontendParams{TransactionId: &txID}
				return clientset.V30().DeleteFrontend(ctx, n, params)
			},
			func(n string) (*http.Response, error) {
				params := &v32ee.DeleteFrontendParams{TransactionId: &txID}
				return clientset.V32EE().DeleteFrontend(ctx, n, params)
			},
			func(n string) (*http.Response, error) {
				params := &v31ee.DeleteFrontendParams{TransactionId: &txID}
				return clientset.V31EE().DeleteFrontend(ctx, n, params)
			},
			func(n string) (*http.Response, error) {
				params := &v30ee.DeleteFrontendParams{TransactionId: &txID}
				return clientset.V30EE().DeleteFrontend(ctx, n, params)
			},
		)
		return dispatchAndCheck(resp, err, "frontend deletion")
	}
}

// DefaultsCreate returns an executor for creating defaults sections.
func DefaultsCreate() func(ctx context.Context, c *client.DataplaneClient, txID string, model *models.Defaults, name string) error {
	return func(ctx context.Context, c *client.DataplaneClient, txID string, model *models.Defaults, _ string) error {
		clientset := c.Clientset()

		resp, err := client.DispatchCreate(ctx, c, model,
			func(m v33.Defaults) (*http.Response, error) {
				params := &v33.CreateDefaultsSectionParams{TransactionId: &txID}
				return clientset.V33().CreateDefaultsSection(ctx, params, m)
			},
			func(m v32.Defaults) (*http.Response, error) {
				params := &v32.CreateDefaultsSectionParams{TransactionId: &txID}
				return clientset.V32().CreateDefaultsSection(ctx, params, m)
			},
			func(m v31.Defaults) (*http.Response, error) {
				params := &v31.CreateDefaultsSectionParams{TransactionId: &txID}
				return clientset.V31().CreateDefaultsSection(ctx, params, m)
			},
			func(m v30.Defaults) (*http.Response, error) {
				params := &v30.CreateDefaultsSectionParams{TransactionId: &txID}
				return clientset.V30().CreateDefaultsSection(ctx, params, m)
			},
			func(m v32ee.Defaults) (*http.Response, error) {
				params := &v32ee.CreateDefaultsSectionParams{TransactionId: &txID}
				return clientset.V32EE().CreateDefaultsSection(ctx, params, m)
			},
			func(m v31ee.Defaults) (*http.Response, error) {
				params := &v31ee.CreateDefaultsSectionParams{TransactionId: &txID}
				return clientset.V31EE().CreateDefaultsSection(ctx, params, m)
			},
			func(m v30ee.Defaults) (*http.Response, error) {
				params := &v30ee.CreateDefaultsSectionParams{TransactionId: &txID}
				return clientset.V30EE().CreateDefaultsSection(ctx, params, m)
			},
		)
		return dispatchAndCheck(resp, err, "defaults creation")
	}
}

// DefaultsUpdate returns an executor for updating defaults sections.
func DefaultsUpdate() func(ctx context.Context, c *client.DataplaneClient, txID string, model *models.Defaults, name string) error {
	return func(ctx context.Context, c *client.DataplaneClient, txID string, model *models.Defaults, name string) error {
		clientset := c.Clientset()

		resp, err := client.DispatchUpdate(ctx, c, name, model,
			func(n string, m v33.Defaults) (*http.Response, error) {
				params := &v33.ReplaceDefaultsSectionParams{TransactionId: &txID}
				return clientset.V33().ReplaceDefaultsSection(ctx, n, params, m)
			},
			func(n string, m v32.Defaults) (*http.Response, error) {
				params := &v32.ReplaceDefaultsSectionParams{TransactionId: &txID}
				return clientset.V32().ReplaceDefaultsSection(ctx, n, params, m)
			},
			func(n string, m v31.Defaults) (*http.Response, error) {
				params := &v31.ReplaceDefaultsSectionParams{TransactionId: &txID}
				return clientset.V31().ReplaceDefaultsSection(ctx, n, params, m)
			},
			func(n string, m v30.Defaults) (*http.Response, error) {
				params := &v30.ReplaceDefaultsSectionParams{TransactionId: &txID}
				return clientset.V30().ReplaceDefaultsSection(ctx, n, params, m)
			},
			func(n string, m v32ee.Defaults) (*http.Response, error) {
				params := &v32ee.ReplaceDefaultsSectionParams{TransactionId: &txID}
				return clientset.V32EE().ReplaceDefaultsSection(ctx, n, params, m)
			},
			func(n string, m v31ee.Defaults) (*http.Response, error) {
				params := &v31ee.ReplaceDefaultsSectionParams{TransactionId: &txID}
				return clientset.V31EE().ReplaceDefaultsSection(ctx, n, params, m)
			},
			func(n string, m v30ee.Defaults) (*http.Response, error) {
				params := &v30ee.ReplaceDefaultsSectionParams{TransactionId: &txID}
				return clientset.V30EE().ReplaceDefaultsSection(ctx, n, params, m)
			},
		)
		return dispatchAndCheck(resp, err, "defaults update")
	}
}

// DefaultsDelete returns an executor for deleting defaults sections.
func DefaultsDelete() func(ctx context.Context, c *client.DataplaneClient, txID string, _ *models.Defaults, name string) error {
	return func(ctx context.Context, c *client.DataplaneClient, txID string, _ *models.Defaults, name string) error {
		clientset := c.Clientset()

		resp, err := client.DispatchDelete(ctx, c, name,
			func(n string) (*http.Response, error) {
				params := &v33.DeleteDefaultsSectionParams{TransactionId: &txID}
				return clientset.V33().DeleteDefaultsSection(ctx, n, params)
			},
			func(n string) (*http.Response, error) {
				params := &v32.DeleteDefaultsSectionParams{TransactionId: &txID}
				return clientset.V32().DeleteDefaultsSection(ctx, n, params)
			},
			func(n string) (*http.Response, error) {
				params := &v31.DeleteDefaultsSectionParams{TransactionId: &txID}
				return clientset.V31().DeleteDefaultsSection(ctx, n, params)
			},
			func(n string) (*http.Response, error) {
				params := &v30.DeleteDefaultsSectionParams{TransactionId: &txID}
				return clientset.V30().DeleteDefaultsSection(ctx, n, params)
			},
			func(n string) (*http.Response, error) {
				params := &v32ee.DeleteDefaultsSectionParams{TransactionId: &txID}
				return clientset.V32EE().DeleteDefaultsSection(ctx, n, params)
			},
			func(n string) (*http.Response, error) {
				params := &v31ee.DeleteDefaultsSectionParams{TransactionId: &txID}
				return clientset.V31EE().DeleteDefaultsSection(ctx, n, params)
			},
			func(n string) (*http.Response, error) {
				params := &v30ee.DeleteDefaultsSectionParams{TransactionId: &txID}
				return clientset.V30EE().DeleteDefaultsSection(ctx, n, params)
			},
		)
		return dispatchAndCheck(resp, err, "defaults deletion")
	}
}
