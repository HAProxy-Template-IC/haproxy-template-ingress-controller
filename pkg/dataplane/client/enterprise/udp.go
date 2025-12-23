package enterprise

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"haptic/pkg/dataplane/client"
	v30ee "haptic/pkg/generated/dataplaneapi/v30ee"
	v31ee "haptic/pkg/generated/dataplaneapi/v31ee"
	v32ee "haptic/pkg/generated/dataplaneapi/v32ee"
)

// ErrUDPLBACLsRequiresV32 is returned when ACL operations are attempted on pre-3.2 enterprise.
var ErrUDPLBACLsRequiresV32 = errors.New("UDP load balancer ACL operations require HAProxy Enterprise v3.2+")

// ErrUDPLBServerSwitchingRequiresV32 is returned when server switching rule operations are attempted on pre-3.2 enterprise.
var ErrUDPLBServerSwitchingRequiresV32 = errors.New("UDP load balancer server switching rules require HAProxy Enterprise v3.2+")

// UDPLBOperations provides operations for HAProxy Enterprise UDP load balancing.
// This includes UDP load balancers and their child resources (ACLs, binds, log targets, server switching rules).
type UDPLBOperations struct {
	client *client.DataplaneClient
}

// NewUDPLBOperations creates a new UDP load balancing operations client.
func NewUDPLBOperations(c *client.DataplaneClient) *UDPLBOperations {
	return &UDPLBOperations{client: c}
}

// =============================================================================
// UDP Load Balancer Operations
// =============================================================================

// UDPLb represents a UDP load balancer configuration.
type UDPLb = v32ee.UDPLb

// GetAllUDPLbs retrieves all UDP load balancers.
func (u *UDPLBOperations) GetAllUDPLbs(ctx context.Context, txID string) ([]UDPLb, error) {
	resp, err := u.client.DispatchEnterpriseOnly(ctx, client.EnterpriseCallFunc[*http.Response]{
		V32EE: func(c *v32ee.Client) (*http.Response, error) {
			params := &v32ee.GetUDPLbsParams{TransactionId: &txID}
			return c.GetUDPLbs(ctx, params)
		},
		V31EE: func(c *v31ee.Client) (*http.Response, error) {
			params := &v31ee.GetUDPLbsParams{TransactionId: &txID}
			return c.GetUDPLbs(ctx, params)
		},
		V30EE: func(c *v30ee.Client) (*http.Response, error) {
			params := &v30ee.GetUDPLbsParams{TransactionId: &txID}
			return c.GetUDPLbs(ctx, params)
		},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to get UDP load balancers: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("get UDP load balancers failed with status %d", resp.StatusCode)
	}

	var result []UDPLb
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode UDP load balancers response: %w", err)
	}
	return result, nil
}

// GetUDPLb retrieves a specific UDP load balancer by name.
func (u *UDPLBOperations) GetUDPLb(ctx context.Context, txID, name string) (*UDPLb, error) {
	resp, err := u.client.DispatchEnterpriseOnly(ctx, client.EnterpriseCallFunc[*http.Response]{
		V32EE: func(c *v32ee.Client) (*http.Response, error) {
			params := &v32ee.GetUDPlbParams{TransactionId: &txID}
			return c.GetUDPlb(ctx, name, params)
		},
		V31EE: func(c *v31ee.Client) (*http.Response, error) {
			params := &v31ee.GetUDPlbParams{TransactionId: &txID}
			return c.GetUDPlb(ctx, name, params)
		},
		V30EE: func(c *v30ee.Client) (*http.Response, error) {
			params := &v30ee.GetUDPlbParams{TransactionId: &txID}
			return c.GetUDPlb(ctx, name, params)
		},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to get UDP load balancer '%s': %w", name, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, ErrNotFound
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("get UDP load balancer '%s' failed with status %d", name, resp.StatusCode)
	}

	var result UDPLb
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode UDP load balancer response: %w", err)
	}
	return &result, nil
}

