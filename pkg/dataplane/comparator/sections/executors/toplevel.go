// Package executors provides pre-built executor functions for HAProxy configuration operations.
//
// These functions encapsulate the dispatcher callback boilerplate, providing a clean
// interface between the generic operation types and the versioned DataPlane API clients.
package executors

import (
	"context"
	"fmt"
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

// BackendCreate returns an executor for creating backends.
func BackendCreate() func(ctx context.Context, c *client.DataplaneClient, txID string, model *models.Backend, name string) error {
	return func(ctx context.Context, c *client.DataplaneClient, txID string, model *models.Backend, _ string) error {
		clientset := c.Clientset()

		resp, err := client.DispatchCreate(ctx, c, model,
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
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		return client.CheckResponse(resp, "backend creation")
	}
}

// BackendUpdate returns an executor for updating backends.
func BackendUpdate() func(ctx context.Context, c *client.DataplaneClient, txID string, model *models.Backend, name string) error {
	return func(ctx context.Context, c *client.DataplaneClient, txID string, model *models.Backend, name string) error {
		clientset := c.Clientset()

		resp, err := client.DispatchUpdate(ctx, c, name, model,
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
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		return client.CheckResponse(resp, "backend update")
	}
}

// BackendDelete returns an executor for deleting backends.
func BackendDelete() func(ctx context.Context, c *client.DataplaneClient, txID string, _ *models.Backend, name string) error {
	return func(ctx context.Context, c *client.DataplaneClient, txID string, _ *models.Backend, name string) error {
		clientset := c.Clientset()

		resp, err := client.DispatchDelete(ctx, c, name,
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
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		return client.CheckResponse(resp, "backend deletion")
	}
}

// FrontendCreate returns an executor for creating frontends.
func FrontendCreate() func(ctx context.Context, c *client.DataplaneClient, txID string, model *models.Frontend, name string) error {
	return func(ctx context.Context, c *client.DataplaneClient, txID string, model *models.Frontend, _ string) error {
		clientset := c.Clientset()

		resp, err := client.DispatchCreate(ctx, c, model,
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
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		return client.CheckResponse(resp, "frontend creation")
	}
}

// FrontendUpdate returns an executor for updating frontends.
func FrontendUpdate() func(ctx context.Context, c *client.DataplaneClient, txID string, model *models.Frontend, name string) error {
	return func(ctx context.Context, c *client.DataplaneClient, txID string, model *models.Frontend, name string) error {
		clientset := c.Clientset()

		resp, err := client.DispatchUpdate(ctx, c, name, model,
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
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		return client.CheckResponse(resp, "frontend update")
	}
}

// FrontendDelete returns an executor for deleting frontends.
func FrontendDelete() func(ctx context.Context, c *client.DataplaneClient, txID string, _ *models.Frontend, name string) error {
	return func(ctx context.Context, c *client.DataplaneClient, txID string, _ *models.Frontend, name string) error {
		clientset := c.Clientset()

		resp, err := client.DispatchDelete(ctx, c, name,
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
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		return client.CheckResponse(resp, "frontend deletion")
	}
}

// DefaultsCreate returns an executor for creating defaults sections.
func DefaultsCreate() func(ctx context.Context, c *client.DataplaneClient, txID string, model *models.Defaults, name string) error {
	return func(ctx context.Context, c *client.DataplaneClient, txID string, model *models.Defaults, _ string) error {
		clientset := c.Clientset()

		resp, err := client.DispatchCreate(ctx, c, model,
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
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		return client.CheckResponse(resp, "defaults creation")
	}
}

// DefaultsUpdate returns an executor for updating defaults sections.
func DefaultsUpdate() func(ctx context.Context, c *client.DataplaneClient, txID string, model *models.Defaults, name string) error {
	return func(ctx context.Context, c *client.DataplaneClient, txID string, model *models.Defaults, name string) error {
		clientset := c.Clientset()

		resp, err := client.DispatchUpdate(ctx, c, name, model,
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
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		return client.CheckResponse(resp, "defaults update")
	}
}

// DefaultsDelete returns an executor for deleting defaults sections.
func DefaultsDelete() func(ctx context.Context, c *client.DataplaneClient, txID string, _ *models.Defaults, name string) error {
	return func(ctx context.Context, c *client.DataplaneClient, txID string, _ *models.Defaults, name string) error {
		clientset := c.Clientset()

		resp, err := client.DispatchDelete(ctx, c, name,
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
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		return client.CheckResponse(resp, "defaults deletion")
	}
}

// CacheCreate returns an executor for creating cache sections.
func CacheCreate() func(ctx context.Context, c *client.DataplaneClient, txID string, model *models.Cache, name string) error {
	return func(ctx context.Context, c *client.DataplaneClient, txID string, model *models.Cache, _ string) error {
		clientset := c.Clientset()

		resp, err := client.DispatchCreate(ctx, c, model,
			func(m v32.Cache) (*http.Response, error) {
				params := &v32.CreateCacheParams{TransactionId: &txID}
				return clientset.V32().CreateCache(ctx, params, m)
			},
			func(m v31.Cache) (*http.Response, error) {
				params := &v31.CreateCacheParams{TransactionId: &txID}
				return clientset.V31().CreateCache(ctx, params, m)
			},
			func(m v30.Cache) (*http.Response, error) {
				params := &v30.CreateCacheParams{TransactionId: &txID}
				return clientset.V30().CreateCache(ctx, params, m)
			},
			func(m v32ee.Cache) (*http.Response, error) {
				params := &v32ee.CreateCacheParams{TransactionId: &txID}
				return clientset.V32EE().CreateCache(ctx, params, m)
			},
			func(m v31ee.Cache) (*http.Response, error) {
				params := &v31ee.CreateCacheParams{TransactionId: &txID}
				return clientset.V31EE().CreateCache(ctx, params, m)
			},
			func(m v30ee.Cache) (*http.Response, error) {
				params := &v30ee.CreateCacheParams{TransactionId: &txID}
				return clientset.V30EE().CreateCache(ctx, params, m)
			},
		)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		return client.CheckResponse(resp, "cache creation")
	}
}

// CacheUpdate returns an executor for updating cache sections.
func CacheUpdate() func(ctx context.Context, c *client.DataplaneClient, txID string, model *models.Cache, name string) error {
	return func(ctx context.Context, c *client.DataplaneClient, txID string, model *models.Cache, name string) error {
		clientset := c.Clientset()

		resp, err := client.DispatchUpdate(ctx, c, name, model,
			func(n string, m v32.Cache) (*http.Response, error) {
				params := &v32.ReplaceCacheParams{TransactionId: &txID}
				return clientset.V32().ReplaceCache(ctx, n, params, m)
			},
			func(n string, m v31.Cache) (*http.Response, error) {
				params := &v31.ReplaceCacheParams{TransactionId: &txID}
				return clientset.V31().ReplaceCache(ctx, n, params, m)
			},
			func(n string, m v30.Cache) (*http.Response, error) {
				params := &v30.ReplaceCacheParams{TransactionId: &txID}
				return clientset.V30().ReplaceCache(ctx, n, params, m)
			},
			func(n string, m v32ee.Cache) (*http.Response, error) {
				params := &v32ee.ReplaceCacheParams{TransactionId: &txID}
				return clientset.V32EE().ReplaceCache(ctx, n, params, m)
			},
			func(n string, m v31ee.Cache) (*http.Response, error) {
				params := &v31ee.ReplaceCacheParams{TransactionId: &txID}
				return clientset.V31EE().ReplaceCache(ctx, n, params, m)
			},
			func(n string, m v30ee.Cache) (*http.Response, error) {
				params := &v30ee.ReplaceCacheParams{TransactionId: &txID}
				return clientset.V30EE().ReplaceCache(ctx, n, params, m)
			},
		)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		return client.CheckResponse(resp, "cache update")
	}
}

// CacheDelete returns an executor for deleting cache sections.
func CacheDelete() func(ctx context.Context, c *client.DataplaneClient, txID string, _ *models.Cache, name string) error {
	return func(ctx context.Context, c *client.DataplaneClient, txID string, _ *models.Cache, name string) error {
		clientset := c.Clientset()

		resp, err := client.DispatchDelete(ctx, c, name,
			func(n string) (*http.Response, error) {
				params := &v32.DeleteCacheParams{TransactionId: &txID}
				return clientset.V32().DeleteCache(ctx, n, params)
			},
			func(n string) (*http.Response, error) {
				params := &v31.DeleteCacheParams{TransactionId: &txID}
				return clientset.V31().DeleteCache(ctx, n, params)
			},
			func(n string) (*http.Response, error) {
				params := &v30.DeleteCacheParams{TransactionId: &txID}
				return clientset.V30().DeleteCache(ctx, n, params)
			},
			func(n string) (*http.Response, error) {
				params := &v32ee.DeleteCacheParams{TransactionId: &txID}
				return clientset.V32EE().DeleteCache(ctx, n, params)
			},
			func(n string) (*http.Response, error) {
				params := &v31ee.DeleteCacheParams{TransactionId: &txID}
				return clientset.V31EE().DeleteCache(ctx, n, params)
			},
			func(n string) (*http.Response, error) {
				params := &v30ee.DeleteCacheParams{TransactionId: &txID}
				return clientset.V30EE().DeleteCache(ctx, n, params)
			},
		)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		return client.CheckResponse(resp, "cache deletion")
	}
}

// HTTPErrorsSectionCreate returns an executor for creating http-errors sections.
func HTTPErrorsSectionCreate() func(ctx context.Context, c *client.DataplaneClient, txID string, model *models.HTTPErrorsSection, name string) error {
	return func(ctx context.Context, c *client.DataplaneClient, txID string, model *models.HTTPErrorsSection, _ string) error {
		clientset := c.Clientset()

		resp, err := client.DispatchCreate(ctx, c, model,
			func(m v32.HttpErrorsSection) (*http.Response, error) {
				params := &v32.CreateHTTPErrorsSectionParams{TransactionId: &txID}
				return clientset.V32().CreateHTTPErrorsSection(ctx, params, m)
			},
			func(m v31.HttpErrorsSection) (*http.Response, error) {
				params := &v31.CreateHTTPErrorsSectionParams{TransactionId: &txID}
				return clientset.V31().CreateHTTPErrorsSection(ctx, params, m)
			},
			func(m v30.HttpErrorsSection) (*http.Response, error) {
				params := &v30.CreateHTTPErrorsSectionParams{TransactionId: &txID}
				return clientset.V30().CreateHTTPErrorsSection(ctx, params, m)
			},
			func(m v32ee.HttpErrorsSection) (*http.Response, error) {
				params := &v32ee.CreateHTTPErrorsSectionParams{TransactionId: &txID}
				return clientset.V32EE().CreateHTTPErrorsSection(ctx, params, m)
			},
			func(m v31ee.HttpErrorsSection) (*http.Response, error) {
				params := &v31ee.CreateHTTPErrorsSectionParams{TransactionId: &txID}
				return clientset.V31EE().CreateHTTPErrorsSection(ctx, params, m)
			},
			func(m v30ee.HttpErrorsSection) (*http.Response, error) {
				params := &v30ee.CreateHTTPErrorsSectionParams{TransactionId: &txID}
				return clientset.V30EE().CreateHTTPErrorsSection(ctx, params, m)
			},
		)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		return client.CheckResponse(resp, "http-errors section creation")
	}
}

// HTTPErrorsSectionUpdate returns an executor for updating http-errors sections.
func HTTPErrorsSectionUpdate() func(ctx context.Context, c *client.DataplaneClient, txID string, model *models.HTTPErrorsSection, name string) error {
	return func(ctx context.Context, c *client.DataplaneClient, txID string, model *models.HTTPErrorsSection, name string) error {
		clientset := c.Clientset()

		resp, err := client.DispatchUpdate(ctx, c, name, model,
			func(n string, m v32.HttpErrorsSection) (*http.Response, error) {
				params := &v32.ReplaceHTTPErrorsSectionParams{TransactionId: &txID}
				return clientset.V32().ReplaceHTTPErrorsSection(ctx, n, params, m)
			},
			func(n string, m v31.HttpErrorsSection) (*http.Response, error) {
				params := &v31.ReplaceHTTPErrorsSectionParams{TransactionId: &txID}
				return clientset.V31().ReplaceHTTPErrorsSection(ctx, n, params, m)
			},
			func(n string, m v30.HttpErrorsSection) (*http.Response, error) {
				params := &v30.ReplaceHTTPErrorsSectionParams{TransactionId: &txID}
				return clientset.V30().ReplaceHTTPErrorsSection(ctx, n, params, m)
			},
			func(n string, m v32ee.HttpErrorsSection) (*http.Response, error) {
				params := &v32ee.ReplaceHTTPErrorsSectionParams{TransactionId: &txID}
				return clientset.V32EE().ReplaceHTTPErrorsSection(ctx, n, params, m)
			},
			func(n string, m v31ee.HttpErrorsSection) (*http.Response, error) {
				params := &v31ee.ReplaceHTTPErrorsSectionParams{TransactionId: &txID}
				return clientset.V31EE().ReplaceHTTPErrorsSection(ctx, n, params, m)
			},
			func(n string, m v30ee.HttpErrorsSection) (*http.Response, error) {
				params := &v30ee.ReplaceHTTPErrorsSectionParams{TransactionId: &txID}
				return clientset.V30EE().ReplaceHTTPErrorsSection(ctx, n, params, m)
			},
		)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		return client.CheckResponse(resp, "http-errors section update")
	}
}

// HTTPErrorsSectionDelete returns an executor for deleting http-errors sections.
func HTTPErrorsSectionDelete() func(ctx context.Context, c *client.DataplaneClient, txID string, _ *models.HTTPErrorsSection, name string) error {
	return func(ctx context.Context, c *client.DataplaneClient, txID string, _ *models.HTTPErrorsSection, name string) error {
		clientset := c.Clientset()

		resp, err := client.DispatchDelete(ctx, c, name,
			func(n string) (*http.Response, error) {
				params := &v32.DeleteHTTPErrorsSectionParams{TransactionId: &txID}
				return clientset.V32().DeleteHTTPErrorsSection(ctx, n, params)
			},
			func(n string) (*http.Response, error) {
				params := &v31.DeleteHTTPErrorsSectionParams{TransactionId: &txID}
				return clientset.V31().DeleteHTTPErrorsSection(ctx, n, params)
			},
			func(n string) (*http.Response, error) {
				params := &v30.DeleteHTTPErrorsSectionParams{TransactionId: &txID}
				return clientset.V30().DeleteHTTPErrorsSection(ctx, n, params)
			},
			func(n string) (*http.Response, error) {
				params := &v32ee.DeleteHTTPErrorsSectionParams{TransactionId: &txID}
				return clientset.V32EE().DeleteHTTPErrorsSection(ctx, n, params)
			},
			func(n string) (*http.Response, error) {
				params := &v31ee.DeleteHTTPErrorsSectionParams{TransactionId: &txID}
				return clientset.V31EE().DeleteHTTPErrorsSection(ctx, n, params)
			},
			func(n string) (*http.Response, error) {
				params := &v30ee.DeleteHTTPErrorsSectionParams{TransactionId: &txID}
				return clientset.V30EE().DeleteHTTPErrorsSection(ctx, n, params)
			},
		)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		return client.CheckResponse(resp, "http-errors section deletion")
	}
}

// LogForwardCreate returns an executor for creating log-forward sections.
func LogForwardCreate() func(ctx context.Context, c *client.DataplaneClient, txID string, model *models.LogForward, name string) error {
	return func(ctx context.Context, c *client.DataplaneClient, txID string, model *models.LogForward, _ string) error {
		clientset := c.Clientset()

		resp, err := client.DispatchCreate(ctx, c, model,
			func(m v32.LogForward) (*http.Response, error) {
				params := &v32.CreateLogForwardParams{TransactionId: &txID}
				return clientset.V32().CreateLogForward(ctx, params, m)
			},
			func(m v31.LogForward) (*http.Response, error) {
				params := &v31.CreateLogForwardParams{TransactionId: &txID}
				return clientset.V31().CreateLogForward(ctx, params, m)
			},
			func(m v30.LogForward) (*http.Response, error) {
				params := &v30.CreateLogForwardParams{TransactionId: &txID}
				return clientset.V30().CreateLogForward(ctx, params, m)
			},
			func(m v32ee.LogForward) (*http.Response, error) {
				params := &v32ee.CreateLogForwardParams{TransactionId: &txID}
				return clientset.V32EE().CreateLogForward(ctx, params, m)
			},
			func(m v31ee.LogForward) (*http.Response, error) {
				params := &v31ee.CreateLogForwardParams{TransactionId: &txID}
				return clientset.V31EE().CreateLogForward(ctx, params, m)
			},
			func(m v30ee.LogForward) (*http.Response, error) {
				params := &v30ee.CreateLogForwardParams{TransactionId: &txID}
				return clientset.V30EE().CreateLogForward(ctx, params, m)
			},
		)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		return client.CheckResponse(resp, "log-forward creation")
	}
}

// LogForwardUpdate returns an executor for updating log-forward sections.
func LogForwardUpdate() func(ctx context.Context, c *client.DataplaneClient, txID string, model *models.LogForward, name string) error {
	return func(ctx context.Context, c *client.DataplaneClient, txID string, model *models.LogForward, name string) error {
		clientset := c.Clientset()

		resp, err := client.DispatchUpdate(ctx, c, name, model,
			func(n string, m v32.LogForward) (*http.Response, error) {
				params := &v32.ReplaceLogForwardParams{TransactionId: &txID}
				return clientset.V32().ReplaceLogForward(ctx, n, params, m)
			},
			func(n string, m v31.LogForward) (*http.Response, error) {
				params := &v31.ReplaceLogForwardParams{TransactionId: &txID}
				return clientset.V31().ReplaceLogForward(ctx, n, params, m)
			},
			func(n string, m v30.LogForward) (*http.Response, error) {
				params := &v30.ReplaceLogForwardParams{TransactionId: &txID}
				return clientset.V30().ReplaceLogForward(ctx, n, params, m)
			},
			func(n string, m v32ee.LogForward) (*http.Response, error) {
				params := &v32ee.ReplaceLogForwardParams{TransactionId: &txID}
				return clientset.V32EE().ReplaceLogForward(ctx, n, params, m)
			},
			func(n string, m v31ee.LogForward) (*http.Response, error) {
				params := &v31ee.ReplaceLogForwardParams{TransactionId: &txID}
				return clientset.V31EE().ReplaceLogForward(ctx, n, params, m)
			},
			func(n string, m v30ee.LogForward) (*http.Response, error) {
				params := &v30ee.ReplaceLogForwardParams{TransactionId: &txID}
				return clientset.V30EE().ReplaceLogForward(ctx, n, params, m)
			},
		)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		return client.CheckResponse(resp, "log-forward update")
	}
}

// LogForwardDelete returns an executor for deleting log-forward sections.
func LogForwardDelete() func(ctx context.Context, c *client.DataplaneClient, txID string, _ *models.LogForward, name string) error {
	return func(ctx context.Context, c *client.DataplaneClient, txID string, _ *models.LogForward, name string) error {
		clientset := c.Clientset()

		resp, err := client.DispatchDelete(ctx, c, name,
			func(n string) (*http.Response, error) {
				params := &v32.DeleteLogForwardParams{TransactionId: &txID}
				return clientset.V32().DeleteLogForward(ctx, n, params)
			},
			func(n string) (*http.Response, error) {
				params := &v31.DeleteLogForwardParams{TransactionId: &txID}
				return clientset.V31().DeleteLogForward(ctx, n, params)
			},
			func(n string) (*http.Response, error) {
				params := &v30.DeleteLogForwardParams{TransactionId: &txID}
				return clientset.V30().DeleteLogForward(ctx, n, params)
			},
			func(n string) (*http.Response, error) {
				params := &v32ee.DeleteLogForwardParams{TransactionId: &txID}
				return clientset.V32EE().DeleteLogForward(ctx, n, params)
			},
			func(n string) (*http.Response, error) {
				params := &v31ee.DeleteLogForwardParams{TransactionId: &txID}
				return clientset.V31EE().DeleteLogForward(ctx, n, params)
			},
			func(n string) (*http.Response, error) {
				params := &v30ee.DeleteLogForwardParams{TransactionId: &txID}
				return clientset.V30EE().DeleteLogForward(ctx, n, params)
			},
		)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		return client.CheckResponse(resp, "log-forward deletion")
	}
}

// MailersSectionCreate returns an executor for creating mailers sections.
func MailersSectionCreate() func(ctx context.Context, c *client.DataplaneClient, txID string, model *models.MailersSection, name string) error {
	return func(ctx context.Context, c *client.DataplaneClient, txID string, model *models.MailersSection, _ string) error {
		clientset := c.Clientset()

		resp, err := client.DispatchCreate(ctx, c, model,
			func(m v32.MailersSection) (*http.Response, error) {
				params := &v32.CreateMailersSectionParams{TransactionId: &txID}
				return clientset.V32().CreateMailersSection(ctx, params, m)
			},
			func(m v31.MailersSection) (*http.Response, error) {
				params := &v31.CreateMailersSectionParams{TransactionId: &txID}
				return clientset.V31().CreateMailersSection(ctx, params, m)
			},
			func(m v30.MailersSection) (*http.Response, error) {
				params := &v30.CreateMailersSectionParams{TransactionId: &txID}
				return clientset.V30().CreateMailersSection(ctx, params, m)
			},
			func(m v32ee.MailersSection) (*http.Response, error) {
				params := &v32ee.CreateMailersSectionParams{TransactionId: &txID}
				return clientset.V32EE().CreateMailersSection(ctx, params, m)
			},
			func(m v31ee.MailersSection) (*http.Response, error) {
				params := &v31ee.CreateMailersSectionParams{TransactionId: &txID}
				return clientset.V31EE().CreateMailersSection(ctx, params, m)
			},
			func(m v30ee.MailersSection) (*http.Response, error) {
				params := &v30ee.CreateMailersSectionParams{TransactionId: &txID}
				return clientset.V30EE().CreateMailersSection(ctx, params, m)
			},
		)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		return client.CheckResponse(resp, "mailers section creation")
	}
}

// MailersSectionUpdate returns an executor for updating mailers sections.
func MailersSectionUpdate() func(ctx context.Context, c *client.DataplaneClient, txID string, model *models.MailersSection, name string) error {
	return func(ctx context.Context, c *client.DataplaneClient, txID string, model *models.MailersSection, name string) error {
		clientset := c.Clientset()

		resp, err := client.DispatchUpdate(ctx, c, name, model,
			func(n string, m v32.MailersSection) (*http.Response, error) {
				params := &v32.EditMailersSectionParams{TransactionId: &txID}
				return clientset.V32().EditMailersSection(ctx, n, params, m)
			},
			func(n string, m v31.MailersSection) (*http.Response, error) {
				params := &v31.EditMailersSectionParams{TransactionId: &txID}
				return clientset.V31().EditMailersSection(ctx, n, params, m)
			},
			func(n string, m v30.MailersSection) (*http.Response, error) {
				params := &v30.EditMailersSectionParams{TransactionId: &txID}
				return clientset.V30().EditMailersSection(ctx, n, params, m)
			},
			func(n string, m v32ee.MailersSection) (*http.Response, error) {
				params := &v32ee.EditMailersSectionParams{TransactionId: &txID}
				return clientset.V32EE().EditMailersSection(ctx, n, params, m)
			},
			func(n string, m v31ee.MailersSection) (*http.Response, error) {
				params := &v31ee.EditMailersSectionParams{TransactionId: &txID}
				return clientset.V31EE().EditMailersSection(ctx, n, params, m)
			},
			func(n string, m v30ee.MailersSection) (*http.Response, error) {
				params := &v30ee.EditMailersSectionParams{TransactionId: &txID}
				return clientset.V30EE().EditMailersSection(ctx, n, params, m)
			},
		)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		return client.CheckResponse(resp, "mailers section update")
	}
}

// MailersSectionDelete returns an executor for deleting mailers sections.
func MailersSectionDelete() func(ctx context.Context, c *client.DataplaneClient, txID string, _ *models.MailersSection, name string) error {
	return func(ctx context.Context, c *client.DataplaneClient, txID string, _ *models.MailersSection, name string) error {
		clientset := c.Clientset()

		resp, err := client.DispatchDelete(ctx, c, name,
			func(n string) (*http.Response, error) {
				params := &v32.DeleteMailersSectionParams{TransactionId: &txID}
				return clientset.V32().DeleteMailersSection(ctx, n, params)
			},
			func(n string) (*http.Response, error) {
				params := &v31.DeleteMailersSectionParams{TransactionId: &txID}
				return clientset.V31().DeleteMailersSection(ctx, n, params)
			},
			func(n string) (*http.Response, error) {
				params := &v30.DeleteMailersSectionParams{TransactionId: &txID}
				return clientset.V30().DeleteMailersSection(ctx, n, params)
			},
			func(n string) (*http.Response, error) {
				params := &v32ee.DeleteMailersSectionParams{TransactionId: &txID}
				return clientset.V32EE().DeleteMailersSection(ctx, n, params)
			},
			func(n string) (*http.Response, error) {
				params := &v31ee.DeleteMailersSectionParams{TransactionId: &txID}
				return clientset.V31EE().DeleteMailersSection(ctx, n, params)
			},
			func(n string) (*http.Response, error) {
				params := &v30ee.DeleteMailersSectionParams{TransactionId: &txID}
				return clientset.V30EE().DeleteMailersSection(ctx, n, params)
			},
		)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		return client.CheckResponse(resp, "mailers section deletion")
	}
}

// PeerSectionCreate returns an executor for creating peer sections.
func PeerSectionCreate() func(ctx context.Context, c *client.DataplaneClient, txID string, model *models.PeerSection, name string) error {
	return func(ctx context.Context, c *client.DataplaneClient, txID string, model *models.PeerSection, _ string) error {
		clientset := c.Clientset()

		resp, err := client.DispatchCreate(ctx, c, model,
			func(m v32.PeerSection) (*http.Response, error) {
				params := &v32.CreatePeerParams{TransactionId: &txID}
				return clientset.V32().CreatePeer(ctx, params, m)
			},
			func(m v31.PeerSection) (*http.Response, error) {
				params := &v31.CreatePeerParams{TransactionId: &txID}
				return clientset.V31().CreatePeer(ctx, params, m)
			},
			func(m v30.PeerSection) (*http.Response, error) {
				params := &v30.CreatePeerParams{TransactionId: &txID}
				return clientset.V30().CreatePeer(ctx, params, m)
			},
			func(m v32ee.PeerSection) (*http.Response, error) {
				params := &v32ee.CreatePeerParams{TransactionId: &txID}
				return clientset.V32EE().CreatePeer(ctx, params, m)
			},
			func(m v31ee.PeerSection) (*http.Response, error) {
				params := &v31ee.CreatePeerParams{TransactionId: &txID}
				return clientset.V31EE().CreatePeer(ctx, params, m)
			},
			func(m v30ee.PeerSection) (*http.Response, error) {
				params := &v30ee.CreatePeerParams{TransactionId: &txID}
				return clientset.V30EE().CreatePeer(ctx, params, m)
			},
		)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		return client.CheckResponse(resp, "peer section creation")
	}
}

// PeerSectionUpdate returns an executor for updating peer sections.
// Note: The HAProxy Dataplane API does not support updating peer sections directly.
func PeerSectionUpdate() func(ctx context.Context, c *client.DataplaneClient, txID string, model *models.PeerSection, name string) error {
	return func(_ context.Context, _ *client.DataplaneClient, _ string, _ *models.PeerSection, name string) error {
		return fmt.Errorf("peer section updates are not supported by HAProxy Dataplane API (section: %s)", name)
	}
}

// PeerSectionDelete returns an executor for deleting peer sections.
func PeerSectionDelete() func(ctx context.Context, c *client.DataplaneClient, txID string, _ *models.PeerSection, name string) error {
	return func(ctx context.Context, c *client.DataplaneClient, txID string, _ *models.PeerSection, name string) error {
		clientset := c.Clientset()

		resp, err := client.DispatchDelete(ctx, c, name,
			func(n string) (*http.Response, error) {
				params := &v32.DeletePeerParams{TransactionId: &txID}
				return clientset.V32().DeletePeer(ctx, n, params)
			},
			func(n string) (*http.Response, error) {
				params := &v31.DeletePeerParams{TransactionId: &txID}
				return clientset.V31().DeletePeer(ctx, n, params)
			},
			func(n string) (*http.Response, error) {
				params := &v30.DeletePeerParams{TransactionId: &txID}
				return clientset.V30().DeletePeer(ctx, n, params)
			},
			func(n string) (*http.Response, error) {
				params := &v32ee.DeletePeerParams{TransactionId: &txID}
				return clientset.V32EE().DeletePeer(ctx, n, params)
			},
			func(n string) (*http.Response, error) {
				params := &v31ee.DeletePeerParams{TransactionId: &txID}
				return clientset.V31EE().DeletePeer(ctx, n, params)
			},
			func(n string) (*http.Response, error) {
				params := &v30ee.DeletePeerParams{TransactionId: &txID}
				return clientset.V30EE().DeletePeer(ctx, n, params)
			},
		)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		return client.CheckResponse(resp, "peer section deletion")
	}
}

// ProgramCreate returns an executor for creating program sections.
func ProgramCreate() func(ctx context.Context, c *client.DataplaneClient, txID string, model *models.Program, name string) error {
	return func(ctx context.Context, c *client.DataplaneClient, txID string, model *models.Program, _ string) error {
		clientset := c.Clientset()

		resp, err := client.DispatchCreate(ctx, c, model,
			func(m v32.Program) (*http.Response, error) {
				params := &v32.CreateProgramParams{TransactionId: &txID}
				return clientset.V32().CreateProgram(ctx, params, m)
			},
			func(m v31.Program) (*http.Response, error) {
				params := &v31.CreateProgramParams{TransactionId: &txID}
				return clientset.V31().CreateProgram(ctx, params, m)
			},
			func(m v30.Program) (*http.Response, error) {
				params := &v30.CreateProgramParams{TransactionId: &txID}
				return clientset.V30().CreateProgram(ctx, params, m)
			},
			func(m v32ee.Program) (*http.Response, error) {
				params := &v32ee.CreateProgramParams{TransactionId: &txID}
				return clientset.V32EE().CreateProgram(ctx, params, m)
			},
			func(m v31ee.Program) (*http.Response, error) {
				params := &v31ee.CreateProgramParams{TransactionId: &txID}
				return clientset.V31EE().CreateProgram(ctx, params, m)
			},
			func(m v30ee.Program) (*http.Response, error) {
				params := &v30ee.CreateProgramParams{TransactionId: &txID}
				return clientset.V30EE().CreateProgram(ctx, params, m)
			},
		)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		return client.CheckResponse(resp, "program creation")
	}
}

// ProgramUpdate returns an executor for updating program sections.
func ProgramUpdate() func(ctx context.Context, c *client.DataplaneClient, txID string, model *models.Program, name string) error {
	return func(ctx context.Context, c *client.DataplaneClient, txID string, model *models.Program, name string) error {
		clientset := c.Clientset()

		resp, err := client.DispatchUpdate(ctx, c, name, model,
			func(n string, m v32.Program) (*http.Response, error) {
				params := &v32.ReplaceProgramParams{TransactionId: &txID}
				return clientset.V32().ReplaceProgram(ctx, n, params, m)
			},
			func(n string, m v31.Program) (*http.Response, error) {
				params := &v31.ReplaceProgramParams{TransactionId: &txID}
				return clientset.V31().ReplaceProgram(ctx, n, params, m)
			},
			func(n string, m v30.Program) (*http.Response, error) {
				params := &v30.ReplaceProgramParams{TransactionId: &txID}
				return clientset.V30().ReplaceProgram(ctx, n, params, m)
			},
			func(n string, m v32ee.Program) (*http.Response, error) {
				params := &v32ee.ReplaceProgramParams{TransactionId: &txID}
				return clientset.V32EE().ReplaceProgram(ctx, n, params, m)
			},
			func(n string, m v31ee.Program) (*http.Response, error) {
				params := &v31ee.ReplaceProgramParams{TransactionId: &txID}
				return clientset.V31EE().ReplaceProgram(ctx, n, params, m)
			},
			func(n string, m v30ee.Program) (*http.Response, error) {
				params := &v30ee.ReplaceProgramParams{TransactionId: &txID}
				return clientset.V30EE().ReplaceProgram(ctx, n, params, m)
			},
		)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		return client.CheckResponse(resp, "program update")
	}
}

// ProgramDelete returns an executor for deleting program sections.
func ProgramDelete() func(ctx context.Context, c *client.DataplaneClient, txID string, _ *models.Program, name string) error {
	return func(ctx context.Context, c *client.DataplaneClient, txID string, _ *models.Program, name string) error {
		clientset := c.Clientset()

		resp, err := client.DispatchDelete(ctx, c, name,
			func(n string) (*http.Response, error) {
				params := &v32.DeleteProgramParams{TransactionId: &txID}
				return clientset.V32().DeleteProgram(ctx, n, params)
			},
			func(n string) (*http.Response, error) {
				params := &v31.DeleteProgramParams{TransactionId: &txID}
				return clientset.V31().DeleteProgram(ctx, n, params)
			},
			func(n string) (*http.Response, error) {
				params := &v30.DeleteProgramParams{TransactionId: &txID}
				return clientset.V30().DeleteProgram(ctx, n, params)
			},
			func(n string) (*http.Response, error) {
				params := &v32ee.DeleteProgramParams{TransactionId: &txID}
				return clientset.V32EE().DeleteProgram(ctx, n, params)
			},
			func(n string) (*http.Response, error) {
				params := &v31ee.DeleteProgramParams{TransactionId: &txID}
				return clientset.V31EE().DeleteProgram(ctx, n, params)
			},
			func(n string) (*http.Response, error) {
				params := &v30ee.DeleteProgramParams{TransactionId: &txID}
				return clientset.V30EE().DeleteProgram(ctx, n, params)
			},
		)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		return client.CheckResponse(resp, "program deletion")
	}
}

// ResolverCreate returns an executor for creating resolver sections.
func ResolverCreate() func(ctx context.Context, c *client.DataplaneClient, txID string, model *models.Resolver, name string) error {
	return func(ctx context.Context, c *client.DataplaneClient, txID string, model *models.Resolver, _ string) error {
		clientset := c.Clientset()

		resp, err := client.DispatchCreate(ctx, c, model,
			func(m v32.Resolver) (*http.Response, error) {
				params := &v32.CreateResolverParams{TransactionId: &txID}
				return clientset.V32().CreateResolver(ctx, params, m)
			},
			func(m v31.Resolver) (*http.Response, error) {
				params := &v31.CreateResolverParams{TransactionId: &txID}
				return clientset.V31().CreateResolver(ctx, params, m)
			},
			func(m v30.Resolver) (*http.Response, error) {
				params := &v30.CreateResolverParams{TransactionId: &txID}
				return clientset.V30().CreateResolver(ctx, params, m)
			},
			func(m v32ee.Resolver) (*http.Response, error) {
				params := &v32ee.CreateResolverParams{TransactionId: &txID}
				return clientset.V32EE().CreateResolver(ctx, params, m)
			},
			func(m v31ee.Resolver) (*http.Response, error) {
				params := &v31ee.CreateResolverParams{TransactionId: &txID}
				return clientset.V31EE().CreateResolver(ctx, params, m)
			},
			func(m v30ee.Resolver) (*http.Response, error) {
				params := &v30ee.CreateResolverParams{TransactionId: &txID}
				return clientset.V30EE().CreateResolver(ctx, params, m)
			},
		)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		return client.CheckResponse(resp, "resolver creation")
	}
}

// ResolverUpdate returns an executor for updating resolver sections.
func ResolverUpdate() func(ctx context.Context, c *client.DataplaneClient, txID string, model *models.Resolver, name string) error {
	return func(ctx context.Context, c *client.DataplaneClient, txID string, model *models.Resolver, name string) error {
		clientset := c.Clientset()

		resp, err := client.DispatchUpdate(ctx, c, name, model,
			func(n string, m v32.Resolver) (*http.Response, error) {
				params := &v32.ReplaceResolverParams{TransactionId: &txID}
				return clientset.V32().ReplaceResolver(ctx, n, params, m)
			},
			func(n string, m v31.Resolver) (*http.Response, error) {
				params := &v31.ReplaceResolverParams{TransactionId: &txID}
				return clientset.V31().ReplaceResolver(ctx, n, params, m)
			},
			func(n string, m v30.Resolver) (*http.Response, error) {
				params := &v30.ReplaceResolverParams{TransactionId: &txID}
				return clientset.V30().ReplaceResolver(ctx, n, params, m)
			},
			func(n string, m v32ee.Resolver) (*http.Response, error) {
				params := &v32ee.ReplaceResolverParams{TransactionId: &txID}
				return clientset.V32EE().ReplaceResolver(ctx, n, params, m)
			},
			func(n string, m v31ee.Resolver) (*http.Response, error) {
				params := &v31ee.ReplaceResolverParams{TransactionId: &txID}
				return clientset.V31EE().ReplaceResolver(ctx, n, params, m)
			},
			func(n string, m v30ee.Resolver) (*http.Response, error) {
				params := &v30ee.ReplaceResolverParams{TransactionId: &txID}
				return clientset.V30EE().ReplaceResolver(ctx, n, params, m)
			},
		)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		return client.CheckResponse(resp, "resolver update")
	}
}

// ResolverDelete returns an executor for deleting resolver sections.
func ResolverDelete() func(ctx context.Context, c *client.DataplaneClient, txID string, _ *models.Resolver, name string) error {
	return func(ctx context.Context, c *client.DataplaneClient, txID string, _ *models.Resolver, name string) error {
		clientset := c.Clientset()

		resp, err := client.DispatchDelete(ctx, c, name,
			func(n string) (*http.Response, error) {
				params := &v32.DeleteResolverParams{TransactionId: &txID}
				return clientset.V32().DeleteResolver(ctx, n, params)
			},
			func(n string) (*http.Response, error) {
				params := &v31.DeleteResolverParams{TransactionId: &txID}
				return clientset.V31().DeleteResolver(ctx, n, params)
			},
			func(n string) (*http.Response, error) {
				params := &v30.DeleteResolverParams{TransactionId: &txID}
				return clientset.V30().DeleteResolver(ctx, n, params)
			},
			func(n string) (*http.Response, error) {
				params := &v32ee.DeleteResolverParams{TransactionId: &txID}
				return clientset.V32EE().DeleteResolver(ctx, n, params)
			},
			func(n string) (*http.Response, error) {
				params := &v31ee.DeleteResolverParams{TransactionId: &txID}
				return clientset.V31EE().DeleteResolver(ctx, n, params)
			},
			func(n string) (*http.Response, error) {
				params := &v30ee.DeleteResolverParams{TransactionId: &txID}
				return clientset.V30EE().DeleteResolver(ctx, n, params)
			},
		)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		return client.CheckResponse(resp, "resolver deletion")
	}
}

// RingCreate returns an executor for creating ring sections.
func RingCreate() func(ctx context.Context, c *client.DataplaneClient, txID string, model *models.Ring, name string) error {
	return func(ctx context.Context, c *client.DataplaneClient, txID string, model *models.Ring, _ string) error {
		clientset := c.Clientset()

		resp, err := client.DispatchCreate(ctx, c, model,
			func(m v32.Ring) (*http.Response, error) {
				params := &v32.CreateRingParams{TransactionId: &txID}
				return clientset.V32().CreateRing(ctx, params, m)
			},
			func(m v31.Ring) (*http.Response, error) {
				params := &v31.CreateRingParams{TransactionId: &txID}
				return clientset.V31().CreateRing(ctx, params, m)
			},
			func(m v30.Ring) (*http.Response, error) {
				params := &v30.CreateRingParams{TransactionId: &txID}
				return clientset.V30().CreateRing(ctx, params, m)
			},
			func(m v32ee.Ring) (*http.Response, error) {
				params := &v32ee.CreateRingParams{TransactionId: &txID}
				return clientset.V32EE().CreateRing(ctx, params, m)
			},
			func(m v31ee.Ring) (*http.Response, error) {
				params := &v31ee.CreateRingParams{TransactionId: &txID}
				return clientset.V31EE().CreateRing(ctx, params, m)
			},
			func(m v30ee.Ring) (*http.Response, error) {
				params := &v30ee.CreateRingParams{TransactionId: &txID}
				return clientset.V30EE().CreateRing(ctx, params, m)
			},
		)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		return client.CheckResponse(resp, "ring creation")
	}
}

// RingUpdate returns an executor for updating ring sections.
func RingUpdate() func(ctx context.Context, c *client.DataplaneClient, txID string, model *models.Ring, name string) error {
	return func(ctx context.Context, c *client.DataplaneClient, txID string, model *models.Ring, name string) error {
		clientset := c.Clientset()

		resp, err := client.DispatchUpdate(ctx, c, name, model,
			func(n string, m v32.Ring) (*http.Response, error) {
				params := &v32.ReplaceRingParams{TransactionId: &txID}
				return clientset.V32().ReplaceRing(ctx, n, params, m)
			},
			func(n string, m v31.Ring) (*http.Response, error) {
				params := &v31.ReplaceRingParams{TransactionId: &txID}
				return clientset.V31().ReplaceRing(ctx, n, params, m)
			},
			func(n string, m v30.Ring) (*http.Response, error) {
				params := &v30.ReplaceRingParams{TransactionId: &txID}
				return clientset.V30().ReplaceRing(ctx, n, params, m)
			},
			func(n string, m v32ee.Ring) (*http.Response, error) {
				params := &v32ee.ReplaceRingParams{TransactionId: &txID}
				return clientset.V32EE().ReplaceRing(ctx, n, params, m)
			},
			func(n string, m v31ee.Ring) (*http.Response, error) {
				params := &v31ee.ReplaceRingParams{TransactionId: &txID}
				return clientset.V31EE().ReplaceRing(ctx, n, params, m)
			},
			func(n string, m v30ee.Ring) (*http.Response, error) {
				params := &v30ee.ReplaceRingParams{TransactionId: &txID}
				return clientset.V30EE().ReplaceRing(ctx, n, params, m)
			},
		)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		return client.CheckResponse(resp, "ring update")
	}
}

// RingDelete returns an executor for deleting ring sections.
func RingDelete() func(ctx context.Context, c *client.DataplaneClient, txID string, _ *models.Ring, name string) error {
	return func(ctx context.Context, c *client.DataplaneClient, txID string, _ *models.Ring, name string) error {
		clientset := c.Clientset()

		resp, err := client.DispatchDelete(ctx, c, name,
			func(n string) (*http.Response, error) {
				params := &v32.DeleteRingParams{TransactionId: &txID}
				return clientset.V32().DeleteRing(ctx, n, params)
			},
			func(n string) (*http.Response, error) {
				params := &v31.DeleteRingParams{TransactionId: &txID}
				return clientset.V31().DeleteRing(ctx, n, params)
			},
			func(n string) (*http.Response, error) {
				params := &v30.DeleteRingParams{TransactionId: &txID}
				return clientset.V30().DeleteRing(ctx, n, params)
			},
			func(n string) (*http.Response, error) {
				params := &v32ee.DeleteRingParams{TransactionId: &txID}
				return clientset.V32EE().DeleteRing(ctx, n, params)
			},
			func(n string) (*http.Response, error) {
				params := &v31ee.DeleteRingParams{TransactionId: &txID}
				return clientset.V31EE().DeleteRing(ctx, n, params)
			},
			func(n string) (*http.Response, error) {
				params := &v30ee.DeleteRingParams{TransactionId: &txID}
				return clientset.V30EE().DeleteRing(ctx, n, params)
			},
		)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		return client.CheckResponse(resp, "ring deletion")
	}
}

// CrtStoreCreate returns an executor for creating crt-store sections.
func CrtStoreCreate() func(ctx context.Context, c *client.DataplaneClient, txID string, model *models.CrtStore, name string) error {
	return func(ctx context.Context, c *client.DataplaneClient, txID string, model *models.CrtStore, _ string) error {
		clientset := c.Clientset()

		resp, err := client.DispatchCreate(ctx, c, model,
			func(m v32.CrtStore) (*http.Response, error) {
				params := &v32.CreateCrtStoreParams{TransactionId: &txID}
				return clientset.V32().CreateCrtStore(ctx, params, m)
			},
			func(m v31.CrtStore) (*http.Response, error) {
				params := &v31.CreateCrtStoreParams{TransactionId: &txID}
				return clientset.V31().CreateCrtStore(ctx, params, m)
			},
			func(m v30.CrtStore) (*http.Response, error) {
				params := &v30.CreateCrtStoreParams{TransactionId: &txID}
				return clientset.V30().CreateCrtStore(ctx, params, m)
			},
			func(m v32ee.CrtStore) (*http.Response, error) {
				params := &v32ee.CreateCrtStoreParams{TransactionId: &txID}
				return clientset.V32EE().CreateCrtStore(ctx, params, m)
			},
			func(m v31ee.CrtStore) (*http.Response, error) {
				params := &v31ee.CreateCrtStoreParams{TransactionId: &txID}
				return clientset.V31EE().CreateCrtStore(ctx, params, m)
			},
			func(m v30ee.CrtStore) (*http.Response, error) {
				params := &v30ee.CreateCrtStoreParams{TransactionId: &txID}
				return clientset.V30EE().CreateCrtStore(ctx, params, m)
			},
		)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		return client.CheckResponse(resp, "crt-store creation")
	}
}

// CrtStoreUpdate returns an executor for updating crt-store sections.
func CrtStoreUpdate() func(ctx context.Context, c *client.DataplaneClient, txID string, model *models.CrtStore, name string) error {
	return func(ctx context.Context, c *client.DataplaneClient, txID string, model *models.CrtStore, name string) error {
		clientset := c.Clientset()

		resp, err := client.DispatchUpdate(ctx, c, name, model,
			func(n string, m v32.CrtStore) (*http.Response, error) {
				params := &v32.EditCrtStoreParams{TransactionId: &txID}
				return clientset.V32().EditCrtStore(ctx, n, params, m)
			},
			func(n string, m v31.CrtStore) (*http.Response, error) {
				params := &v31.EditCrtStoreParams{TransactionId: &txID}
				return clientset.V31().EditCrtStore(ctx, n, params, m)
			},
			func(n string, m v30.CrtStore) (*http.Response, error) {
				params := &v30.EditCrtStoreParams{TransactionId: &txID}
				return clientset.V30().EditCrtStore(ctx, n, params, m)
			},
			func(n string, m v32ee.CrtStore) (*http.Response, error) {
				params := &v32ee.EditCrtStoreParams{TransactionId: &txID}
				return clientset.V32EE().EditCrtStore(ctx, n, params, m)
			},
			func(n string, m v31ee.CrtStore) (*http.Response, error) {
				params := &v31ee.EditCrtStoreParams{TransactionId: &txID}
				return clientset.V31EE().EditCrtStore(ctx, n, params, m)
			},
			func(n string, m v30ee.CrtStore) (*http.Response, error) {
				params := &v30ee.EditCrtStoreParams{TransactionId: &txID}
				return clientset.V30EE().EditCrtStore(ctx, n, params, m)
			},
		)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		return client.CheckResponse(resp, "crt-store update")
	}
}

// CrtStoreDelete returns an executor for deleting crt-store sections.
func CrtStoreDelete() func(ctx context.Context, c *client.DataplaneClient, txID string, _ *models.CrtStore, name string) error {
	return func(ctx context.Context, c *client.DataplaneClient, txID string, _ *models.CrtStore, name string) error {
		clientset := c.Clientset()

		resp, err := client.DispatchDelete(ctx, c, name,
			func(n string) (*http.Response, error) {
				params := &v32.DeleteCrtStoreParams{TransactionId: &txID}
				return clientset.V32().DeleteCrtStore(ctx, n, params)
			},
			func(n string) (*http.Response, error) {
				params := &v31.DeleteCrtStoreParams{TransactionId: &txID}
				return clientset.V31().DeleteCrtStore(ctx, n, params)
			},
			func(n string) (*http.Response, error) {
				params := &v30.DeleteCrtStoreParams{TransactionId: &txID}
				return clientset.V30().DeleteCrtStore(ctx, n, params)
			},
			func(n string) (*http.Response, error) {
				params := &v32ee.DeleteCrtStoreParams{TransactionId: &txID}
				return clientset.V32EE().DeleteCrtStore(ctx, n, params)
			},
			func(n string) (*http.Response, error) {
				params := &v31ee.DeleteCrtStoreParams{TransactionId: &txID}
				return clientset.V31EE().DeleteCrtStore(ctx, n, params)
			},
			func(n string) (*http.Response, error) {
				params := &v30ee.DeleteCrtStoreParams{TransactionId: &txID}
				return clientset.V30EE().DeleteCrtStore(ctx, n, params)
			},
		)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		return client.CheckResponse(resp, "crt-store deletion")
	}
}

// UserlistCreate returns an executor for creating userlist sections.
func UserlistCreate() func(ctx context.Context, c *client.DataplaneClient, txID string, model *models.Userlist, name string) error {
	return func(ctx context.Context, c *client.DataplaneClient, txID string, model *models.Userlist, _ string) error {
		clientset := c.Clientset()

		resp, err := client.DispatchCreate(ctx, c, model,
			func(m v32.Userlist) (*http.Response, error) {
				params := &v32.CreateUserlistParams{TransactionId: &txID}
				return clientset.V32().CreateUserlist(ctx, params, m)
			},
			func(m v31.Userlist) (*http.Response, error) {
				params := &v31.CreateUserlistParams{TransactionId: &txID}
				return clientset.V31().CreateUserlist(ctx, params, m)
			},
			func(m v30.Userlist) (*http.Response, error) {
				params := &v30.CreateUserlistParams{TransactionId: &txID}
				return clientset.V30().CreateUserlist(ctx, params, m)
			},
			func(m v32ee.Userlist) (*http.Response, error) {
				params := &v32ee.CreateUserlistParams{TransactionId: &txID}
				return clientset.V32EE().CreateUserlist(ctx, params, m)
			},
			func(m v31ee.Userlist) (*http.Response, error) {
				params := &v31ee.CreateUserlistParams{TransactionId: &txID}
				return clientset.V31EE().CreateUserlist(ctx, params, m)
			},
			func(m v30ee.Userlist) (*http.Response, error) {
				params := &v30ee.CreateUserlistParams{TransactionId: &txID}
				return clientset.V30EE().CreateUserlist(ctx, params, m)
			},
		)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		return client.CheckResponse(resp, "userlist creation")
	}
}

// UserlistDelete returns an executor for deleting userlist sections.
func UserlistDelete() func(ctx context.Context, c *client.DataplaneClient, txID string, _ *models.Userlist, name string) error {
	return func(ctx context.Context, c *client.DataplaneClient, txID string, _ *models.Userlist, name string) error {
		clientset := c.Clientset()

		resp, err := client.DispatchDelete(ctx, c, name,
			func(n string) (*http.Response, error) {
				params := &v32.DeleteUserlistParams{TransactionId: &txID}
				return clientset.V32().DeleteUserlist(ctx, n, params)
			},
			func(n string) (*http.Response, error) {
				params := &v31.DeleteUserlistParams{TransactionId: &txID}
				return clientset.V31().DeleteUserlist(ctx, n, params)
			},
			func(n string) (*http.Response, error) {
				params := &v30.DeleteUserlistParams{TransactionId: &txID}
				return clientset.V30().DeleteUserlist(ctx, n, params)
			},
			func(n string) (*http.Response, error) {
				params := &v32ee.DeleteUserlistParams{TransactionId: &txID}
				return clientset.V32EE().DeleteUserlist(ctx, n, params)
			},
			func(n string) (*http.Response, error) {
				params := &v31ee.DeleteUserlistParams{TransactionId: &txID}
				return clientset.V31EE().DeleteUserlist(ctx, n, params)
			},
			func(n string) (*http.Response, error) {
				params := &v30ee.DeleteUserlistParams{TransactionId: &txID}
				return clientset.V30EE().DeleteUserlist(ctx, n, params)
			},
		)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		return client.CheckResponse(resp, "userlist deletion")
	}
}

// FCGIAppCreate returns an executor for creating fcgi-app sections.
func FCGIAppCreate() func(ctx context.Context, c *client.DataplaneClient, txID string, model *models.FCGIApp, name string) error {
	return func(ctx context.Context, c *client.DataplaneClient, txID string, model *models.FCGIApp, _ string) error {
		clientset := c.Clientset()

		resp, err := client.DispatchCreate(ctx, c, model,
			func(m v32.FCGIApp) (*http.Response, error) {
				params := &v32.CreateFCGIAppParams{TransactionId: &txID}
				return clientset.V32().CreateFCGIApp(ctx, params, m)
			},
			func(m v31.FCGIApp) (*http.Response, error) {
				params := &v31.CreateFCGIAppParams{TransactionId: &txID}
				return clientset.V31().CreateFCGIApp(ctx, params, m)
			},
			func(m v30.FCGIApp) (*http.Response, error) {
				params := &v30.CreateFCGIAppParams{TransactionId: &txID}
				return clientset.V30().CreateFCGIApp(ctx, params, m)
			},
			func(m v32ee.FCGIApp) (*http.Response, error) {
				params := &v32ee.CreateFCGIAppParams{TransactionId: &txID}
				return clientset.V32EE().CreateFCGIApp(ctx, params, m)
			},
			func(m v31ee.FCGIApp) (*http.Response, error) {
				params := &v31ee.CreateFCGIAppParams{TransactionId: &txID}
				return clientset.V31EE().CreateFCGIApp(ctx, params, m)
			},
			func(m v30ee.FCGIApp) (*http.Response, error) {
				params := &v30ee.CreateFCGIAppParams{TransactionId: &txID}
				return clientset.V30EE().CreateFCGIApp(ctx, params, m)
			},
		)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		return client.CheckResponse(resp, "fcgi-app creation")
	}
}

// FCGIAppUpdate returns an executor for updating fcgi-app sections.
func FCGIAppUpdate() func(ctx context.Context, c *client.DataplaneClient, txID string, model *models.FCGIApp, name string) error {
	return func(ctx context.Context, c *client.DataplaneClient, txID string, model *models.FCGIApp, name string) error {
		clientset := c.Clientset()

		resp, err := client.DispatchUpdate(ctx, c, name, model,
			func(n string, m v32.FCGIApp) (*http.Response, error) {
				params := &v32.ReplaceFCGIAppParams{TransactionId: &txID}
				return clientset.V32().ReplaceFCGIApp(ctx, n, params, m)
			},
			func(n string, m v31.FCGIApp) (*http.Response, error) {
				params := &v31.ReplaceFCGIAppParams{TransactionId: &txID}
				return clientset.V31().ReplaceFCGIApp(ctx, n, params, m)
			},
			func(n string, m v30.FCGIApp) (*http.Response, error) {
				params := &v30.ReplaceFCGIAppParams{TransactionId: &txID}
				return clientset.V30().ReplaceFCGIApp(ctx, n, params, m)
			},
			func(n string, m v32ee.FCGIApp) (*http.Response, error) {
				params := &v32ee.ReplaceFCGIAppParams{TransactionId: &txID}
				return clientset.V32EE().ReplaceFCGIApp(ctx, n, params, m)
			},
			func(n string, m v31ee.FCGIApp) (*http.Response, error) {
				params := &v31ee.ReplaceFCGIAppParams{TransactionId: &txID}
				return clientset.V31EE().ReplaceFCGIApp(ctx, n, params, m)
			},
			func(n string, m v30ee.FCGIApp) (*http.Response, error) {
				params := &v30ee.ReplaceFCGIAppParams{TransactionId: &txID}
				return clientset.V30EE().ReplaceFCGIApp(ctx, n, params, m)
			},
		)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		return client.CheckResponse(resp, "fcgi-app update")
	}
}

// FCGIAppDelete returns an executor for deleting fcgi-app sections.
func FCGIAppDelete() func(ctx context.Context, c *client.DataplaneClient, txID string, _ *models.FCGIApp, name string) error {
	return func(ctx context.Context, c *client.DataplaneClient, txID string, _ *models.FCGIApp, name string) error {
		clientset := c.Clientset()

		resp, err := client.DispatchDelete(ctx, c, name,
			func(n string) (*http.Response, error) {
				params := &v32.DeleteFCGIAppParams{TransactionId: &txID}
				return clientset.V32().DeleteFCGIApp(ctx, n, params)
			},
			func(n string) (*http.Response, error) {
				params := &v31.DeleteFCGIAppParams{TransactionId: &txID}
				return clientset.V31().DeleteFCGIApp(ctx, n, params)
			},
			func(n string) (*http.Response, error) {
				params := &v30.DeleteFCGIAppParams{TransactionId: &txID}
				return clientset.V30().DeleteFCGIApp(ctx, n, params)
			},
			func(n string) (*http.Response, error) {
				params := &v32ee.DeleteFCGIAppParams{TransactionId: &txID}
				return clientset.V32EE().DeleteFCGIApp(ctx, n, params)
			},
			func(n string) (*http.Response, error) {
				params := &v31ee.DeleteFCGIAppParams{TransactionId: &txID}
				return clientset.V31EE().DeleteFCGIApp(ctx, n, params)
			},
			func(n string) (*http.Response, error) {
				params := &v30ee.DeleteFCGIAppParams{TransactionId: &txID}
				return clientset.V30EE().DeleteFCGIApp(ctx, n, params)
			},
		)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		return client.CheckResponse(resp, "fcgi-app deletion")
	}
}
