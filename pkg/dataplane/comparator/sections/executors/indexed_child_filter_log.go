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
		return dispatchAndCheck(resp, err, "filter creation in frontend")
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
		return dispatchAndCheck(resp, err, "filter update in frontend")
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
		return dispatchAndCheck(resp, err, "filter deletion from frontend")
	}
}

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
		return dispatchAndCheck(resp, err, "filter creation in backend")
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
		return dispatchAndCheck(resp, err, "filter update in backend")
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
		return dispatchAndCheck(resp, err, "filter deletion from backend")
	}
}

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
		return dispatchAndCheck(resp, err, "log target creation in frontend")
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
		return dispatchAndCheck(resp, err, "log target update in frontend")
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
		return dispatchAndCheck(resp, err, "log target deletion from frontend")
	}
}

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
		return dispatchAndCheck(resp, err, "log target creation in backend")
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
		return dispatchAndCheck(resp, err, "log target update in backend")
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
		return dispatchAndCheck(resp, err, "log target deletion from backend")
	}
}
