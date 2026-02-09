package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	v31 "gitlab.com/haproxy-haptic/haptic/pkg/generated/dataplaneapi/v31"
	v31ee "gitlab.com/haproxy-haptic/haptic/pkg/generated/dataplaneapi/v31ee"
	v32 "gitlab.com/haproxy-haptic/haptic/pkg/generated/dataplaneapi/v32"
	v32ee "gitlab.com/haproxy-haptic/haptic/pkg/generated/dataplaneapi/v32ee"
	v33 "gitlab.com/haproxy-haptic/haptic/pkg/generated/dataplaneapi/v33"
)

// Traces represents HAProxy trace events configuration.
// Unlike log profiles which are named resources, traces is a singleton configuration.
type Traces struct {
	Entries []TraceEntry `json:"entries,omitempty"`
}

// TraceEntry represents an individual trace entry configuration.
type TraceEntry struct {
	Name      string `json:"name,omitempty"`
	Event     string `json:"event,omitempty"`
	Level     string `json:"level,omitempty"`
	Lock      string `json:"lock,omitempty"`
	Sink      string `json:"sink,omitempty"`
	State     string `json:"state,omitempty"`
	Verbosity string `json:"verbosity,omitempty"`
}

// GetTraces retrieves the traces configuration.
// Traces configuration is only available in HAProxy DataPlane API v3.1+.
func (c *DataplaneClient) GetTraces(ctx context.Context) (*Traces, error) {
	resp, err := c.DispatchWithCapability(ctx, CallFunc[*http.Response]{
		V33: func(c *v33.Client) (*http.Response, error) {
			return c.GetTraces(ctx, &v33.GetTracesParams{})
		},
		V32: func(c *v32.Client) (*http.Response, error) {
			return c.GetTraces(ctx, &v32.GetTracesParams{})
		},
		V32EE: func(c *v32ee.Client) (*http.Response, error) {
			return c.GetTraces(ctx, &v32ee.GetTracesParams{})
		},
		V31: func(c *v31.Client) (*http.Response, error) {
			return c.GetTraces(ctx, &v31.GetTracesParams{})
		},
		V31EE: func(c *v31ee.Client) (*http.Response, error) {
			return c.GetTraces(ctx, &v31ee.GetTracesParams{})
		},
	}, func(caps Capabilities) error {
		if !caps.SupportsTraces {
			return fmt.Errorf("traces require DataPlane API v3.1+")
		}
		return nil
	})

	if err != nil {
		return nil, fmt.Errorf("failed to get traces: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		// No traces configured, return empty
		return &Traces{}, nil
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("get traces failed with status %d", resp.StatusCode)
	}

	var traces Traces
	if err := json.NewDecoder(resp.Body).Decode(&traces); err != nil {
		return nil, fmt.Errorf("failed to decode traces response: %w", err)
	}

	return &traces, nil
}

// CreateTraces creates the traces configuration.
// This is used when no traces configuration exists yet.
// Traces configuration is only available in HAProxy DataPlane API v3.1+.
func (c *DataplaneClient) CreateTraces(ctx context.Context, traces *Traces, transactionID string) error {
	if !c.clientset.Capabilities().SupportsTraces {
		return fmt.Errorf("traces are not supported by DataPlane API version %s (requires v3.1+)", c.clientset.DetectedVersion())
	}

	jsonData, err := json.Marshal(traces)
	if err != nil {
		return fmt.Errorf("failed to marshal traces: %w", err)
	}

	body := bytes.NewReader(jsonData)

	resp, err := c.DispatchWithCapability(ctx, CallFunc[*http.Response]{
		V33: func(c *v33.Client) (*http.Response, error) {
			return c.CreateTracesWithBody(ctx, &v33.CreateTracesParams{TransactionId: &transactionID}, "application/json", body)
		},
		V32: func(c *v32.Client) (*http.Response, error) {
			return c.CreateTracesWithBody(ctx, &v32.CreateTracesParams{TransactionId: &transactionID}, "application/json", body)
		},
		V32EE: func(c *v32ee.Client) (*http.Response, error) {
			return c.CreateTracesWithBody(ctx, &v32ee.CreateTracesParams{TransactionId: &transactionID}, "application/json", body)
		},
		V31: func(c *v31.Client) (*http.Response, error) {
			return c.CreateTracesWithBody(ctx, &v31.CreateTracesParams{TransactionId: &transactionID}, "application/json", body)
		},
		V31EE: func(c *v31ee.Client) (*http.Response, error) {
			return c.CreateTracesWithBody(ctx, &v31ee.CreateTracesParams{TransactionId: &transactionID}, "application/json", body)
		},
	}, func(caps Capabilities) error {
		if !caps.SupportsTraces {
			return fmt.Errorf("traces require DataPlane API v3.1+")
		}
		return nil
	})

	if err != nil {
		return fmt.Errorf("failed to create traces: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("create traces failed with status %d", resp.StatusCode)
	}

	return nil
}

// ReplaceTraces replaces the traces configuration.
// This is used to update existing traces configuration.
// Traces configuration is only available in HAProxy DataPlane API v3.1+.
func (c *DataplaneClient) ReplaceTraces(ctx context.Context, traces *Traces, transactionID string) error {
	jsonData, err := json.Marshal(traces)
	if err != nil {
		return fmt.Errorf("failed to marshal traces: %w", err)
	}

	body := bytes.NewReader(jsonData)

	resp, err := c.DispatchWithCapability(ctx, CallFunc[*http.Response]{
		V33: func(c *v33.Client) (*http.Response, error) {
			return c.ReplaceTracesWithBody(ctx, &v33.ReplaceTracesParams{TransactionId: &transactionID}, "application/json", body)
		},
		V32: func(c *v32.Client) (*http.Response, error) {
			return c.ReplaceTracesWithBody(ctx, &v32.ReplaceTracesParams{TransactionId: &transactionID}, "application/json", body)
		},
		V32EE: func(c *v32ee.Client) (*http.Response, error) {
			return c.ReplaceTracesWithBody(ctx, &v32ee.ReplaceTracesParams{TransactionId: &transactionID}, "application/json", body)
		},
		V31: func(c *v31.Client) (*http.Response, error) {
			return c.ReplaceTracesWithBody(ctx, &v31.ReplaceTracesParams{TransactionId: &transactionID}, "application/json", body)
		},
		V31EE: func(c *v31ee.Client) (*http.Response, error) {
			return c.ReplaceTracesWithBody(ctx, &v31ee.ReplaceTracesParams{TransactionId: &transactionID}, "application/json", body)
		},
	}, func(caps Capabilities) error {
		if !caps.SupportsTraces {
			return fmt.Errorf("traces require DataPlane API v3.1+")
		}
		return nil
	})

	if err != nil {
		return fmt.Errorf("failed to replace traces: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("replace traces failed with status %d", resp.StatusCode)
	}

	return nil
}

// DeleteTraces deletes the traces configuration.
// Traces configuration is only available in HAProxy DataPlane API v3.1+.
func (c *DataplaneClient) DeleteTraces(ctx context.Context, transactionID string) error {
	resp, err := c.DispatchWithCapability(ctx, CallFunc[*http.Response]{
		V33: func(c *v33.Client) (*http.Response, error) {
			return c.DeleteTraces(ctx, &v33.DeleteTracesParams{TransactionId: &transactionID})
		},
		V32: func(c *v32.Client) (*http.Response, error) {
			return c.DeleteTraces(ctx, &v32.DeleteTracesParams{TransactionId: &transactionID})
		},
		V32EE: func(c *v32ee.Client) (*http.Response, error) {
			return c.DeleteTraces(ctx, &v32ee.DeleteTracesParams{TransactionId: &transactionID})
		},
		V31: func(c *v31.Client) (*http.Response, error) {
			return c.DeleteTraces(ctx, &v31.DeleteTracesParams{TransactionId: &transactionID})
		},
		V31EE: func(c *v31ee.Client) (*http.Response, error) {
			return c.DeleteTraces(ctx, &v31ee.DeleteTracesParams{TransactionId: &transactionID})
		},
	}, func(caps Capabilities) error {
		if !caps.SupportsTraces {
			return fmt.Errorf("traces require DataPlane API v3.1+")
		}
		return nil
	})

	if err != nil {
		return fmt.Errorf("failed to delete traces: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		// Already deleted, not an error
		return nil
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("delete traces failed with status %d", resp.StatusCode)
	}

	return nil
}
