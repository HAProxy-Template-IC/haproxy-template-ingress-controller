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
	v33 "gitlab.com/haproxy-haptic/haptic/pkg/generated/dataplaneapi/v33"
)

// ACLFrontendCreate returns an executor for creating ACLs in frontends.
func ACLFrontendCreate() func(ctx context.Context, c *client.DataplaneClient, txID string, parent string, index int, model *models.ACL) error {
	return func(ctx context.Context, c *client.DataplaneClient, txID string, parent string, index int, model *models.ACL) error {
		clientset := c.Clientset()

		resp, err := client.DispatchCreateChild(ctx, c, parent, index, model,
			func(p string, idx int, m v33.Acl) (*http.Response, error) {
				params := &v33.CreateAclFrontendParams{TransactionId: &txID}
				return clientset.V33().CreateAclFrontend(ctx, p, idx, params, m)
			},
			func(p string, idx int, m v32.Acl) (*http.Response, error) {
				params := &v32.CreateAclFrontendParams{TransactionId: &txID}
				return clientset.V32().CreateAclFrontend(ctx, p, idx, params, m)
			},
			func(p string, idx int, m v31.Acl) (*http.Response, error) {
				params := &v31.CreateAclFrontendParams{TransactionId: &txID}
				return clientset.V31().CreateAclFrontend(ctx, p, idx, params, m)
			},
			func(p string, idx int, m v30.Acl) (*http.Response, error) {
				params := &v30.CreateAclFrontendParams{TransactionId: &txID}
				return clientset.V30().CreateAclFrontend(ctx, p, idx, params, m)
			},
			func(p string, idx int, m v32ee.Acl) (*http.Response, error) {
				params := &v32ee.CreateAclFrontendParams{TransactionId: &txID}
				return clientset.V32EE().CreateAclFrontend(ctx, p, idx, params, m)
			},
			func(p string, idx int, m v31ee.Acl) (*http.Response, error) {
				params := &v31ee.CreateAclFrontendParams{TransactionId: &txID}
				return clientset.V31EE().CreateAclFrontend(ctx, p, idx, params, m)
			},
			func(p string, idx int, m v30ee.Acl) (*http.Response, error) {
				params := &v30ee.CreateAclFrontendParams{TransactionId: &txID}
				return clientset.V30EE().CreateAclFrontend(ctx, p, idx, params, m)
			},
		)
		return dispatchAndCheck(resp, err, "ACL creation in frontend")
	}
}

// ACLFrontendUpdate returns an executor for updating ACLs in frontends.
func ACLFrontendUpdate() func(ctx context.Context, c *client.DataplaneClient, txID string, parent string, index int, model *models.ACL) error {
	return func(ctx context.Context, c *client.DataplaneClient, txID string, parent string, index int, model *models.ACL) error {
		clientset := c.Clientset()

		resp, err := client.DispatchReplaceChild(ctx, c, parent, index, model,
			func(p string, idx int, m v33.Acl) (*http.Response, error) {
				params := &v33.ReplaceAclFrontendParams{TransactionId: &txID}
				return clientset.V33().ReplaceAclFrontend(ctx, p, idx, params, m)
			},
			func(p string, idx int, m v32.Acl) (*http.Response, error) {
				params := &v32.ReplaceAclFrontendParams{TransactionId: &txID}
				return clientset.V32().ReplaceAclFrontend(ctx, p, idx, params, m)
			},
			func(p string, idx int, m v31.Acl) (*http.Response, error) {
				params := &v31.ReplaceAclFrontendParams{TransactionId: &txID}
				return clientset.V31().ReplaceAclFrontend(ctx, p, idx, params, m)
			},
			func(p string, idx int, m v30.Acl) (*http.Response, error) {
				params := &v30.ReplaceAclFrontendParams{TransactionId: &txID}
				return clientset.V30().ReplaceAclFrontend(ctx, p, idx, params, m)
			},
			func(p string, idx int, m v32ee.Acl) (*http.Response, error) {
				params := &v32ee.ReplaceAclFrontendParams{TransactionId: &txID}
				return clientset.V32EE().ReplaceAclFrontend(ctx, p, idx, params, m)
			},
			func(p string, idx int, m v31ee.Acl) (*http.Response, error) {
				params := &v31ee.ReplaceAclFrontendParams{TransactionId: &txID}
				return clientset.V31EE().ReplaceAclFrontend(ctx, p, idx, params, m)
			},
			func(p string, idx int, m v30ee.Acl) (*http.Response, error) {
				params := &v30ee.ReplaceAclFrontendParams{TransactionId: &txID}
				return clientset.V30EE().ReplaceAclFrontend(ctx, p, idx, params, m)
			},
		)
		return dispatchAndCheck(resp, err, "ACL update in frontend")
	}
}

