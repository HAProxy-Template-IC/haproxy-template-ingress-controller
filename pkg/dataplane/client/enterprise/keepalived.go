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

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("commit Keepalived transaction '%s' failed with status %d", txID, resp.StatusCode)
	}
	return nil
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

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("delete Keepalived transaction '%s' failed with status %d", txID, resp.StatusCode)
	}
	return nil
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

	if resp.StatusCode == http.StatusNotFound {
		return nil, ErrNotFound
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("get Keepalived transaction '%s' failed with status %d", txID, resp.StatusCode)
	}

	var result KeepalivedTransaction
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode Keepalived transaction response: %w", err)
	}
	return &result, nil
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

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("get VRRP instances failed with status %d", resp.StatusCode)
	}

	var result []VRRPInstance
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode VRRP instances response: %w", err)
	}
	return result, nil
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

	if resp.StatusCode == http.StatusNotFound {
		return nil, ErrNotFound
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("get VRRP instance '%s' failed with status %d", name, resp.StatusCode)
	}

	var result VRRPInstance
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode VRRP instance response: %w", err)
	}
	return &result, nil
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

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("create VRRP instance failed with status %d", resp.StatusCode)
	}
	return nil
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

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("replace VRRP instance '%s' failed with status %d", name, resp.StatusCode)
	}
	return nil
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

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("delete VRRP instance '%s' failed with status %d", name, resp.StatusCode)
	}
	return nil
}

// VRRPSyncGroup represents a VRRP sync group configuration.
type VRRPSyncGroup = v32ee.VrrpSyncGroup

// GetAllVRRPSyncGroups retrieves all VRRP sync groups.
func (k *KeepalivedOperations) GetAllVRRPSyncGroups(ctx context.Context) ([]VRRPSyncGroup, error) {
	resp, err := k.client.DispatchEnterpriseOnly(ctx, client.EnterpriseCallFunc[*http.Response]{
		V32EE: func(c *v32ee.Client) (*http.Response, error) {
			return c.GetAllVRRPSyncGroup(ctx)
		},
		V31EE: func(c *v31ee.Client) (*http.Response, error) {
			return c.GetAllVRRPSyncGroup(ctx)
		},
		V30EE: func(c *v30ee.Client) (*http.Response, error) {
			return c.GetAllVRRPSyncGroup(ctx)
		},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to get VRRP sync groups: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("get VRRP sync groups failed with status %d", resp.StatusCode)
	}

	var result []VRRPSyncGroup
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode VRRP sync groups response: %w", err)
	}
	return result, nil
}

// GetVRRPSyncGroup retrieves a specific VRRP sync group by name.
func (k *KeepalivedOperations) GetVRRPSyncGroup(ctx context.Context, name string) (*VRRPSyncGroup, error) {
	resp, err := k.client.DispatchEnterpriseOnly(ctx, client.EnterpriseCallFunc[*http.Response]{
		V32EE: func(c *v32ee.Client) (*http.Response, error) {
			return c.GetVRRPSyncGroup(ctx, name)
		},
		V31EE: func(c *v31ee.Client) (*http.Response, error) {
			return c.GetVRRPSyncGroup(ctx, name)
		},
		V30EE: func(c *v30ee.Client) (*http.Response, error) {
			return c.GetVRRPSyncGroup(ctx, name)
		},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to get VRRP sync group '%s': %w", name, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, ErrNotFound
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("get VRRP sync group '%s' failed with status %d", name, resp.StatusCode)
	}

	var result VRRPSyncGroup
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode VRRP sync group response: %w", err)
	}
	return &result, nil
}

