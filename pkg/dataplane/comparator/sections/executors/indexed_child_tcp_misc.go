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
