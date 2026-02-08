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

// LogProfileCreate returns an executor for creating log-profile sections.
// Log profiles are only available in HAProxy DataPlane API v3.1+.
func LogProfileCreate() func(ctx context.Context, c *client.DataplaneClient, txID string, model *models.LogProfile, name string) error {
	return func(ctx context.Context, c *client.DataplaneClient, txID string, model *models.LogProfile, _ string) error {
		clientset := c.Clientset()

		// Log profiles are v3.1+ only - use DispatchCreate31Plus
		resp, err := DispatchCreate31Plus(ctx, c, model,
			func(m v32.LogProfile) (*http.Response, error) {
				params := &v32.CreateLogProfileParams{TransactionId: &txID}
				return clientset.V32().CreateLogProfile(ctx, params, m)
			},
			func(m v31.LogProfile) (*http.Response, error) {
				params := &v31.CreateLogProfileParams{TransactionId: &txID}
				return clientset.V31().CreateLogProfile(ctx, params, m)
			},
			func(m v32ee.LogProfile) (*http.Response, error) {
				params := &v32ee.CreateLogProfileParams{TransactionId: &txID}
				return clientset.V32EE().CreateLogProfile(ctx, params, m)
			},
			func(m v31ee.LogProfile) (*http.Response, error) {
				params := &v31ee.CreateLogProfileParams{TransactionId: &txID}
				return clientset.V31EE().CreateLogProfile(ctx, params, m)
			},
		)
		return dispatchAndCheck(resp, err, "log-profile creation")
	}
}

// LogProfileUpdate returns an executor for updating log-profile sections.
// Log profiles are only available in HAProxy DataPlane API v3.1+.
func LogProfileUpdate() func(ctx context.Context, c *client.DataplaneClient, txID string, model *models.LogProfile, name string) error {
	return func(ctx context.Context, c *client.DataplaneClient, txID string, model *models.LogProfile, name string) error {
		clientset := c.Clientset()

		// Log profiles are v3.1+ only - use DispatchUpdate31Plus
		resp, err := DispatchUpdate31Plus(ctx, c, name, model,
			func(n string, m v32.LogProfile) (*http.Response, error) {
				params := &v32.EditLogProfileParams{TransactionId: &txID}
				return clientset.V32().EditLogProfile(ctx, n, params, m)
			},
			func(n string, m v31.LogProfile) (*http.Response, error) {
				params := &v31.EditLogProfileParams{TransactionId: &txID}
				return clientset.V31().EditLogProfile(ctx, n, params, m)
			},
			func(n string, m v32ee.LogProfile) (*http.Response, error) {
				params := &v32ee.EditLogProfileParams{TransactionId: &txID}
				return clientset.V32EE().EditLogProfile(ctx, n, params, m)
			},
			func(n string, m v31ee.LogProfile) (*http.Response, error) {
				params := &v31ee.EditLogProfileParams{TransactionId: &txID}
				return clientset.V31EE().EditLogProfile(ctx, n, params, m)
			},
		)
		return dispatchAndCheck(resp, err, "log-profile update")
	}
}

// LogProfileDelete returns an executor for deleting log-profile sections.
// Log profiles are only available in HAProxy DataPlane API v3.1+.
func LogProfileDelete() func(ctx context.Context, c *client.DataplaneClient, txID string, _ *models.LogProfile, name string) error {
	return func(ctx context.Context, c *client.DataplaneClient, txID string, _ *models.LogProfile, name string) error {
		clientset := c.Clientset()

		// Log profiles are v3.1+ only - use DispatchDelete31Plus
		resp, err := DispatchDelete31Plus(ctx, c, name,
			func(n string) (*http.Response, error) {
				params := &v32.DeleteLogProfileParams{TransactionId: &txID}
				return clientset.V32().DeleteLogProfile(ctx, n, params)
			},
			func(n string) (*http.Response, error) {
				params := &v31.DeleteLogProfileParams{TransactionId: &txID}
				return clientset.V31().DeleteLogProfile(ctx, n, params)
			},
			func(n string) (*http.Response, error) {
				params := &v32ee.DeleteLogProfileParams{TransactionId: &txID}
				return clientset.V32EE().DeleteLogProfile(ctx, n, params)
			},
			func(n string) (*http.Response, error) {
				params := &v31ee.DeleteLogProfileParams{TransactionId: &txID}
				return clientset.V31EE().DeleteLogProfile(ctx, n, params)
			},
		)
		return dispatchAndCheck(resp, err, "log-profile deletion")
	}
}

