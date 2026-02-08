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
