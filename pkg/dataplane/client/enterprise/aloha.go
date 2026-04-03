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

// ALOHAOperations provides operations for HAProxy Enterprise ALOHA features.
type ALOHAOperations struct {
	client *client.DataplaneClient
}

// NewALOHAOperations creates a new ALOHA operations client.
func NewALOHAOperations(c *client.DataplaneClient) *ALOHAOperations {
	return &ALOHAOperations{client: c}
}

// ALOHAEndpoints represents ALOHA endpoint information.
type ALOHAEndpoints = v32ee.Endpoints

// GetEndpoints retrieves ALOHA endpoint information.
func (a *ALOHAOperations) GetEndpoints(ctx context.Context) (*ALOHAEndpoints, error) {
	resp, err := a.client.DispatchEnterpriseOnly(ctx, client.EnterpriseCallFunc[*http.Response]{
		V32EE: func(c *v32ee.Client) (*http.Response, error) {
			return c.GetAlohaEndpoints(ctx)
		},
		V31EE: func(c *v31ee.Client) (*http.Response, error) {
			return c.GetAlohaEndpoints(ctx)
		},
		V30EE: func(c *v30ee.Client) (*http.Response, error) {
			return c.GetAlohaEndpoints(ctx)
		},
	})
	if err != nil {
		return nil, fmt.Errorf("getting ALOHA endpoints: %w", err)
	}
	defer resp.Body.Close()

	return decodeResponse[ALOHAEndpoints](resp, "failed to get ALOHA endpoints")
}

// ALOHAAction represents an ALOHA action.
type ALOHAAction = v32ee.AlohaAction

// GetAllActions retrieves all available ALOHA actions.
func (a *ALOHAOperations) GetAllActions(ctx context.Context) ([]ALOHAAction, error) {
	resp, err := a.client.DispatchEnterpriseOnly(ctx, client.EnterpriseCallFunc[*http.Response]{
		V32EE: func(c *v32ee.Client) (*http.Response, error) {
			return c.GetAlohaActions(ctx)
		},
		V31EE: func(c *v31ee.Client) (*http.Response, error) {
			return c.GetAlohaActions(ctx)
		},
		V30EE: func(c *v30ee.Client) (*http.Response, error) {
			return c.GetAlohaActions(ctx)
		},
	})
	if err != nil {
		return nil, fmt.Errorf("getting ALOHA actions: %w", err)
	}
	defer resp.Body.Close()

	return decodeSliceResponse[ALOHAAction](resp, "failed to get ALOHA actions")
}

// GetAction retrieves a specific ALOHA action by ID.
func (a *ALOHAOperations) GetAction(ctx context.Context, id string) (*ALOHAAction, error) {
	resp, err := a.client.DispatchEnterpriseOnly(ctx, client.EnterpriseCallFunc[*http.Response]{
		V32EE: func(c *v32ee.Client) (*http.Response, error) {
			return c.GetAlohaAction(ctx, id)
		},
		V31EE: func(c *v31ee.Client) (*http.Response, error) {
			return c.GetAlohaAction(ctx, id)
		},
		V30EE: func(c *v30ee.Client) (*http.Response, error) {
			return c.GetAlohaAction(ctx, id)
		},
	})
	if err != nil {
		return nil, fmt.Errorf("getting ALOHA action '%s': %w", id, err)
	}
	defer resp.Body.Close()

	return decodeResponseOr404[ALOHAAction](resp, fmt.Sprintf("getting ALOHA action '%s'", id))
}

// ExecuteAction executes an ALOHA action.
func (a *ALOHAOperations) ExecuteAction(ctx context.Context, action *ALOHAAction) (*ALOHAAction, error) {
	jsonData, err := json.Marshal(action)
	if err != nil {
		return nil, fmt.Errorf("marshalling ALOHA action: %w", err)
	}

	resp, err := a.client.DispatchEnterpriseOnly(ctx, client.EnterpriseCallFunc[*http.Response]{
		V32EE: func(c *v32ee.Client) (*http.Response, error) {
			var act v32ee.AlohaAction
			if err := json.Unmarshal(jsonData, &act); err != nil {
				return nil, err
			}
			return c.ExecuteAlohaAction(ctx, act)
		},
		V31EE: func(c *v31ee.Client) (*http.Response, error) {
			var act v31ee.AlohaAction
			if err := json.Unmarshal(jsonData, &act); err != nil {
				return nil, err
			}
			return c.ExecuteAlohaAction(ctx, act)
		},
		V30EE: func(c *v30ee.Client) (*http.Response, error) {
			var act v30ee.AlohaAction
			if err := json.Unmarshal(jsonData, &act); err != nil {
				return nil, err
			}
			return c.ExecuteAlohaAction(ctx, act)
		},
	})
	if err != nil {
		return nil, fmt.Errorf("executing ALOHA action: %w", err)
	}
	defer resp.Body.Close()

	return decodeResponse[ALOHAAction](resp, "failed to execute ALOHA action")
}
