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

// BackendSwitchingRuleCreate returns an executor for creating backend switching rules.
func BackendSwitchingRuleCreate() func(ctx context.Context, c *client.DataplaneClient, txID string, parent string, index int, model *models.BackendSwitchingRule) error {
	return func(ctx context.Context, c *client.DataplaneClient, txID string, parent string, index int, model *models.BackendSwitchingRule) error {
		clientset := c.Clientset()

		resp, err := client.DispatchCreateChild(ctx, c, parent, index, model,
			func(p string, idx int, m v32.BackendSwitchingRule) (*http.Response, error) {
				params := &v32.CreateBackendSwitchingRuleParams{TransactionId: &txID}
				return clientset.V32().CreateBackendSwitchingRule(ctx, p, idx, params, m)
			},
			func(p string, idx int, m v31.BackendSwitchingRule) (*http.Response, error) {
				params := &v31.CreateBackendSwitchingRuleParams{TransactionId: &txID}
				return clientset.V31().CreateBackendSwitchingRule(ctx, p, idx, params, m)
			},
			func(p string, idx int, m v30.BackendSwitchingRule) (*http.Response, error) {
				params := &v30.CreateBackendSwitchingRuleParams{TransactionId: &txID}
				return clientset.V30().CreateBackendSwitchingRule(ctx, p, idx, params, m)
			},
			func(p string, idx int, m v32ee.BackendSwitchingRule) (*http.Response, error) {
				params := &v32ee.CreateBackendSwitchingRuleParams{TransactionId: &txID}
				return clientset.V32EE().CreateBackendSwitchingRule(ctx, p, idx, params, m)
			},
			func(p string, idx int, m v31ee.BackendSwitchingRule) (*http.Response, error) {
				params := &v31ee.CreateBackendSwitchingRuleParams{TransactionId: &txID}
				return clientset.V31EE().CreateBackendSwitchingRule(ctx, p, idx, params, m)
			},
			func(p string, idx int, m v30ee.BackendSwitchingRule) (*http.Response, error) {
				params := &v30ee.CreateBackendSwitchingRuleParams{TransactionId: &txID}
				return clientset.V30EE().CreateBackendSwitchingRule(ctx, p, idx, params, m)
			},
		)
		return dispatchAndCheck(resp, err, "backend switching rule creation")
	}
}

// BackendSwitchingRuleUpdate returns an executor for updating backend switching rules.
func BackendSwitchingRuleUpdate() func(ctx context.Context, c *client.DataplaneClient, txID string, parent string, index int, model *models.BackendSwitchingRule) error {
	return func(ctx context.Context, c *client.DataplaneClient, txID string, parent string, index int, model *models.BackendSwitchingRule) error {
		clientset := c.Clientset()

		resp, err := client.DispatchReplaceChild(ctx, c, parent, index, model,
			func(p string, idx int, m v32.BackendSwitchingRule) (*http.Response, error) {
				params := &v32.ReplaceBackendSwitchingRuleParams{TransactionId: &txID}
				return clientset.V32().ReplaceBackendSwitchingRule(ctx, p, idx, params, m)
			},
			func(p string, idx int, m v31.BackendSwitchingRule) (*http.Response, error) {
				params := &v31.ReplaceBackendSwitchingRuleParams{TransactionId: &txID}
				return clientset.V31().ReplaceBackendSwitchingRule(ctx, p, idx, params, m)
			},
			func(p string, idx int, m v30.BackendSwitchingRule) (*http.Response, error) {
				params := &v30.ReplaceBackendSwitchingRuleParams{TransactionId: &txID}
				return clientset.V30().ReplaceBackendSwitchingRule(ctx, p, idx, params, m)
			},
			func(p string, idx int, m v32ee.BackendSwitchingRule) (*http.Response, error) {
				params := &v32ee.ReplaceBackendSwitchingRuleParams{TransactionId: &txID}
				return clientset.V32EE().ReplaceBackendSwitchingRule(ctx, p, idx, params, m)
			},
			func(p string, idx int, m v31ee.BackendSwitchingRule) (*http.Response, error) {
				params := &v31ee.ReplaceBackendSwitchingRuleParams{TransactionId: &txID}
				return clientset.V31EE().ReplaceBackendSwitchingRule(ctx, p, idx, params, m)
			},
			func(p string, idx int, m v30ee.BackendSwitchingRule) (*http.Response, error) {
				params := &v30ee.ReplaceBackendSwitchingRuleParams{TransactionId: &txID}
				return clientset.V30EE().ReplaceBackendSwitchingRule(ctx, p, idx, params, m)
			},
		)
		return dispatchAndCheck(resp, err, "backend switching rule update")
	}
}