// CreateUDPLb creates a new UDP load balancer.
func (u *UDPLBOperations) CreateUDPLb(ctx context.Context, txID string, lb *UDPLb) error {
	jsonData, err := json.Marshal(lb)
	if err != nil {
		return fmt.Errorf("failed to marshal UDP load balancer: %w", err)
	}

	resp, err := u.client.DispatchEnterpriseOnly(ctx, client.EnterpriseCallFunc[*http.Response]{
		V32EE: func(c *v32ee.Client) (*http.Response, error) {
			var l v32ee.UDPLb
			if err := json.Unmarshal(jsonData, &l); err != nil {
				return nil, err
			}
			params := &v32ee.CreateUDPLbParams{TransactionId: &txID}
			return c.CreateUDPLb(ctx, params, l)
		},
		V31EE: func(c *v31ee.Client) (*http.Response, error) {
			var l v31ee.UDPLb
			if err := json.Unmarshal(jsonData, &l); err != nil {
				return nil, err
			}
			params := &v31ee.CreateUDPLbParams{TransactionId: &txID}
			return c.CreateUDPLb(ctx, params, l)
		},
		V30EE: func(c *v30ee.Client) (*http.Response, error) {
			var l v30ee.UDPLb
			if err := json.Unmarshal(jsonData, &l); err != nil {
				return nil, err
			}
			params := &v30ee.CreateUDPLbParams{TransactionId: &txID}
			return c.CreateUDPLb(ctx, params, l)
		},
	})
	if err != nil {
		return fmt.Errorf("failed to create UDP load balancer: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("create UDP load balancer failed with status %d", resp.StatusCode)
	}
	return nil
}

// ReplaceUDPLb replaces an existing UDP load balancer.
func (u *UDPLBOperations) ReplaceUDPLb(ctx context.Context, txID, name string, lb *UDPLb) error {
	jsonData, err := json.Marshal(lb)
	if err != nil {
		return fmt.Errorf("failed to marshal UDP load balancer: %w", err)
	}

	resp, err := u.client.DispatchEnterpriseOnly(ctx, client.EnterpriseCallFunc[*http.Response]{
		V32EE: func(c *v32ee.Client) (*http.Response, error) {
			var l v32ee.UDPLb
			if err := json.Unmarshal(jsonData, &l); err != nil {
				return nil, err
			}
			params := &v32ee.ReplaceUDPLbParams{TransactionId: &txID}
			return c.ReplaceUDPLb(ctx, name, params, l)
		},
		V31EE: func(c *v31ee.Client) (*http.Response, error) {
			var l v31ee.UDPLb
			if err := json.Unmarshal(jsonData, &l); err != nil {
				return nil, err
			}
			params := &v31ee.ReplaceUDPLbParams{TransactionId: &txID}
			return c.ReplaceUDPLb(ctx, name, params, l)
		},
		V30EE: func(c *v30ee.Client) (*http.Response, error) {
			var l v30ee.UDPLb
			if err := json.Unmarshal(jsonData, &l); err != nil {
				return nil, err
			}
			params := &v30ee.ReplaceUDPLbParams{TransactionId: &txID}
			return c.ReplaceUDPLb(ctx, name, params, l)
		},
	})
	if err != nil {
		return fmt.Errorf("failed to replace UDP load balancer '%s': %w", name, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("replace UDP load balancer '%s' failed with status %d", name, resp.StatusCode)
	}
	return nil
}

// DeleteUDPLb deletes a UDP load balancer.
func (u *UDPLBOperations) DeleteUDPLb(ctx context.Context, txID, name string) error {
	resp, err := u.client.DispatchEnterpriseOnly(ctx, client.EnterpriseCallFunc[*http.Response]{
		V32EE: func(c *v32ee.Client) (*http.Response, error) {
			params := &v32ee.DeleteUDPLbParams{TransactionId: &txID}
			return c.DeleteUDPLb(ctx, name, params)
		},
		V31EE: func(c *v31ee.Client) (*http.Response, error) {
			params := &v31ee.DeleteUDPLbParams{TransactionId: &txID}
			return c.DeleteUDPLb(ctx, name, params)
		},
		V30EE: func(c *v30ee.Client) (*http.Response, error) {
			params := &v30ee.DeleteUDPLbParams{TransactionId: &txID}
			return c.DeleteUDPLb(ctx, name, params)
		},
	})
	if err != nil {
		return fmt.Errorf("failed to delete UDP load balancer '%s': %w", name, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("delete UDP load balancer '%s' failed with status %d", name, resp.StatusCode)
	}
	return nil
}

// =============================================================================
// UDP Load Balancer ACL Operations
// =============================================================================

// ACL represents an ACL configuration.
type ACL = v32ee.Acl

// GetAllACLsUDPLb retrieves all ACLs for a UDP load balancer.
// Note: UDP LB ACLs are only available in HAProxy Enterprise v3.2+.
func (u *UDPLBOperations) GetAllACLsUDPLb(ctx context.Context, txID, lbName string) ([]ACL, error) {
	// UDP LB ACLs are v3.2+ only - check version first
	if u.client.Clientset().MinorVersion() < 2 {
		return nil, ErrUDPLBACLsRequiresV32
	}

	resp, err := u.client.DispatchEnterpriseOnly(ctx, client.EnterpriseCallFunc[*http.Response]{
		V32EE: func(c *v32ee.Client) (*http.Response, error) {
			params := &v32ee.GetAllAclUDPLbParams{TransactionId: &txID}
			return c.GetAllAclUDPLb(ctx, lbName, params)
		},
		// V31EE and V30EE don't have UDP LB ACL endpoints
	})
	if err != nil {
		return nil, fmt.Errorf("failed to get ACLs for UDP LB '%s': %w", lbName, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("get ACLs for UDP LB '%s' failed with status %d", lbName, resp.StatusCode)
	}

	var result []ACL
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode ACLs response: %w", err)
	}
	return result, nil
}

// CreateACLUDPLb creates a new ACL for a UDP load balancer at the specified index.
// Note: UDP LB ACLs are only available in HAProxy Enterprise v3.2+.
func (u *UDPLBOperations) CreateACLUDPLb(ctx context.Context, txID, lbName string, index int, acl *ACL) error {
	// UDP LB ACLs are v3.2+ only - check version first
	if u.client.Clientset().MinorVersion() < 2 {
		return ErrUDPLBACLsRequiresV32
	}

	jsonData, err := json.Marshal(acl)
	if err != nil {
		return fmt.Errorf("failed to marshal ACL: %w", err)
	}

	resp, err := u.client.DispatchEnterpriseOnly(ctx, client.EnterpriseCallFunc[*http.Response]{
		V32EE: func(c *v32ee.Client) (*http.Response, error) {
			var a v32ee.Acl
			if err := json.Unmarshal(jsonData, &a); err != nil {
				return nil, err
			}
			params := &v32ee.CreateAclUDPLbParams{TransactionId: &txID}
			return c.CreateAclUDPLb(ctx, lbName, index, params, a)
		},
		// V31EE and V30EE don't have UDP LB ACL endpoints
	})
	if err != nil {
		return fmt.Errorf("failed to create ACL for UDP LB '%s': %w", lbName, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("create ACL for UDP LB '%s' failed with status %d", lbName, resp.StatusCode)
	}
	return nil
}

// DeleteACLUDPLb deletes an ACL from a UDP load balancer at the specified index.
// Note: UDP LB ACLs are only available in HAProxy Enterprise v3.2+.
func (u *UDPLBOperations) DeleteACLUDPLb(ctx context.Context, txID, lbName string, index int) error {
	// UDP LB ACLs are v3.2+ only - check version first
	if u.client.Clientset().MinorVersion() < 2 {
		return ErrUDPLBACLsRequiresV32
	}

	resp, err := u.client.DispatchEnterpriseOnly(ctx, client.EnterpriseCallFunc[*http.Response]{
		V32EE: func(c *v32ee.Client) (*http.Response, error) {
			params := &v32ee.DeleteAclUDPLbParams{TransactionId: &txID}
			return c.DeleteAclUDPLb(ctx, lbName, index, params)
		},
		// V31EE and V30EE don't have UDP LB ACL endpoints
	})
	if err != nil {
		return fmt.Errorf("failed to delete ACL from UDP LB '%s' at index %d: %w", lbName, index, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("delete ACL from UDP LB '%s' at index %d failed with status %d", lbName, index, resp.StatusCode)
	}
	return nil
}

// =============================================================================
// UDP Load Balancer Dgram Bind Operations
// =============================================================================

// DgramBind represents a datagram bind configuration.
type DgramBind = v32ee.DgramBind

// GetAllDgramBindsUDPLb retrieves all dgram binds for a UDP load balancer.
func (u *UDPLBOperations) GetAllDgramBindsUDPLb(ctx context.Context, txID, lbName string) ([]DgramBind, error) {
	resp, err := u.client.DispatchEnterpriseOnly(ctx, client.EnterpriseCallFunc[*http.Response]{
		V32EE: func(c *v32ee.Client) (*http.Response, error) {
			params := &v32ee.GetAllDgramBindUDPLbParams{TransactionId: &txID}
			return c.GetAllDgramBindUDPLb(ctx, lbName, params)
		},
		V31EE: func(c *v31ee.Client) (*http.Response, error) {
			params := &v31ee.GetAllDgramBindUDPLbParams{TransactionId: &txID}
			return c.GetAllDgramBindUDPLb(ctx, lbName, params)
		},
		V30EE: func(c *v30ee.Client) (*http.Response, error) {
			params := &v30ee.GetAllDgramBindUDPLbParams{TransactionId: &txID}
			return c.GetAllDgramBindUDPLb(ctx, lbName, params)
		},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to get dgram binds for UDP LB '%s': %w", lbName, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("get dgram binds for UDP LB '%s' failed with status %d", lbName, resp.StatusCode)
	}

	var result []DgramBind
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode dgram binds response: %w", err)
	}
	return result, nil
}

// CreateDgramBindUDPLb creates a new dgram bind for a UDP load balancer.
func (u *UDPLBOperations) CreateDgramBindUDPLb(ctx context.Context, txID, lbName string, bind *DgramBind) error {
	jsonData, err := json.Marshal(bind)
	if err != nil {
		return fmt.Errorf("failed to marshal dgram bind: %w", err)
	}

	resp, err := u.client.DispatchEnterpriseOnly(ctx, client.EnterpriseCallFunc[*http.Response]{
		V32EE: func(c *v32ee.Client) (*http.Response, error) {
			var b v32ee.DgramBind
			if err := json.Unmarshal(jsonData, &b); err != nil {
				return nil, err
			}
			params := &v32ee.CreateDgramBindUDPLbParams{TransactionId: &txID}
			return c.CreateDgramBindUDPLb(ctx, lbName, params, b)
		},
		V31EE: func(c *v31ee.Client) (*http.Response, error) {
			var b v31ee.DgramBind
			if err := json.Unmarshal(jsonData, &b); err != nil {
				return nil, err
			}
			params := &v31ee.CreateDgramBindUDPLbParams{TransactionId: &txID}
			return c.CreateDgramBindUDPLb(ctx, lbName, params, b)
		},
		V30EE: func(c *v30ee.Client) (*http.Response, error) {
			var b v30ee.DgramBind
			if err := json.Unmarshal(jsonData, &b); err != nil {
				return nil, err
			}
			params := &v30ee.CreateDgramBindUDPLbParams{TransactionId: &txID}
			return c.CreateDgramBindUDPLb(ctx, lbName, params, b)
		},
	})
	if err != nil {
		return fmt.Errorf("failed to create dgram bind for UDP LB '%s': %w", lbName, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("create dgram bind for UDP LB '%s' failed with status %d", lbName, resp.StatusCode)
	}
	return nil
}

// DeleteDgramBindUDPLb deletes a dgram bind from a UDP load balancer.
func (u *UDPLBOperations) DeleteDgramBindUDPLb(ctx context.Context, txID, lbName, bindName string) error {
	resp, err := u.client.DispatchEnterpriseOnly(ctx, client.EnterpriseCallFunc[*http.Response]{
		V32EE: func(c *v32ee.Client) (*http.Response, error) {
			params := &v32ee.DeleteDgramBindUDPLbParams{TransactionId: &txID}
			return c.DeleteDgramBindUDPLb(ctx, lbName, bindName, params)
		},
		V31EE: func(c *v31ee.Client) (*http.Response, error) {
			params := &v31ee.DeleteDgramBindUDPLbParams{TransactionId: &txID}
			return c.DeleteDgramBindUDPLb(ctx, lbName, bindName, params)
		},
		V30EE: func(c *v30ee.Client) (*http.Response, error) {
			params := &v30ee.DeleteDgramBindUDPLbParams{TransactionId: &txID}
			return c.DeleteDgramBindUDPLb(ctx, lbName, bindName, params)
		},
	})
	if err != nil {
		return fmt.Errorf("failed to delete dgram bind '%s' from UDP LB '%s': %w", bindName, lbName, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("delete dgram bind '%s' from UDP LB '%s' failed with status %d", bindName, lbName, resp.StatusCode)
	}
	return nil
}

// =============================================================================
// UDP Load Balancer Log Target Operations
// =============================================================================

// LogTarget represents a log target configuration.
type LogTarget = v32ee.LogTarget

// GetAllLogTargetsUDPLb retrieves all log targets for a UDP load balancer.
func (u *UDPLBOperations) GetAllLogTargetsUDPLb(ctx context.Context, txID, lbName string) ([]LogTarget, error) {
	resp, err := u.client.DispatchEnterpriseOnly(ctx, client.EnterpriseCallFunc[*http.Response]{
		V32EE: func(c *v32ee.Client) (*http.Response, error) {
			params := &v32ee.GetAllLogTargetUDPLbParams{TransactionId: &txID}
			return c.GetAllLogTargetUDPLb(ctx, lbName, params)
		},
		V31EE: func(c *v31ee.Client) (*http.Response, error) {
			params := &v31ee.GetAllLogTargetUDPLbParams{TransactionId: &txID}
			return c.GetAllLogTargetUDPLb(ctx, lbName, params)
		},
		V30EE: func(c *v30ee.Client) (*http.Response, error) {
			params := &v30ee.GetAllLogTargetUDPLbParams{TransactionId: &txID}
			return c.GetAllLogTargetUDPLb(ctx, lbName, params)
		},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to get log targets for UDP LB '%s': %w", lbName, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("get log targets for UDP LB '%s' failed with status %d", lbName, resp.StatusCode)
	}

	var result []LogTarget
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode log targets response: %w", err)
	}
	return result, nil
}

// CreateLogTargetUDPLb creates a new log target for a UDP load balancer at the specified index.
func (u *UDPLBOperations) CreateLogTargetUDPLb(ctx context.Context, txID, lbName string, index int, target *LogTarget) error {
	jsonData, err := json.Marshal(target)
	if err != nil {
		return fmt.Errorf("failed to marshal log target: %w", err)
	}

	resp, err := u.client.DispatchEnterpriseOnly(ctx, client.EnterpriseCallFunc[*http.Response]{
		V32EE: func(c *v32ee.Client) (*http.Response, error) {
			var t v32ee.LogTarget
			if err := json.Unmarshal(jsonData, &t); err != nil {
				return nil, err
			}
			params := &v32ee.CreateLogTargetUDPLbParams{TransactionId: &txID}
			return c.CreateLogTargetUDPLb(ctx, lbName, index, params, t)
		},
		V31EE: func(c *v31ee.Client) (*http.Response, error) {
			var t v31ee.LogTarget
			if err := json.Unmarshal(jsonData, &t); err != nil {
				return nil, err
			}
			params := &v31ee.CreateLogTargetUDPLbParams{TransactionId: &txID}
			return c.CreateLogTargetUDPLb(ctx, lbName, index, params, t)
		},
		V30EE: func(c *v30ee.Client) (*http.Response, error) {
			var t v30ee.LogTarget
			if err := json.Unmarshal(jsonData, &t); err != nil {
				return nil, err
			}
			params := &v30ee.CreateLogTargetUDPLbParams{TransactionId: &txID}
			return c.CreateLogTargetUDPLb(ctx, lbName, index, params, t)
		},
	})
	if err != nil {
		return fmt.Errorf("failed to create log target for UDP LB '%s': %w", lbName, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("create log target for UDP LB '%s' failed with status %d", lbName, resp.StatusCode)
	}
	return nil
}

// DeleteLogTargetUDPLb deletes a log target from a UDP load balancer at the specified index.
func (u *UDPLBOperations) DeleteLogTargetUDPLb(ctx context.Context, txID, lbName string, index int) error {
	resp, err := u.client.DispatchEnterpriseOnly(ctx, client.EnterpriseCallFunc[*http.Response]{
		V32EE: func(c *v32ee.Client) (*http.Response, error) {
			params := &v32ee.DeleteLogTargetUDPLbParams{TransactionId: &txID}
			return c.DeleteLogTargetUDPLb(ctx, lbName, index, params)
		},
		V31EE: func(c *v31ee.Client) (*http.Response, error) {
			params := &v31ee.DeleteLogTargetUDPLbParams{TransactionId: &txID}
			return c.DeleteLogTargetUDPLb(ctx, lbName, index, params)
		},
		V30EE: func(c *v30ee.Client) (*http.Response, error) {
			params := &v30ee.DeleteLogTargetUDPLbParams{TransactionId: &txID}
			return c.DeleteLogTargetUDPLb(ctx, lbName, index, params)
		},
	})
	if err != nil {
		return fmt.Errorf("failed to delete log target from UDP LB '%s' at index %d: %w", lbName, index, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("delete log target from UDP LB '%s' at index %d failed with status %d", lbName, index, resp.StatusCode)
	}
	return nil
}

// =============================================================================
// UDP Load Balancer Server Switching Rule Operations
// =============================================================================

// ServerSwitchingRule represents a server switching rule configuration.
type ServerSwitchingRule = v32ee.ServerSwitchingRule

// GetAllServerSwitchingRulesUDPLb retrieves all server switching rules for a UDP load balancer.
// Note: UDP LB server switching rules are only available in HAProxy Enterprise v3.2+.
func (u *UDPLBOperations) GetAllServerSwitchingRulesUDPLb(ctx context.Context, txID, lbName string) ([]ServerSwitchingRule, error) {
	// UDP LB server switching rules are v3.2+ only - check version first
	if u.client.Clientset().MinorVersion() < 2 {
		return nil, ErrUDPLBServerSwitchingRequiresV32
	}

	resp, err := u.client.DispatchEnterpriseOnly(ctx, client.EnterpriseCallFunc[*http.Response]{
		V32EE: func(c *v32ee.Client) (*http.Response, error) {
			params := &v32ee.GetAllServerSwitchingRuleUDPLbParams{TransactionId: &txID}
			return c.GetAllServerSwitchingRuleUDPLb(ctx, lbName, params)
		},
		// V31EE and V30EE don't have UDP LB server switching rule endpoints
	})
	if err != nil {
		return nil, fmt.Errorf("failed to get server switching rules for UDP LB '%s': %w", lbName, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("get server switching rules for UDP LB '%s' failed with status %d", lbName, resp.StatusCode)
	}

	var result []ServerSwitchingRule
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode server switching rules response: %w", err)
	}
	return result, nil
}

// CreateServerSwitchingRuleUDPLb creates a new server switching rule for a UDP load balancer at the specified index.
// Note: UDP LB server switching rules are only available in HAProxy Enterprise v3.2+.
func (u *UDPLBOperations) CreateServerSwitchingRuleUDPLb(ctx context.Context, txID, lbName string, index int, rule *ServerSwitchingRule) error {
	// UDP LB server switching rules are v3.2+ only - check version first
	if u.client.Clientset().MinorVersion() < 2 {
		return ErrUDPLBServerSwitchingRequiresV32
	}

	jsonData, err := json.Marshal(rule)
	if err != nil {
		return fmt.Errorf("failed to marshal server switching rule: %w", err)
	}

	resp, err := u.client.DispatchEnterpriseOnly(ctx, client.EnterpriseCallFunc[*http.Response]{
		V32EE: func(c *v32ee.Client) (*http.Response, error) {
			var r v32ee.ServerSwitchingRule
			if err := json.Unmarshal(jsonData, &r); err != nil {
				return nil, err
			}
			params := &v32ee.CreateServerSwitchingRuleUDPLbParams{TransactionId: &txID}
			return c.CreateServerSwitchingRuleUDPLb(ctx, lbName, index, params, r)
		},
		// V31EE and V30EE don't have UDP LB server switching rule endpoints
	})
	if err != nil {
		return fmt.Errorf("failed to create server switching rule for UDP LB '%s': %w", lbName, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("create server switching rule for UDP LB '%s' failed with status %d", lbName, resp.StatusCode)
	}
	return nil
}

// DeleteServerSwitchingRuleUDPLb deletes a server switching rule from a UDP load balancer at the specified index.
// Note: UDP LB server switching rules are only available in HAProxy Enterprise v3.2+.
func (u *UDPLBOperations) DeleteServerSwitchingRuleUDPLb(ctx context.Context, txID, lbName string, index int) error {
	// UDP LB server switching rules are v3.2+ only - check version first
	if u.client.Clientset().MinorVersion() < 2 {
		return ErrUDPLBServerSwitchingRequiresV32
	}

	resp, err := u.client.DispatchEnterpriseOnly(ctx, client.EnterpriseCallFunc[*http.Response]{
		V32EE: func(c *v32ee.Client) (*http.Response, error) {
			params := &v32ee.DeleteServerSwitchingRuleUDPLbParams{TransactionId: &txID}
			return c.DeleteServerSwitchingRuleUDPLb(ctx, lbName, index, params)
		},
		// V31EE and V30EE don't have UDP LB server switching rule endpoints
	})
	if err != nil {
		return fmt.Errorf("failed to delete server switching rule from UDP LB '%s' at index %d: %w", lbName, index, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("delete server switching rule from UDP LB '%s' at index %d failed with status %d", lbName, index, resp.StatusCode)
	}
	return nil
}