// TracesUpdate returns an executor for updating the traces section.
// The traces section is a singleton - it can be created or replaced.
// Traces configuration is only available in HAProxy DataPlane API v3.1+.
func TracesUpdate() func(ctx context.Context, c *client.DataplaneClient, txID string, model *models.Traces) error {
	return func(ctx context.Context, c *client.DataplaneClient, txID string, model *models.Traces) error {
		clientset := c.Clientset()

		// Traces are v3.1+ only - use DispatchUpdate31Plus with empty name since it's a singleton
		resp, err := DispatchUpdate31Plus(ctx, c, "", model,
			func(_ string, m v32.Traces) (*http.Response, error) {
				params := &v32.ReplaceTracesParams{TransactionId: &txID}
				return clientset.V32().ReplaceTraces(ctx, params, m)
			},
			func(_ string, m v31.Traces) (*http.Response, error) {
				params := &v31.ReplaceTracesParams{TransactionId: &txID}
				return clientset.V31().ReplaceTraces(ctx, params, m)
			},
			func(_ string, m v32ee.Traces) (*http.Response, error) {
				params := &v32ee.ReplaceTracesParams{TransactionId: &txID}
				return clientset.V32EE().ReplaceTraces(ctx, params, m)
			},
			func(_ string, m v31ee.Traces) (*http.Response, error) {
				params := &v31ee.ReplaceTracesParams{TransactionId: &txID}
				return clientset.V31EE().ReplaceTraces(ctx, params, m)
			},
		)
		return dispatchAndCheck(resp, err, "traces update")
	}
}

// v3.1+ Dispatch Helpers
// These are specialized dispatchers for features only available in v3.1+.
// They will return an error if called on v3.0 clients (which should never
// happen since capability checks prevent operation generation for unsupported versions).

// DispatchCreate31Plus is a generic helper for create operations on v3.1+ only features.
func DispatchCreate31Plus[TUnified any, TV32 any, TV31 any, TV32EE any, TV31EE any](
	ctx context.Context,
	c *client.DataplaneClient,
	unifiedModel TUnified,
	v32Call func(TV32) (*http.Response, error),
	v31Call func(TV31) (*http.Response, error),
	v32eeCall func(TV32EE) (*http.Response, error),
	v31eeCall func(TV31EE) (*http.Response, error),
) (*http.Response, error) {
	// Wrap v3.1+ callbacks to v3.0+ interface with error handlers for unsupported versions
	return client.DispatchCreate(ctx, c, unifiedModel,
		v32Call,
		v31Call,
		func(_ struct{}) (*http.Response, error) {
			return nil, fmt.Errorf("this feature requires DataPlane API v3.1+")
		},
		v32eeCall,
		v31eeCall,
		func(_ struct{}) (*http.Response, error) {
			return nil, fmt.Errorf("this feature requires DataPlane API v3.1+")
		},
	)
}

// DispatchUpdate31Plus is a generic helper for update operations on v3.1+ only features.
func DispatchUpdate31Plus[TUnified any, TV32 any, TV31 any, TV32EE any, TV31EE any](
	ctx context.Context,
	c *client.DataplaneClient,
	name string,
	unifiedModel TUnified,
	v32Call func(string, TV32) (*http.Response, error),
	v31Call func(string, TV31) (*http.Response, error),
	v32eeCall func(string, TV32EE) (*http.Response, error),
	v31eeCall func(string, TV31EE) (*http.Response, error),
) (*http.Response, error) {
	// Wrap v3.1+ callbacks to v3.0+ interface with error handlers for unsupported versions
	return client.DispatchUpdate(ctx, c, name, unifiedModel,
		v32Call,
		v31Call,
		func(_ string, _ struct{}) (*http.Response, error) {
			return nil, fmt.Errorf("this feature requires DataPlane API v3.1+")
		},
		v32eeCall,
		v31eeCall,
		func(_ string, _ struct{}) (*http.Response, error) {
			return nil, fmt.Errorf("this feature requires DataPlane API v3.1+")
		},
	)
}

// DispatchDelete31Plus is a generic helper for delete operations on v3.1+ only features.
func DispatchDelete31Plus(
	ctx context.Context,
	c *client.DataplaneClient,
	name string,
	v32Call func(string) (*http.Response, error),
	v31Call func(string) (*http.Response, error),
	v32eeCall func(string) (*http.Response, error),
	v31eeCall func(string) (*http.Response, error),
) (*http.Response, error) {
	// Wrap v3.1+ callbacks to v3.0+ interface with error handlers for unsupported versions
	return client.DispatchDelete(ctx, c, name,
		v32Call,
		v31Call,
		func(_ string) (*http.Response, error) {
			return nil, fmt.Errorf("this feature requires DataPlane API v3.1+")
		},
		v32eeCall,
		v31eeCall,
		func(_ string) (*http.Response, error) {
			return nil, fmt.Errorf("this feature requires DataPlane API v3.1+")
		},
	)
}
