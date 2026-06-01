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

package client

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	v30 "gitlab.com/haproxy-haptic/haptic/pkg/generated/dataplaneapi/v30"
	v30ee "gitlab.com/haproxy-haptic/haptic/pkg/generated/dataplaneapi/v30ee"
	v31 "gitlab.com/haproxy-haptic/haptic/pkg/generated/dataplaneapi/v31"
	v31ee "gitlab.com/haproxy-haptic/haptic/pkg/generated/dataplaneapi/v31ee"
	v32 "gitlab.com/haproxy-haptic/haptic/pkg/generated/dataplaneapi/v32"
	v32ee "gitlab.com/haproxy-haptic/haptic/pkg/generated/dataplaneapi/v32ee"
	v33 "gitlab.com/haproxy-haptic/haptic/pkg/generated/dataplaneapi/v33"
)

// Transaction represents an in-progress dataplane API transaction.
//
// The HAPTIC controller no longer uses transactions in its sync path — every
// reconcile pushes the full rendered config via the /raw endpoint. This
// primitive is retained only so the per-section enterprise integration tests
// in tests/integration/enterprise_*_test.go can drive the dataplane's
// transactional CRUD endpoints directly. New production callers should NOT
// reach for this — use Sync / PushRawConfiguration / PushRawConfigurationSkipReload.
type Transaction struct {
	ID      string
	Version int64

	client *DataplaneClient
}

// CreateTransaction opens a new transaction against the dataplane API. The
// caller is responsible for either committing or aborting it; an orphaned
// transaction stays open server-side until the dataplane garbage-collects it.
func (c *DataplaneClient) CreateTransaction(ctx context.Context, version int64) (*Transaction, error) {
	resp, err := c.Dispatch(ctx, CallFunc[*http.Response]{
		V33: func(c *v33.Client) (*http.Response, error) {
			return c.StartTransaction(ctx, &v33.StartTransactionParams{Version: v33.Version(version)})
		},
		V32: func(c *v32.Client) (*http.Response, error) {
			return c.StartTransaction(ctx, &v32.StartTransactionParams{Version: v32.Version(version)})
		},
		V31: func(c *v31.Client) (*http.Response, error) {
			return c.StartTransaction(ctx, &v31.StartTransactionParams{Version: v31.Version(version)})
		},
		V30: func(c *v30.Client) (*http.Response, error) {
			return c.StartTransaction(ctx, &v30.StartTransactionParams{Version: v30.Version(version)})
		},
		V32EE: func(c *v32ee.Client) (*http.Response, error) {
			return c.StartTransaction(ctx, &v32ee.StartTransactionParams{Version: v32ee.Version(version)})
		},
		V31EE: func(c *v31ee.Client) (*http.Response, error) {
			return c.StartTransaction(ctx, &v31ee.StartTransactionParams{Version: v31ee.Version(version)})
		},
		V30EE: func(c *v30ee.Client) (*http.Response, error) {
			return c.StartTransaction(ctx, &v30ee.StartTransactionParams{Version: v30ee.Version(version)})
		},
	})
	if err != nil {
		return nil, fmt.Errorf("starting transaction: %w", err)
	}
	defer resp.Body.Close()
	if err := CheckResponse(resp, "start transaction"); err != nil {
		return nil, err
	}
	var body struct {
		ID      string `json:"id"`
		Version int64  `json:"_version"`
	}
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading transaction response: %w", err)
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		return nil, fmt.Errorf("decoding transaction response: %w", err)
	}
	if body.ID == "" {
		return nil, fmt.Errorf("dataplane returned empty transaction id (body=%s)", string(raw))
	}
	return &Transaction{ID: body.ID, Version: body.Version, client: c}, nil
}

// Abort cancels the transaction; nothing written under this tx ID takes
// effect. Safe to call on the deferred error-handling path.
func (t *Transaction) Abort(ctx context.Context) error {
	if t == nil || t.client == nil || t.ID == "" {
		return nil
	}
	resp, err := t.client.Dispatch(ctx, CallFunc[*http.Response]{
		V33: func(c *v33.Client) (*http.Response, error) { return c.DeleteTransaction(ctx, t.ID) },
		V32: func(c *v32.Client) (*http.Response, error) { return c.DeleteTransaction(ctx, t.ID) },
		V31: func(c *v31.Client) (*http.Response, error) { return c.DeleteTransaction(ctx, t.ID) },
		V30: func(c *v30.Client) (*http.Response, error) { return c.DeleteTransaction(ctx, t.ID) },
		V32EE: func(c *v32ee.Client) (*http.Response, error) {
			return c.DeleteTransaction(ctx, t.ID)
		},
		V31EE: func(c *v31ee.Client) (*http.Response, error) {
			return c.DeleteTransaction(ctx, t.ID)
		},
		V30EE: func(c *v30ee.Client) (*http.Response, error) {
			return c.DeleteTransaction(ctx, t.ID)
		},
	})
	if err != nil {
		return fmt.Errorf("aborting transaction %s: %w", t.ID, err)
	}
	defer resp.Body.Close()
	// Treat 404 as success — the transaction is already gone (either committed
	// by another caller or garbage-collected by the dataplane). The
	// caller's intent ("make sure this transaction is not lingering") is
	// satisfied either way.
	if resp.StatusCode == http.StatusNotFound {
		return nil
	}
	return CheckResponse(resp, "delete transaction")
}