// ACLFrontendDelete returns an executor for deleting ACLs from frontends.
func ACLFrontendDelete() func(ctx context.Context, c *client.DataplaneClient, txID string, parent string, index int, _ *models.ACL) error {
	return func(ctx context.Context, c *client.DataplaneClient, txID string, parent string, index int, _ *models.ACL) error {
		clientset := c.Clientset()

		resp, err := client.DispatchDeleteChild(ctx, c, parent, index,
			func(p string, idx int) (*http.Response, error) {
				params := &v33.DeleteAclFrontendParams{TransactionId: &txID}
				return clientset.V33().DeleteAclFrontend(ctx, p, idx, params)
			},
			func(p string, idx int) (*http.Response, error) {
				params := &v32.DeleteAclFrontendParams{TransactionId: &txID}
				return clientset.V32().DeleteAclFrontend(ctx, p, idx, params)
			},
			func(p string, idx int) (*http.Response, error) {
				params := &v31.DeleteAclFrontendParams{TransactionId: &txID}
				return clientset.V31().DeleteAclFrontend(ctx, p, idx, params)
			},
			func(p string, idx int) (*http.Response, error) {
				params := &v30.DeleteAclFrontendParams{TransactionId: &txID}
				return clientset.V30().DeleteAclFrontend(ctx, p, idx, params)
			},
			func(p string, idx int) (*http.Response, error) {
				params := &v32ee.DeleteAclFrontendParams{TransactionId: &txID}
				return clientset.V32EE().DeleteAclFrontend(ctx, p, idx, params)
			},
			func(p string, idx int) (*http.Response, error) {
				params := &v31ee.DeleteAclFrontendParams{TransactionId: &txID}
				return clientset.V31EE().DeleteAclFrontend(ctx, p, idx, params)
			},
			func(p string, idx int) (*http.Response, error) {
				params := &v30ee.DeleteAclFrontendParams{TransactionId: &txID}
				return clientset.V30EE().DeleteAclFrontend(ctx, p, idx, params)
			},
		)
		return dispatchAndCheck(resp, err, "ACL deletion from frontend")
	}
}

// ACLBackendCreate returns an executor for creating ACLs in backends.
func ACLBackendCreate() func(ctx context.Context, c *client.DataplaneClient, txID string, parent string, index int, model *models.ACL) error {
	return func(ctx context.Context, c *client.DataplaneClient, txID string, parent string, index int, model *models.ACL) error {
		clientset := c.Clientset()

		resp, err := client.DispatchCreateChild(ctx, c, parent, index, model,
			func(p string, idx int, m v33.Acl) (*http.Response, error) {
				params := &v33.CreateAclBackendParams{TransactionId: &txID}
				return clientset.V33().CreateAclBackend(ctx, p, idx, params, m)
			},
			func(p string, idx int, m v32.Acl) (*http.Response, error) {
				params := &v32.CreateAclBackendParams{TransactionId: &txID}
				return clientset.V32().CreateAclBackend(ctx, p, idx, params, m)
			},
			func(p string, idx int, m v31.Acl) (*http.Response, error) {
				params := &v31.CreateAclBackendParams{TransactionId: &txID}
				return clientset.V31().CreateAclBackend(ctx, p, idx, params, m)
			},
			func(p string, idx int, m v30.Acl) (*http.Response, error) {
				params := &v30.CreateAclBackendParams{TransactionId: &txID}
				return clientset.V30().CreateAclBackend(ctx, p, idx, params, m)
			},
			func(p string, idx int, m v32ee.Acl) (*http.Response, error) {
				params := &v32ee.CreateAclBackendParams{TransactionId: &txID}
				return clientset.V32EE().CreateAclBackend(ctx, p, idx, params, m)
			},
			func(p string, idx int, m v31ee.Acl) (*http.Response, error) {
				params := &v31ee.CreateAclBackendParams{TransactionId: &txID}
				return clientset.V31EE().CreateAclBackend(ctx, p, idx, params, m)
			},
			func(p string, idx int, m v30ee.Acl) (*http.Response, error) {
				params := &v30ee.CreateAclBackendParams{TransactionId: &txID}
				return clientset.V30EE().CreateAclBackend(ctx, p, idx, params, m)
			},
		)
		return dispatchAndCheck(resp, err, "ACL creation in backend")
	}
}