// BackendSwitchingRuleDelete returns an executor for deleting backend switching rules.
func BackendSwitchingRuleDelete() func(ctx context.Context, c *client.DataplaneClient, txID string, parent string, index int, _ *models.BackendSwitchingRule) error {
	return func(ctx context.Context, c *client.DataplaneClient, txID string, parent string, index int, _ *models.BackendSwitchingRule) error {
		clientset := c.Clientset()

		resp, err := client.DispatchDeleteChild(ctx, c, parent, index,
			func(p string, idx int) (*http.Response, error) {
				params := &v32.DeleteBackendSwitchingRuleParams{TransactionId: &txID}
				return clientset.V32().DeleteBackendSwitchingRule(ctx, p, idx, params)
			},
			func(p string, idx int) (*http.Response, error) {
				params := &v31.DeleteBackendSwitchingRuleParams{TransactionId: &txID}
				return clientset.V31().DeleteBackendSwitchingRule(ctx, p, idx, params)
			},
			func(p string, idx int) (*http.Response, error) {
				params := &v30.DeleteBackendSwitchingRuleParams{TransactionId: &txID}
				return clientset.V30().DeleteBackendSwitchingRule(ctx, p, idx, params)
			},
			func(p string, idx int) (*http.Response, error) {
				params := &v32ee.DeleteBackendSwitchingRuleParams{TransactionId: &txID}
				return clientset.V32EE().DeleteBackendSwitchingRule(ctx, p, idx, params)
			},
			func(p string, idx int) (*http.Response, error) {
				params := &v31ee.DeleteBackendSwitchingRuleParams{TransactionId: &txID}
				return clientset.V31EE().DeleteBackendSwitchingRule(ctx, p, idx, params)
			},
			func(p string, idx int) (*http.Response, error) {
				params := &v30ee.DeleteBackendSwitchingRuleParams{TransactionId: &txID}
				return clientset.V30EE().DeleteBackendSwitchingRule(ctx, p, idx, params)
			},
		)
		return dispatchAndCheck(resp, err, "backend switching rule deletion")
	}
}

// ServerSwitchingRuleBackendCreate returns an executor for creating server switching rules in backends.
func ServerSwitchingRuleBackendCreate() func(ctx context.Context, c *client.DataplaneClient, txID string, parent string, index int, model *models.ServerSwitchingRule) error {
	return func(ctx context.Context, c *client.DataplaneClient, txID string, parent string, index int, model *models.ServerSwitchingRule) error {
		clientset := c.Clientset()

		resp, err := client.DispatchCreateChild(ctx, c, parent, index, model,
			func(p string, idx int, m v32.ServerSwitchingRule) (*http.Response, error) {
				params := &v32.CreateServerSwitchingRuleParams{TransactionId: &txID}
				return clientset.V32().CreateServerSwitchingRule(ctx, p, idx, params, m)
			},
			func(p string, idx int, m v31.ServerSwitchingRule) (*http.Response, error) {
				params := &v31.CreateServerSwitchingRuleParams{TransactionId: &txID}
				return clientset.V31().CreateServerSwitchingRule(ctx, p, idx, params, m)
			},
			func(p string, idx int, m v30.ServerSwitchingRule) (*http.Response, error) {
				params := &v30.CreateServerSwitchingRuleParams{TransactionId: &txID}
				return clientset.V30().CreateServerSwitchingRule(ctx, p, idx, params, m)
			},
			func(p string, idx int, m v32ee.ServerSwitchingRule) (*http.Response, error) {
				params := &v32ee.CreateServerSwitchingRuleBackendParams{TransactionId: &txID}
				return clientset.V32EE().CreateServerSwitchingRuleBackend(ctx, p, idx, params, m)
			},
			func(p string, idx int, m v31ee.ServerSwitchingRule) (*http.Response, error) {
				params := &v31ee.CreateServerSwitchingRuleParams{TransactionId: &txID}
				return clientset.V31EE().CreateServerSwitchingRule(ctx, p, idx, params, m)
			},
			func(p string, idx int, m v30ee.ServerSwitchingRule) (*http.Response, error) {
				params := &v30ee.CreateServerSwitchingRuleParams{TransactionId: &txID}
				return clientset.V30EE().CreateServerSwitchingRule(ctx, p, idx, params, m)
			},
		)
		return dispatchAndCheck(resp, err, "server switching rule creation in backend")
	}
}

