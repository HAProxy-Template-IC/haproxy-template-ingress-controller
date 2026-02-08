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
