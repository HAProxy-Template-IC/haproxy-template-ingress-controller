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
