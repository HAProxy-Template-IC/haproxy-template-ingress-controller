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

// DynamicUpdateOperations provides operations for HAProxy Enterprise dynamic update feature.
type DynamicUpdateOperations struct {
	client *client.DataplaneClient
}

// NewDynamicUpdateOperations creates a new dynamic update operations client.
func NewDynamicUpdateOperations(c *client.DataplaneClient) *DynamicUpdateOperations {
	return &DynamicUpdateOperations{client: c}
}

// GetSection checks if the dynamic update section exists.
func (d *DynamicUpdateOperations) GetSection(ctx context.Context, txID string) error {
	resp, err := d.client.DispatchEnterpriseOnly(ctx, client.EnterpriseCallFunc[*http.Response]{
		V32EE: func(c *v32ee.Client) (*http.Response, error) {
			params := &v32ee.GetDynamicUpdateSectionParams{TransactionId: &txID}
			return c.GetDynamicUpdateSection(ctx, params)
		},
		V31EE: func(c *v31ee.Client) (*http.Response, error) {
			params := &v31ee.GetDynamicUpdateSectionParams{TransactionId: &txID}
			return c.GetDynamicUpdateSection(ctx, params)
		},
		V30EE: func(c *v30ee.Client) (*http.Response, error) {
			params := &v30ee.GetDynamicUpdateSectionParams{TransactionId: &txID}
			return c.GetDynamicUpdateSection(ctx, params)
		},
	})
	if err != nil {
		return fmt.Errorf("getting dynamic update section: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return ErrNotFound
	}
	return checkResponseStatus(resp, "failed to get dynamic update section")
}

// CreateSection creates the dynamic update section.
func (d *DynamicUpdateOperations) CreateSection(ctx context.Context, txID string) error {
	resp, err := d.client.DispatchEnterpriseOnly(ctx, client.EnterpriseCallFunc[*http.Response]{
		V32EE: func(c *v32ee.Client) (*http.Response, error) {
			params := &v32ee.CreateDynamicUpdateSectionParams{TransactionId: &txID}
			return c.CreateDynamicUpdateSection(ctx, params)
		},
		V31EE: func(c *v31ee.Client) (*http.Response, error) {
			params := &v31ee.CreateDynamicUpdateSectionParams{TransactionId: &txID}
			return c.CreateDynamicUpdateSection(ctx, params)
		},
		V30EE: func(c *v30ee.Client) (*http.Response, error) {
			params := &v30ee.CreateDynamicUpdateSectionParams{TransactionId: &txID}
			return c.CreateDynamicUpdateSection(ctx, params)
		},
	})
	if err != nil {
		return fmt.Errorf("creating dynamic update section: %w", err)
	}
	defer resp.Body.Close()

	return checkResponseStatus(resp, "failed to create dynamic update section")
}

// DeleteSection deletes the dynamic update section.
func (d *DynamicUpdateOperations) DeleteSection(ctx context.Context, txID string) error {
	resp, err := d.client.DispatchEnterpriseOnly(ctx, client.EnterpriseCallFunc[*http.Response]{
		V32EE: func(c *v32ee.Client) (*http.Response, error) {
			params := &v32ee.DeleteDynamicUpdateSectionParams{TransactionId: &txID}
			return c.DeleteDynamicUpdateSection(ctx, params)
		},
		V31EE: func(c *v31ee.Client) (*http.Response, error) {
			params := &v31ee.DeleteDynamicUpdateSectionParams{TransactionId: &txID}
			return c.DeleteDynamicUpdateSection(ctx, params)
		},
		V30EE: func(c *v30ee.Client) (*http.Response, error) {
			params := &v30ee.DeleteDynamicUpdateSectionParams{TransactionId: &txID}
			return c.DeleteDynamicUpdateSection(ctx, params)
		},
	})
	if err != nil {
		return fmt.Errorf("deleting dynamic update section: %w", err)
	}
	defer resp.Body.Close()

	return checkResponseStatus(resp, "failed to delete dynamic update section")
}

// DynamicUpdateRule represents a dynamic update rule.
type DynamicUpdateRule = v32ee.DynamicUpdateRule

// GetAllRules retrieves all dynamic update rules.
func (d *DynamicUpdateOperations) GetAllRules(ctx context.Context, txID string) ([]DynamicUpdateRule, error) {
	resp, err := d.client.DispatchEnterpriseOnly(ctx, client.EnterpriseCallFunc[*http.Response]{
		V32EE: func(c *v32ee.Client) (*http.Response, error) {
			params := &v32ee.GetDynamicUpdateRulesParams{TransactionId: &txID}
			return c.GetDynamicUpdateRules(ctx, params)
		},
		V31EE: func(c *v31ee.Client) (*http.Response, error) {
			params := &v31ee.GetDynamicUpdateRulesParams{TransactionId: &txID}
			return c.GetDynamicUpdateRules(ctx, params)
		},
		V30EE: func(c *v30ee.Client) (*http.Response, error) {
			params := &v30ee.GetDynamicUpdateRulesParams{TransactionId: &txID}
			return c.GetDynamicUpdateRules(ctx, params)
		},
	})
	if err != nil {
		return nil, fmt.Errorf("getting dynamic update rules: %w", err)
	}
	defer resp.Body.Close()

	// 404 means dynamic-update section doesn't exist - return empty list
	if resp.StatusCode == http.StatusNotFound {
		return []DynamicUpdateRule{}, nil
	}

	if err := checkResponseStatus(resp, "failed to get dynamic update rules"); err != nil {
		return nil, err
	}

	var result []DynamicUpdateRule
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decoding dynamic update rules response: %w", err)
	}
	return result, nil
}

