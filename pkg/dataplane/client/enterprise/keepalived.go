package enterprise

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/client"
	v30ee "gitlab.com/haproxy-haptic/haptic/pkg/generated/dataplaneapi/v30ee"
	v31ee "gitlab.com/haproxy-haptic/haptic/pkg/generated/dataplaneapi/v31ee"
	v32ee "gitlab.com/haproxy-haptic/haptic/pkg/generated/dataplaneapi/v32ee"
)

// KeepalivedOperations provides operations for HAProxy Enterprise Keepalived/VRRP management.
// This includes VRRP instances, sync groups, scripts, and Keepalived-specific transactions.
type KeepalivedOperations struct {
	client *client.DataplaneClient
}

// NewKeepalivedOperations creates a new Keepalived operations client.
func NewKeepalivedOperations(c *client.DataplaneClient) *KeepalivedOperations {
	return &KeepalivedOperations{client: c}
}

// Keepalived Transaction Operations
// Keepalived has its own transaction system separate from HAProxy configuration.

// KeepalivedTransaction represents a Keepalived configuration transaction.
type KeepalivedTransaction = v32ee.KeepalivedTransaction

// StartTransaction starts a new Keepalived configuration transaction.
func (k *KeepalivedOperations) StartTransaction(ctx context.Context) (string, error) {
	resp, err := k.client.DispatchEnterpriseOnly(ctx, client.EnterpriseCallFunc[*http.Response]{
		V32EE: func(c *v32ee.Client) (*http.Response, error) {
			params := &v32ee.StartKeepalivedTransactionParams{}
			return c.StartKeepalivedTransaction(ctx, params)
		},
		V31EE: func(c *v31ee.Client) (*http.Response, error) {
			params := &v31ee.StartKeepalivedTransactionParams{}
			return c.StartKeepalivedTransaction(ctx, params)
		},
		V30EE: func(c *v30ee.Client) (*http.Response, error) {
			params := &v30ee.StartKeepalivedTransactionParams{}
			return c.StartKeepalivedTransaction(ctx, params)
		},
	})
	if err != nil {
		return "", fmt.Errorf("failed to start Keepalived transaction: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("start Keepalived transaction failed with status %d", resp.StatusCode)
	}

	var result KeepalivedTransaction
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("failed to decode Keepalived transaction response: %w", err)
	}

	if result.Id == nil {
		return "", fmt.Errorf("no transaction ID in response")
	}
	return *result.Id, nil
}

// CommitTransaction commits a Keepalived configuration transaction.
func (k *KeepalivedOperations) CommitTransaction(ctx context.Context, txID string) error {
	resp, err := k.client.DispatchEnterpriseOnly(ctx, client.EnterpriseCallFunc[*http.Response]{
		V32EE: func(c *v32ee.Client) (*http.Response, error) {
			params := &v32ee.CommitKeepalivedTransactionParams{}
			return c.CommitKeepalivedTransaction(ctx, txID, params)
		},
		V31EE: func(c *v31ee.Client) (*http.Response, error) {
			params := &v31ee.CommitKeepalivedTransactionParams{}
			return c.CommitKeepalivedTransaction(ctx, txID, params)
		},
		V30EE: func(c *v30ee.Client) (*http.Response, error) {
			params := &v30ee.CommitKeepalivedTransactionParams{}
			return c.CommitKeepalivedTransaction(ctx, txID, params)
		},
	})
	if err != nil {
		return fmt.Errorf("failed to commit Keepalived transaction '%s': %w", txID, err)
	}
	defer resp.Body.Close()

	return checkResponseStatus(resp, fmt.Sprintf("failed to commit Keepalived transaction '%s'", txID))
}

