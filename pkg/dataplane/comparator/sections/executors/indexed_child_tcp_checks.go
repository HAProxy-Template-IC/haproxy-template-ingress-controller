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
		return dispatchAndCheck(resp, err, "HTTP check creation in backend")
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
		return dispatchAndCheck(resp, err, "HTTP check update in backend")
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
		return dispatchAndCheck(resp, err, "HTTP check deletion from backend")
	}
}

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
		return dispatchAndCheck(resp, err, "TCP check creation in backend")
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
		return dispatchAndCheck(resp, err, "TCP check update in backend")
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
		return dispatchAndCheck(resp, err, "TCP check deletion from backend")
	}
}