// GetRule retrieves a specific dynamic update rule by index.
func (d *DynamicUpdateOperations) GetRule(ctx context.Context, txID string, index int) (*DynamicUpdateRule, error) {
	resp, err := d.client.DispatchEnterpriseOnly(ctx, client.EnterpriseCallFunc[*http.Response]{
		V32EE: func(c *v32ee.Client) (*http.Response, error) {
			params := &v32ee.GetDynamicUpdateRuleParams{TransactionId: &txID}
			return c.GetDynamicUpdateRule(ctx, index, params)
		},
		V31EE: func(c *v31ee.Client) (*http.Response, error) {
			params := &v31ee.GetDynamicUpdateRuleParams{TransactionId: &txID}
			return c.GetDynamicUpdateRule(ctx, index, params)
		},
		V30EE: func(c *v30ee.Client) (*http.Response, error) {
			params := &v30ee.GetDynamicUpdateRuleParams{TransactionId: &txID}
			return c.GetDynamicUpdateRule(ctx, index, params)
		},
	})
	if err != nil {
		return nil, fmt.Errorf("getting dynamic update rule at index %d: %w", index, err)
	}
	defer resp.Body.Close()

	return decodeResponseOr404[DynamicUpdateRule](resp, fmt.Sprintf("getting dynamic update rule at index %d", index))
}

// CreateRule creates a new dynamic update rule at the specified index.
func (d *DynamicUpdateOperations) CreateRule(ctx context.Context, txID string, index int, rule *DynamicUpdateRule) error {
	jsonData, err := json.Marshal(rule)
	if err != nil {
		return fmt.Errorf("marshalling dynamic update rule: %w", err)
	}

	resp, err := d.client.DispatchEnterpriseOnly(ctx, client.EnterpriseCallFunc[*http.Response]{
		V32EE: func(c *v32ee.Client) (*http.Response, error) {
			var r v32ee.DynamicUpdateRule
			if err := json.Unmarshal(jsonData, &r); err != nil {
				return nil, err
			}
			params := &v32ee.CreateDynamicUpdateRuleParams{TransactionId: &txID}
			return c.CreateDynamicUpdateRule(ctx, index, params, r)
		},
		V31EE: func(c *v31ee.Client) (*http.Response, error) {
			var r v31ee.DynamicUpdateRule
			if err := json.Unmarshal(jsonData, &r); err != nil {
				return nil, err
			}
			params := &v31ee.CreateDynamicUpdateRuleParams{TransactionId: &txID}
			return c.CreateDynamicUpdateRule(ctx, index, params, r)
		},
		V30EE: func(c *v30ee.Client) (*http.Response, error) {
			var r v30ee.DynamicUpdateRule
			if err := json.Unmarshal(jsonData, &r); err != nil {
				return nil, err
			}
			params := &v30ee.CreateDynamicUpdateRuleParams{TransactionId: &txID}
			return c.CreateDynamicUpdateRule(ctx, index, params, r)
		},
	})
	if err != nil {
		return fmt.Errorf("creating dynamic update rule: %w", err)
	}
	defer resp.Body.Close()

	return checkResponseStatus(resp, "failed to create dynamic update rule")
}

// DeleteRule deletes a dynamic update rule at the specified index.
func (d *DynamicUpdateOperations) DeleteRule(ctx context.Context, txID string, index int) error {
	resp, err := d.client.DispatchEnterpriseOnly(ctx, client.EnterpriseCallFunc[*http.Response]{
		V32EE: func(c *v32ee.Client) (*http.Response, error) {
			params := &v32ee.DeleteDynamicUpdateRuleParams{TransactionId: &txID}
			return c.DeleteDynamicUpdateRule(ctx, index, params)
		},
		V31EE: func(c *v31ee.Client) (*http.Response, error) {
			params := &v31ee.DeleteDynamicUpdateRuleParams{TransactionId: &txID}
			return c.DeleteDynamicUpdateRule(ctx, index, params)
		},
		V30EE: func(c *v30ee.Client) (*http.Response, error) {
			params := &v30ee.DeleteDynamicUpdateRuleParams{TransactionId: &txID}
			return c.DeleteDynamicUpdateRule(ctx, index, params)
		},
	})
	if err != nil {
		return fmt.Errorf("deleting dynamic update rule at index %d: %w", index, err)
	}
	defer resp.Body.Close()

	return checkResponseStatus(resp, fmt.Sprintf("deleting dynamic update rule at index %d", index))
}