// DeleteTransaction deletes (cancels) a Keepalived configuration transaction.
func (k *KeepalivedOperations) DeleteTransaction(ctx context.Context, txID string) error {
	resp, err := k.client.DispatchEnterpriseOnly(ctx, client.EnterpriseCallFunc[*http.Response]{
		V32EE: func(c *v32ee.Client) (*http.Response, error) {
			return c.DeleteKeepalivedTransaction(ctx, txID)
		},
		V31EE: func(c *v31ee.Client) (*http.Response, error) {
			return c.DeleteKeepalivedTransaction(ctx, txID)
		},
		V30EE: func(c *v30ee.Client) (*http.Response, error) {
			return c.DeleteKeepalivedTransaction(ctx, txID)
		},
	})
	if err != nil {
		return fmt.Errorf("failed to delete Keepalived transaction '%s': %w", txID, err)
	}
	defer resp.Body.Close()

	return checkResponseStatus(resp, fmt.Sprintf("failed to delete Keepalived transaction '%s'", txID))
}

// GetTransaction retrieves a specific Keepalived transaction.
func (k *KeepalivedOperations) GetTransaction(ctx context.Context, txID string) (*KeepalivedTransaction, error) {
	resp, err := k.client.DispatchEnterpriseOnly(ctx, client.EnterpriseCallFunc[*http.Response]{
		V32EE: func(c *v32ee.Client) (*http.Response, error) {
			return c.GetKeepalivedTransaction(ctx, txID)
		},
		V31EE: func(c *v31ee.Client) (*http.Response, error) {
			return c.GetKeepalivedTransaction(ctx, txID)
		},
		V30EE: func(c *v30ee.Client) (*http.Response, error) {
			return c.GetKeepalivedTransaction(ctx, txID)
		},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to get Keepalived transaction '%s': %w", txID, err)
	}
	defer resp.Body.Close()

	return decodeResponseOr404[KeepalivedTransaction](resp, fmt.Sprintf("failed to get Keepalived transaction '%s'", txID))
}

// VRRPInstance represents a VRRP instance configuration.
type VRRPInstance = v32ee.VrrpInstance

// GetAllVRRPInstances retrieves all VRRP instances.
func (k *KeepalivedOperations) GetAllVRRPInstances(ctx context.Context) ([]VRRPInstance, error) {
	resp, err := k.client.DispatchEnterpriseOnly(ctx, client.EnterpriseCallFunc[*http.Response]{
		V32EE: func(c *v32ee.Client) (*http.Response, error) {
			return c.GetAllVRRPInstance(ctx)
		},
		V31EE: func(c *v31ee.Client) (*http.Response, error) {
			return c.GetAllVRRPInstance(ctx)
		},
		V30EE: func(c *v30ee.Client) (*http.Response, error) {
			return c.GetAllVRRPInstance(ctx)
		},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to get VRRP instances: %w", err)
	}
	defer resp.Body.Close()

	return decodeSliceResponse[VRRPInstance](resp, "failed to get VRRP instances")
}

// GetVRRPInstance retrieves a specific VRRP instance by name.
func (k *KeepalivedOperations) GetVRRPInstance(ctx context.Context, name string) (*VRRPInstance, error) {
	resp, err := k.client.DispatchEnterpriseOnly(ctx, client.EnterpriseCallFunc[*http.Response]{
		V32EE: func(c *v32ee.Client) (*http.Response, error) {
			return c.GetVRRPInstance(ctx, name)
		},
		V31EE: func(c *v31ee.Client) (*http.Response, error) {
			return c.GetVRRPInstance(ctx, name)
		},
		V30EE: func(c *v30ee.Client) (*http.Response, error) {
			return c.GetVRRPInstance(ctx, name)
		},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to get VRRP instance '%s': %w", name, err)
	}
	defer resp.Body.Close()

	return decodeResponseOr404[VRRPInstance](resp, fmt.Sprintf("failed to get VRRP instance '%s'", name))
}

// CreateVRRPInstance creates a new VRRP instance.
func (k *KeepalivedOperations) CreateVRRPInstance(ctx context.Context, txID string, instance *VRRPInstance) error {
	jsonData, err := json.Marshal(instance)
	if err != nil {
		return fmt.Errorf("failed to marshal VRRP instance: %w", err)
	}

	resp, err := k.client.DispatchEnterpriseOnly(ctx, client.EnterpriseCallFunc[*http.Response]{
		V32EE: func(c *v32ee.Client) (*http.Response, error) {
			var i v32ee.VrrpInstance
			if err := json.Unmarshal(jsonData, &i); err != nil {
				return nil, err
			}
			params := &v32ee.CreateVRRPInstanceParams{TransactionId: &txID}
			return c.CreateVRRPInstance(ctx, params, i)
		},
		V31EE: func(c *v31ee.Client) (*http.Response, error) {
			var i v31ee.VrrpInstance
			if err := json.Unmarshal(jsonData, &i); err != nil {
				return nil, err
			}
			params := &v31ee.CreateVRRPInstanceParams{TransactionId: &txID}
			return c.CreateVRRPInstance(ctx, params, i)
		},
		V30EE: func(c *v30ee.Client) (*http.Response, error) {
			var i v30ee.VrrpInstance
			if err := json.Unmarshal(jsonData, &i); err != nil {
				return nil, err
			}
			params := &v30ee.CreateVRRPInstanceParams{TransactionId: &txID}
			return c.CreateVRRPInstance(ctx, params, i)
		},
	})
	if err != nil {
		return fmt.Errorf("failed to create VRRP instance: %w", err)
	}
	defer resp.Body.Close()

	return checkResponseStatus(resp, "failed to create VRRP instance")
}

// ReplaceVRRPInstance replaces an existing VRRP instance.
func (k *KeepalivedOperations) ReplaceVRRPInstance(ctx context.Context, txID, name string, instance *VRRPInstance) error {
	jsonData, err := json.Marshal(instance)
	if err != nil {
		return fmt.Errorf("failed to marshal VRRP instance: %w", err)
	}

	resp, err := k.client.DispatchEnterpriseOnly(ctx, client.EnterpriseCallFunc[*http.Response]{
		V32EE: func(c *v32ee.Client) (*http.Response, error) {
			var i v32ee.VrrpInstance
			if err := json.Unmarshal(jsonData, &i); err != nil {
				return nil, err
			}
			params := &v32ee.ReplaceVRRPInstanceParams{TransactionId: &txID}
			return c.ReplaceVRRPInstance(ctx, name, params, i)
		},
		V31EE: func(c *v31ee.Client) (*http.Response, error) {
			var i v31ee.VrrpInstance
			if err := json.Unmarshal(jsonData, &i); err != nil {
				return nil, err
			}
			params := &v31ee.ReplaceVRRPInstanceParams{TransactionId: &txID}
			return c.ReplaceVRRPInstance(ctx, name, params, i)
		},
		V30EE: func(c *v30ee.Client) (*http.Response, error) {
			var i v30ee.VrrpInstance
			if err := json.Unmarshal(jsonData, &i); err != nil {
				return nil, err
			}
			params := &v30ee.ReplaceVRRPInstanceParams{TransactionId: &txID}
			return c.ReplaceVRRPInstance(ctx, name, params, i)
		},
	})
	if err != nil {
		return fmt.Errorf("failed to replace VRRP instance '%s': %w", name, err)
	}
	defer resp.Body.Close()

	return checkResponseStatus(resp, fmt.Sprintf("failed to replace VRRP instance '%s'", name))
}

// DeleteVRRPInstance deletes a VRRP instance.
func (k *KeepalivedOperations) DeleteVRRPInstance(ctx context.Context, txID, name string) error {
	resp, err := k.client.DispatchEnterpriseOnly(ctx, client.EnterpriseCallFunc[*http.Response]{
		V32EE: func(c *v32ee.Client) (*http.Response, error) {
			params := &v32ee.DeleteVRRPInstanceParams{TransactionId: &txID}
			return c.DeleteVRRPInstance(ctx, name, params)
		},
		V31EE: func(c *v31ee.Client) (*http.Response, error) {
			params := &v31ee.DeleteVRRPInstanceParams{TransactionId: &txID}
			return c.DeleteVRRPInstance(ctx, name, params)
		},
		V30EE: func(c *v30ee.Client) (*http.Response, error) {
			params := &v30ee.DeleteVRRPInstanceParams{TransactionId: &txID}
			return c.DeleteVRRPInstance(ctx, name, params)
		},
	})
	if err != nil {
		return fmt.Errorf("failed to delete VRRP instance '%s': %w", name, err)
	}
	defer resp.Body.Close()

	return checkResponseStatus(resp, fmt.Sprintf("failed to delete VRRP instance '%s'", name))
}
