// Package executors provides pre-built executor functions for HAProxy configuration operations.
package executors

import (
	"context"
	"fmt"
	"net/http"

	"github.com/haproxytech/client-native/v6/models"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/client"
	v32 "gitlab.com/haproxy-haptic/haptic/pkg/generated/dataplaneapi/v32"
	v32ee "gitlab.com/haproxy-haptic/haptic/pkg/generated/dataplaneapi/v32ee"
)

// AcmeProviderCreate returns an executor for creating acme sections.
// ACME providers are only available in HAProxy DataPlane API v3.2+.
func AcmeProviderCreate() func(ctx context.Context, c *client.DataplaneClient, txID string, model *models.AcmeProvider, name string) error {
	return func(ctx context.Context, c *client.DataplaneClient, txID string, model *models.AcmeProvider, _ string) error {
		clientset := c.Clientset()

		// ACME providers are v3.2+ only - use DispatchCreate32Plus
		resp, err := DispatchCreate32Plus(ctx, c, model,
			func(m v32.AcmeProvider) (*http.Response, error) {
				params := &v32.CreateAcmeProviderParams{TransactionId: &txID}
				return clientset.V32().CreateAcmeProvider(ctx, params, m)
			},
			func(m v32ee.AcmeProvider) (*http.Response, error) {
				params := &v32ee.CreateAcmeProviderParams{TransactionId: &txID}
				return clientset.V32EE().CreateAcmeProvider(ctx, params, m)
			},
		)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		return client.CheckResponse(resp, "acme-provider creation")
	}
}

// AcmeProviderUpdate returns an executor for updating acme sections.
// ACME providers are only available in HAProxy DataPlane API v3.2+.
func AcmeProviderUpdate() func(ctx context.Context, c *client.DataplaneClient, txID string, model *models.AcmeProvider, name string) error {
	return func(ctx context.Context, c *client.DataplaneClient, txID string, model *models.AcmeProvider, name string) error {
		clientset := c.Clientset()

		// ACME providers are v3.2+ only - use DispatchUpdate32Plus
		resp, err := DispatchUpdate32Plus(ctx, c, name, model,
			func(n string, m v32.AcmeProvider) (*http.Response, error) {
				params := &v32.EditAcmeProviderParams{TransactionId: &txID}
				return clientset.V32().EditAcmeProvider(ctx, n, params, m)
			},
			func(n string, m v32ee.AcmeProvider) (*http.Response, error) {
				params := &v32ee.EditAcmeProviderParams{TransactionId: &txID}
				return clientset.V32EE().EditAcmeProvider(ctx, n, params, m)
			},
		)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		return client.CheckResponse(resp, "acme-provider update")
	}
}

// AcmeProviderDelete returns an executor for deleting acme sections.
// ACME providers are only available in HAProxy DataPlane API v3.2+.
func AcmeProviderDelete() func(ctx context.Context, c *client.DataplaneClient, txID string, _ *models.AcmeProvider, name string) error {
	return func(ctx context.Context, c *client.DataplaneClient, txID string, _ *models.AcmeProvider, name string) error {
		clientset := c.Clientset()

		// ACME providers are v3.2+ only - use DispatchDelete32Plus
		resp, err := DispatchDelete32Plus(ctx, c, name,
			func(n string) (*http.Response, error) {
				params := &v32.DeleteAcmeProviderParams{TransactionId: &txID}
				return clientset.V32().DeleteAcmeProvider(ctx, n, params)
			},
			func(n string) (*http.Response, error) {
				params := &v32ee.DeleteAcmeProviderParams{TransactionId: &txID}
				return clientset.V32EE().DeleteAcmeProvider(ctx, n, params)
			},
		)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		return client.CheckResponse(resp, "acme-provider deletion")
	}
}

// v3.2+ Dispatch Helpers
// These are specialized dispatchers for features only available in v3.2+.
// They will return an error if called on v3.0 or v3.1 clients (which should never
// happen since capability checks prevent operation generation for unsupported versions).

// DispatchCreate32Plus is a generic helper for create operations on v3.2+ only features.
func DispatchCreate32Plus[TUnified any, TV32 any, TV32EE any](
	ctx context.Context,
	c *client.DataplaneClient,
	unifiedModel TUnified,
	v32Call func(TV32) (*http.Response, error),
	v32eeCall func(TV32EE) (*http.Response, error),
) (*http.Response, error) {
	// Wrap v3.2+ callbacks to v3.0+ interface with error handlers for unsupported versions
	return client.DispatchCreate(ctx, c, unifiedModel,
		v32Call,
		func(_ struct{}) (*http.Response, error) {
			return nil, fmt.Errorf("this feature requires DataPlane API v3.2+")
		},
		func(_ struct{}) (*http.Response, error) {
			return nil, fmt.Errorf("this feature requires DataPlane API v3.2+")
		},
		v32eeCall,
		func(_ struct{}) (*http.Response, error) {
			return nil, fmt.Errorf("this feature requires DataPlane API v3.2+")
		},
		func(_ struct{}) (*http.Response, error) {
			return nil, fmt.Errorf("this feature requires DataPlane API v3.2+")
		},
	)
}

// DispatchUpdate32Plus is a generic helper for update operations on v3.2+ only features.
func DispatchUpdate32Plus[TUnified any, TV32 any, TV32EE any](
	ctx context.Context,
	c *client.DataplaneClient,
	name string,
	unifiedModel TUnified,
	v32Call func(string, TV32) (*http.Response, error),
	v32eeCall func(string, TV32EE) (*http.Response, error),
) (*http.Response, error) {
	// Wrap v3.2+ callbacks to v3.0+ interface with error handlers for unsupported versions
	return client.DispatchUpdate(ctx, c, name, unifiedModel,
		v32Call,
		func(_ string, _ struct{}) (*http.Response, error) {
			return nil, fmt.Errorf("this feature requires DataPlane API v3.2+")
		},
		func(_ string, _ struct{}) (*http.Response, error) {
			return nil, fmt.Errorf("this feature requires DataPlane API v3.2+")
		},
		v32eeCall,
		func(_ string, _ struct{}) (*http.Response, error) {
			return nil, fmt.Errorf("this feature requires DataPlane API v3.2+")
		},
		func(_ string, _ struct{}) (*http.Response, error) {
			return nil, fmt.Errorf("this feature requires DataPlane API v3.2+")
		},
	)
}

// DispatchDelete32Plus is a generic helper for delete operations on v3.2+ only features.
func DispatchDelete32Plus(
	ctx context.Context,
	c *client.DataplaneClient,
	name string,
	v32Call func(string) (*http.Response, error),
	v32eeCall func(string) (*http.Response, error),
) (*http.Response, error) {
	// Wrap v3.2+ callbacks to v3.0+ interface with error handlers for unsupported versions
	return client.DispatchDelete(ctx, c, name,
		v32Call,
		func(_ string) (*http.Response, error) {
			return nil, fmt.Errorf("this feature requires DataPlane API v3.2+")
		},
		func(_ string) (*http.Response, error) {
			return nil, fmt.Errorf("this feature requires DataPlane API v3.2+")
		},
		v32eeCall,
		func(_ string) (*http.Response, error) {
			return nil, fmt.Errorf("this feature requires DataPlane API v3.2+")
		},
		func(_ string) (*http.Response, error) {
			return nil, fmt.Errorf("this feature requires DataPlane API v3.2+")
		},
	)
}