// CreateVRRPSyncGroup creates a new VRRP sync group.
func (k *KeepalivedOperations) CreateVRRPSyncGroup(ctx context.Context, txID string, group *VRRPSyncGroup) error {
	jsonData, err := json.Marshal(group)
	if err != nil {
		return fmt.Errorf("failed to marshal VRRP sync group: %w", err)
	}

	resp, err := k.client.DispatchEnterpriseOnly(ctx, client.EnterpriseCallFunc[*http.Response]{
		V32EE: func(c *v32ee.Client) (*http.Response, error) {
			var g v32ee.VrrpSyncGroup
			if err := json.Unmarshal(jsonData, &g); err != nil {
				return nil, err
			}
			params := &v32ee.CreateVRRPSyncGroupParams{TransactionId: &txID}
			return c.CreateVRRPSyncGroup(ctx, params, g)
		},
		V31EE: func(c *v31ee.Client) (*http.Response, error) {
			var g v31ee.VrrpSyncGroup
			if err := json.Unmarshal(jsonData, &g); err != nil {
				return nil, err
			}
			params := &v31ee.CreateVRRPSyncGroupParams{TransactionId: &txID}
			return c.CreateVRRPSyncGroup(ctx, params, g)
		},
		V30EE: func(c *v30ee.Client) (*http.Response, error) {
			var g v30ee.VrrpSyncGroup
			if err := json.Unmarshal(jsonData, &g); err != nil {
				return nil, err
			}
			params := &v30ee.CreateVRRPSyncGroupParams{TransactionId: &txID}
			return c.CreateVRRPSyncGroup(ctx, params, g)
		},
	})
	if err != nil {
		return fmt.Errorf("failed to create VRRP sync group: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("create VRRP sync group failed with status %d", resp.StatusCode)
	}
	return nil
}

// DeleteVRRPSyncGroup deletes a VRRP sync group.
func (k *KeepalivedOperations) DeleteVRRPSyncGroup(ctx context.Context, txID, name string) error {
	resp, err := k.client.DispatchEnterpriseOnly(ctx, client.EnterpriseCallFunc[*http.Response]{
		V32EE: func(c *v32ee.Client) (*http.Response, error) {
			params := &v32ee.DeleteVRRPSyncGroupParams{TransactionId: &txID}
			return c.DeleteVRRPSyncGroup(ctx, name, params)
		},
		V31EE: func(c *v31ee.Client) (*http.Response, error) {
			params := &v31ee.DeleteVRRPSyncGroupParams{TransactionId: &txID}
			return c.DeleteVRRPSyncGroup(ctx, name, params)
		},
		V30EE: func(c *v30ee.Client) (*http.Response, error) {
			params := &v30ee.DeleteVRRPSyncGroupParams{TransactionId: &txID}
			return c.DeleteVRRPSyncGroup(ctx, name, params)
		},
	})
	if err != nil {
		return fmt.Errorf("failed to delete VRRP sync group '%s': %w", name, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("delete VRRP sync group '%s' failed with status %d", name, resp.StatusCode)
	}
	return nil
}

// VRRPScript represents a VRRP tracking script configuration.
type VRRPScript = v32ee.VrrpTrackScript

// GetAllVRRPScripts retrieves all VRRP scripts.
func (k *KeepalivedOperations) GetAllVRRPScripts(ctx context.Context) ([]VRRPScript, error) {
	resp, err := k.client.DispatchEnterpriseOnly(ctx, client.EnterpriseCallFunc[*http.Response]{
		V32EE: func(c *v32ee.Client) (*http.Response, error) {
			return c.GetAllVRRPScript(ctx)
		},
		V31EE: func(c *v31ee.Client) (*http.Response, error) {
			return c.GetAllVRRPScript(ctx)
		},
		V30EE: func(c *v30ee.Client) (*http.Response, error) {
			return c.GetAllVRRPScript(ctx)
		},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to get VRRP scripts: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("get VRRP scripts failed with status %d", resp.StatusCode)
	}

	var result []VRRPScript
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode VRRP scripts response: %w", err)
	}
	return result, nil
}

// GetVRRPScript retrieves a specific VRRP script by name.
func (k *KeepalivedOperations) GetVRRPScript(ctx context.Context, name string) (*VRRPScript, error) {
	resp, err := k.client.DispatchEnterpriseOnly(ctx, client.EnterpriseCallFunc[*http.Response]{
		V32EE: func(c *v32ee.Client) (*http.Response, error) {
			return c.GetVRRPScript(ctx, name)
		},
		V31EE: func(c *v31ee.Client) (*http.Response, error) {
			return c.GetVRRPScript(ctx, name)
		},
		V30EE: func(c *v30ee.Client) (*http.Response, error) {
			return c.GetVRRPScript(ctx, name)
		},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to get VRRP script '%s': %w", name, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, ErrNotFound
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("get VRRP script '%s' failed with status %d", name, resp.StatusCode)
	}

	var result VRRPScript
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode VRRP script response: %w", err)
	}
	return &result, nil
}

// CreateVRRPScript creates a new VRRP script.
func (k *KeepalivedOperations) CreateVRRPScript(ctx context.Context, txID string, script *VRRPScript) error {
	jsonData, err := json.Marshal(script)
	if err != nil {
		return fmt.Errorf("failed to marshal VRRP script: %w", err)
	}

	resp, err := k.client.DispatchEnterpriseOnly(ctx, client.EnterpriseCallFunc[*http.Response]{
		V32EE: func(c *v32ee.Client) (*http.Response, error) {
			var s v32ee.VrrpTrackScript
			if err := json.Unmarshal(jsonData, &s); err != nil {
				return nil, err
			}
			params := &v32ee.CreateVRRPScriptParams{TransactionId: &txID}
			return c.CreateVRRPScript(ctx, params, s)
		},
		V31EE: func(c *v31ee.Client) (*http.Response, error) {
			var s v31ee.VrrpTrackScript
			if err := json.Unmarshal(jsonData, &s); err != nil {
				return nil, err
			}
			params := &v31ee.CreateVRRPScriptParams{TransactionId: &txID}
			return c.CreateVRRPScript(ctx, params, s)
		},
		V30EE: func(c *v30ee.Client) (*http.Response, error) {
			var s v30ee.VrrpTrackScript
			if err := json.Unmarshal(jsonData, &s); err != nil {
				return nil, err
			}
			params := &v30ee.CreateVRRPScriptParams{TransactionId: &txID}
			return c.CreateVRRPScript(ctx, params, s)
		},
	})
	if err != nil {
		return fmt.Errorf("failed to create VRRP script: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("create VRRP script failed with status %d", resp.StatusCode)
	}
	return nil
}

// DeleteVRRPScript deletes a VRRP script.
func (k *KeepalivedOperations) DeleteVRRPScript(ctx context.Context, txID, name string) error {
	resp, err := k.client.DispatchEnterpriseOnly(ctx, client.EnterpriseCallFunc[*http.Response]{
		V32EE: func(c *v32ee.Client) (*http.Response, error) {
			params := &v32ee.DeleteVRRPScriptParams{TransactionId: &txID}
			return c.DeleteVRRPScript(ctx, name, params)
		},
		V31EE: func(c *v31ee.Client) (*http.Response, error) {
			params := &v31ee.DeleteVRRPScriptParams{TransactionId: &txID}
			return c.DeleteVRRPScript(ctx, name, params)
		},
		V30EE: func(c *v30ee.Client) (*http.Response, error) {
			params := &v30ee.DeleteVRRPScriptParams{TransactionId: &txID}
			return c.DeleteVRRPScript(ctx, name, params)
		},
	})
	if err != nil {
		return fmt.Errorf("failed to delete VRRP script '%s': %w", name, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("delete VRRP script '%s' failed with status %d", name, resp.StatusCode)
	}
	return nil
}