// ACLBackendUpdate returns an executor for updating ACLs in backends.
func ACLBackendUpdate() func(ctx context.Context, c *client.DataplaneClient, txID string, parent string, index int, model *models.ACL) error {
	return func(ctx context.Context, c *client.DataplaneClient, txID string, parent string, index int, model *models.ACL) error {
		clientset := c.Clientset()

		resp, err := client.DispatchReplaceChild(ctx, c, parent, index, model,
			func(p string, idx int, m v33.Acl) (*http.Response, error) {
				params := &v33.ReplaceAclBackendParams{TransactionId: &txID}
				return clientset.V33().ReplaceAclBackend(ctx, p, idx, params, m)
			},
			func(p string, idx int, m v32.Acl) (*http.Response, error) {
				params := &v32.ReplaceAclBackendParams{TransactionId: &txID}
				return clientset.V32().ReplaceAclBackend(ctx, p, idx, params, m)
			},
			func(p string, idx int, m v31.Acl) (*http.Response, error) {
				params := &v31.ReplaceAclBackendParams{TransactionId: &txID}
				return clientset.V31().ReplaceAclBackend(ctx, p, idx, params, m)
			},
			func(p string, idx int, m v30.Acl) (*http.Response, error) {
				params := &v30.ReplaceAclBackendParams{TransactionId: &txID}
				return clientset.V30().ReplaceAclBackend(ctx, p, idx, params, m)
			},
			func(p string, idx int, m v32ee.Acl) (*http.Response, error) {
				params := &v32ee.ReplaceAclBackendParams{TransactionId: &txID}
				return clientset.V32EE().ReplaceAclBackend(ctx, p, idx, params, m)
			},
			func(p string, idx int, m v31ee.Acl) (*http.Response, error) {
				params := &v31ee.ReplaceAclBackendParams{TransactionId: &txID}
				return clientset.V31EE().ReplaceAclBackend(ctx, p, idx, params, m)
			},
			func(p string, idx int, m v30ee.Acl) (*http.Response, error) {
				params := &v30ee.ReplaceAclBackendParams{TransactionId: &txID}
				return clientset.V30EE().ReplaceAclBackend(ctx, p, idx, params, m)
			},
		)
		return dispatchAndCheck(resp, err, "ACL update in backend")
	}
}

// ACLBackendDelete returns an executor for deleting ACLs from backends.
func ACLBackendDelete() func(ctx context.Context, c *client.DataplaneClient, txID string, parent string, index int, _ *models.ACL) error {
	return func(ctx context.Context, c *client.DataplaneClient, txID string, parent string, index int, _ *models.ACL) error {
		clientset := c.Clientset()

		resp, err := client.DispatchDeleteChild(ctx, c, parent, index,
			func(p string, idx int) (*http.Response, error) {
				params := &v33.DeleteAclBackendParams{TransactionId: &txID}
				return clientset.V33().DeleteAclBackend(ctx, p, idx, params)
			},
			func(p string, idx int) (*http.Response, error) {
				params := &v32.DeleteAclBackendParams{TransactionId: &txID}
				return clientset.V32().DeleteAclBackend(ctx, p, idx, params)
			},
			func(p string, idx int) (*http.Response, error) {
				params := &v31.DeleteAclBackendParams{TransactionId: &txID}
				return clientset.V31().DeleteAclBackend(ctx, p, idx, params)
			},
			func(p string, idx int) (*http.Response, error) {
				params := &v30.DeleteAclBackendParams{TransactionId: &txID}
				return clientset.V30().DeleteAclBackend(ctx, p, idx, params)
			},
			func(p string, idx int) (*http.Response, error) {
				params := &v32ee.DeleteAclBackendParams{TransactionId: &txID}
				return clientset.V32EE().DeleteAclBackend(ctx, p, idx, params)
			},
			func(p string, idx int) (*http.Response, error) {
				params := &v31ee.DeleteAclBackendParams{TransactionId: &txID}
				return clientset.V31EE().DeleteAclBackend(ctx, p, idx, params)
			},
			func(p string, idx int) (*http.Response, error) {
				params := &v30ee.DeleteAclBackendParams{TransactionId: &txID}
				return clientset.V30EE().DeleteAclBackend(ctx, p, idx, params)
			},
		)
		return dispatchAndCheck(resp, err, "ACL deletion from backend")
	}
}