// ServerSwitchingRuleBackendUpdate returns an executor for updating server switching rules in backends.
func ServerSwitchingRuleBackendUpdate() func(ctx context.Context, c *client.DataplaneClient, txID string, parent string, index int, model *models.ServerSwitchingRule) error {
	return func(ctx context.Context, c *client.DataplaneClient, txID string, parent string, index int, model *models.ServerSwitchingRule) error {
		clientset := c.Clientset()

		resp, err := client.DispatchReplaceChild(ctx, c, parent, index, model,
			func(p string, idx int, m v32.ServerSwitchingRule) (*http.Response, error) {
				params := &v32.ReplaceServerSwitchingRuleParams{TransactionId: &txID}
				return clientset.V32().ReplaceServerSwitchingRule(ctx, p, idx, params, m)
			},
			func(p string, idx int, m v31.ServerSwitchingRule) (*http.Response, error) {
				params := &v31.ReplaceServerSwitchingRuleParams{TransactionId: &txID}
				return clientset.V31().ReplaceServerSwitchingRule(ctx, p, idx, params, m)
			},
			func(p string, idx int, m v30.ServerSwitchingRule) (*http.Response, error) {
				params := &v30.ReplaceServerSwitchingRuleParams{TransactionId: &txID}
				return clientset.V30().ReplaceServerSwitchingRule(ctx, p, idx, params, m)
			},
			func(p string, idx int, m v32ee.ServerSwitchingRule) (*http.Response, error) {
				params := &v32ee.ReplaceServerSwitchingRuleBackendParams{TransactionId: &txID}
				return clientset.V32EE().ReplaceServerSwitchingRuleBackend(ctx, p, idx, params, m)
			},
			func(p string, idx int, m v31ee.ServerSwitchingRule) (*http.Response, error) {
				params := &v31ee.ReplaceServerSwitchingRuleParams{TransactionId: &txID}
				return clientset.V31EE().ReplaceServerSwitchingRule(ctx, p, idx, params, m)
			},
			func(p string, idx int, m v30ee.ServerSwitchingRule) (*http.Response, error) {
				params := &v30ee.ReplaceServerSwitchingRuleParams{TransactionId: &txID}
				return clientset.V30EE().ReplaceServerSwitchingRule(ctx, p, idx, params, m)
			},
		)
		return dispatchAndCheck(resp, err, "server switching rule update in backend")
	}
}

// ServerSwitchingRuleBackendDelete returns an executor for deleting server switching rules from backends.
func ServerSwitchingRuleBackendDelete() func(ctx context.Context, c *client.DataplaneClient, txID string, parent string, index int, _ *models.ServerSwitchingRule) error {
	return func(ctx context.Context, c *client.DataplaneClient, txID string, parent string, index int, _ *models.ServerSwitchingRule) error {
		clientset := c.Clientset()

		resp, err := client.DispatchDeleteChild(ctx, c, parent, index,
			func(p string, idx int) (*http.Response, error) {
				params := &v32.DeleteServerSwitchingRuleParams{TransactionId: &txID}
				return clientset.V32().DeleteServerSwitchingRule(ctx, p, idx, params)
			},
			func(p string, idx int) (*http.Response, error) {
				params := &v31.DeleteServerSwitchingRuleParams{TransactionId: &txID}
				return clientset.V31().DeleteServerSwitchingRule(ctx, p, idx, params)
			},
			func(p string, idx int) (*http.Response, error) {
				params := &v30.DeleteServerSwitchingRuleParams{TransactionId: &txID}
				return clientset.V30().DeleteServerSwitchingRule(ctx, p, idx, params)
			},
			func(p string, idx int) (*http.Response, error) {
				params := &v32ee.DeleteServerSwitchingRuleBackendParams{TransactionId: &txID}
				return clientset.V32EE().DeleteServerSwitchingRuleBackend(ctx, p, idx, params)
			},
			func(p string, idx int) (*http.Response, error) {
				params := &v31ee.DeleteServerSwitchingRuleParams{TransactionId: &txID}
				return clientset.V31EE().DeleteServerSwitchingRule(ctx, p, idx, params)
			},
			func(p string, idx int) (*http.Response, error) {
				params := &v30ee.DeleteServerSwitchingRuleParams{TransactionId: &txID}
				return clientset.V30EE().DeleteServerSwitchingRule(ctx, p, idx, params)
			},
		)
		return dispatchAndCheck(resp, err, "server switching rule deletion from backend")
	}
}
