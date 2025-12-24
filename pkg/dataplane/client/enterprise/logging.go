package enterprise

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"gitlab.com/haproxy-template-ic/haproxy-template-ingress-controller/pkg/dataplane/client"
	v30ee "gitlab.com/haproxy-template-ic/haproxy-template-ingress-controller/pkg/generated/dataplaneapi/v30ee"
	v31ee "gitlab.com/haproxy-template-ic/haproxy-template-ingress-controller/pkg/generated/dataplaneapi/v31ee"
	v32ee "gitlab.com/haproxy-template-ic/haproxy-template-ingress-controller/pkg/generated/dataplaneapi/v32ee"
)

// LoggingOperations provides operations for HAProxy Enterprise advanced logging.
type LoggingOperations struct {
	client *client.DataplaneClient
}

// NewLoggingOperations creates a new logging operations client.
func NewLoggingOperations(c *client.DataplaneClient) *LoggingOperations {
	return &LoggingOperations{client: c}
}

// LogConfiguration represents the HAProxy logging configuration.
type LogConfiguration = v32ee.LogConfiguration

// GetLogConfig retrieves the current log configuration.
func (l *LoggingOperations) GetLogConfig(ctx context.Context) (*LogConfiguration, error) {
	resp, err := l.client.DispatchEnterpriseOnly(ctx, client.EnterpriseCallFunc[*http.Response]{
		V32EE: func(c *v32ee.Client) (*http.Response, error) {
			return c.GetLogConfig(ctx)
		},
		V31EE: func(c *v31ee.Client) (*http.Response, error) {
			return c.GetLogConfig(ctx)
		},
		V30EE: func(c *v30ee.Client) (*http.Response, error) {
			return c.GetLogConfig(ctx)
		},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to get log config: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("get log config failed with status %d", resp.StatusCode)
	}

	var result LogConfiguration
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode log config response: %w", err)
	}
	return &result, nil
}

// ReplaceLogConfig replaces the log configuration.
func (l *LoggingOperations) ReplaceLogConfig(ctx context.Context, config *LogConfiguration) error {
	jsonData, err := json.Marshal(config)
	if err != nil {
		return fmt.Errorf("failed to marshal log config: %w", err)
	}

	resp, err := l.client.DispatchEnterpriseOnly(ctx, client.EnterpriseCallFunc[*http.Response]{
		V32EE: func(c *v32ee.Client) (*http.Response, error) {
			var cfg v32ee.LogConfiguration
			if err := json.Unmarshal(jsonData, &cfg); err != nil {
				return nil, err
			}
			return c.ReplaceLogConfig(ctx, cfg)
		},
		V31EE: func(c *v31ee.Client) (*http.Response, error) {
			var cfg v31ee.LogConfiguration
			if err := json.Unmarshal(jsonData, &cfg); err != nil {
				return nil, err
			}
			return c.ReplaceLogConfig(ctx, cfg)
		},
		V30EE: func(c *v30ee.Client) (*http.Response, error) {
			var cfg v30ee.LogConfiguration
			if err := json.Unmarshal(jsonData, &cfg); err != nil {
				return nil, err
			}
			return c.ReplaceLogConfig(ctx, cfg)
		},
	})
	if err != nil {
		return fmt.Errorf("failed to replace log config: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("replace log config failed with status %d", resp.StatusCode)
	}
	return nil
}
