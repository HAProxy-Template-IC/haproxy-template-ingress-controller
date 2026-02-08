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
)

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
		return dispatchAndCheck(resp, err, "crt-store creation")
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
		return dispatchAndCheck(resp, err, "crt-store update")
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
		return dispatchAndCheck(resp, err, "crt-store deletion")
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
		return dispatchAndCheck(resp, err, "userlist creation")
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
		return dispatchAndCheck(resp, err, "userlist deletion")
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
		return dispatchAndCheck(resp, err, "fcgi-app creation")
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
		return dispatchAndCheck(resp, err, "fcgi-app update")
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
		return dispatchAndCheck(resp, err, "fcgi-app deletion")
	}
}
