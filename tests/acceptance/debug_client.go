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

//go:build acceptance

package acceptance

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/portforward"
	"k8s.io/client-go/transport/spdy"
)

// DebugClient provides access to the controller's debug HTTP server via port-forward.
type DebugClient struct {
	podName       string
	podNamespace  string
	debugPort     int
	localPort     int
	restConfig    *rest.Config
	stopChannel   chan struct{}
	readyChannel  chan struct{}
	portForwarder *portforward.PortForwarder
}

// NewDebugClient creates a new debug client for the given pod.
func NewDebugClient(restConfig *rest.Config, pod *corev1.Pod, debugPort int) *DebugClient {
	return &DebugClient{
		podName:      pod.Name,
		podNamespace: pod.Namespace,
		debugPort:    debugPort,
		localPort:    0, // Will be assigned by port-forward
		restConfig:   restConfig,
	}
}

// Start starts the port-forward to the pod's debug port.
func (dc *DebugClient) Start(ctx context.Context) error {
	// Create port-forward URL
	path := fmt.Sprintf("/api/v1/namespaces/%s/pods/%s/portforward", dc.podNamespace, dc.podName)
	hostURL := dc.restConfig.Host + path

	transport, upgrader, err := spdy.RoundTripperFor(dc.restConfig)
	if err != nil {
		return fmt.Errorf("failed to create round tripper: %w", err)
	}

	parsedURL, err := url.Parse(hostURL)
	if err != nil {
		return fmt.Errorf("failed to parse URL: %w", err)
	}

	dialer := spdy.NewDialer(upgrader, &http.Client{Transport: transport}, "POST", parsedURL)

	dc.stopChannel = make(chan struct{}, 1)
	dc.readyChannel = make(chan struct{})

	// Port 0 means pick random local port
	ports := []string{fmt.Sprintf("0:%d", dc.debugPort)}

	dc.portForwarder, err = portforward.New(
		dialer,
		ports,
		dc.stopChannel,
		dc.readyChannel,
		io.Discard,
		io.Discard,
	)
	if err != nil {
		return fmt.Errorf("failed to create port forwarder: %w", err)
	}

	// Start port-forward in background
	errChan := make(chan error)
	go func() {
		errChan <- dc.portForwarder.ForwardPorts()
	}()

	// Wait for port-forward to be ready or error
	select {
	case <-dc.readyChannel:
		// Get the assigned local port
		forwardedPorts, err := dc.portForwarder.GetPorts()
		if err != nil {
			dc.Stop()
			return fmt.Errorf("failed to get forwarded ports: %w", err)
		}
		dc.localPort = int(forwardedPorts[0].Local)
		return nil

	case err := <-errChan:
		return fmt.Errorf("port-forward failed: %w", err)

	case <-ctx.Done():
		dc.Stop()
		return ctx.Err()

	case <-time.After(30 * time.Second):
		dc.Stop()
		return fmt.Errorf("port-forward timeout")
	}
}

// Stop stops the port-forward. Safe to call multiple times.
func (dc *DebugClient) Stop() {
	if dc.stopChannel != nil {
		close(dc.stopChannel)
		dc.stopChannel = nil
	}
}

// Reconnect closes the existing port-forward and establishes a new one.
// This is useful when the port-forward connection has died due to network issues
// or pod restarts.
func (dc *DebugClient) Reconnect(ctx context.Context) error {
	// Stop existing port-forward
	dc.Stop()
	dc.stopChannel = nil
	dc.readyChannel = nil
	dc.portForwarder = nil
	dc.localPort = 0

	// Start a new port-forward
	return dc.Start(ctx)
}

// isConnectionError checks if an error indicates the port-forward connection is dead.
func isConnectionError(err error) bool {
	if err == nil {
		return false
	}
	errStr := err.Error()
	return strings.Contains(errStr, "connection refused") ||
		strings.Contains(errStr, "connection reset") ||
		strings.Contains(errStr, "broken pipe") ||
		strings.Contains(errStr, "EOF") ||
		strings.Contains(errStr, "no such host") ||
		strings.Contains(errStr, "context deadline exceeded") ||
		strings.Contains(errStr, "i/o timeout")
}

// StartWithRetry starts the port-forward with retry logic for transient failures.
// This is useful when the controller pod may have restarted and the connection
// needs time to become available.
func (dc *DebugClient) StartWithRetry(ctx context.Context, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	var lastErr error
	backoff := 1 * time.Second

	for time.Now().Before(deadline) {
		err := dc.Start(ctx)
		if err == nil {
			return nil
		}
		lastErr = err

		// Reset channels for retry (Start() may have partially initialized them)
		dc.Stop()
		dc.stopChannel = nil
		dc.readyChannel = nil
		dc.portForwarder = nil

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(backoff):
			backoff = min(backoff*2, 5*time.Second)
		}
	}

	return fmt.Errorf("failed to start debug client after retries: %w", lastErr)
}

// GetConfig retrieves the current controller configuration from the debug server.
func (dc *DebugClient) GetConfig(ctx context.Context) (map[string]interface{}, error) {
	url := fmt.Sprintf("http://localhost:%d/debug/vars/config", dc.localPort)
	return dc.getJSON(ctx, url)
}

// GetRenderedConfig retrieves the rendered HAProxy configuration.
func (dc *DebugClient) GetRenderedConfig(ctx context.Context) (string, error) {
	url := fmt.Sprintf("http://localhost:%d/debug/vars/rendered", dc.localPort)

	data, err := dc.getJSON(ctx, url)
	if err != nil {
		return "", err
	}

	// Extract config string from response
	if config, ok := data["config"].(string); ok {
		return config, nil
	}

	return "", fmt.Errorf("rendered config not found in response")
}

// GetRenderedConfigWithRetry retrieves the rendered HAProxy configuration with retry logic.
// This is useful in parallel test execution where port-forward connections may be transiently unavailable.
// If a connection error is detected, it will attempt to reconnect the port-forward.
func (dc *DebugClient) GetRenderedConfigWithRetry(ctx context.Context, timeout time.Duration) (string, error) {
	deadline := time.Now().Add(timeout)
	var lastErr error

	for time.Now().Before(deadline) {
		config, err := dc.GetRenderedConfig(ctx)
		if err == nil {
			return config, nil
		}
		lastErr = err

		// If it's a connection error, try to reconnect the port-forward
		if isConnectionError(err) {
			if reconnErr := dc.Reconnect(ctx); reconnErr != nil {
				// Log reconnect failure but continue retrying
				lastErr = fmt.Errorf("reconnect failed: %w (original error: %v)", reconnErr, err)
			}
		}

		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(1 * time.Second):
			// Retry after 1 second
		}
	}

	return "", fmt.Errorf("timeout waiting for rendered config: %w", lastErr)
}

// GetEvents retrieves recent events from the debug server.
func (dc *DebugClient) GetEvents(ctx context.Context) ([]map[string]interface{}, error) {
	url := fmt.Sprintf("http://localhost:%d/debug/vars/events", dc.localPort)

	data, err := dc.getJSON(ctx, url)
	if err != nil {
		return nil, err
	}

	// Response is an array of events
	if events, ok := data["events"].([]interface{}); ok {
		result := make([]map[string]interface{}, 0, len(events))
		for _, e := range events {
			if eventMap, ok := e.(map[string]interface{}); ok {
				result = append(result, eventMap)
			}
		}
		return result, nil
	}

	return nil, fmt.Errorf("events not found in response")
}

// WaitForConfig waits for the controller configuration to become available.
//
// This is useful during controller startup when the debug endpoint is running
// but configuration hasn't been loaded yet. The method polls the debug endpoint
// until configuration is available or the timeout expires.
// If a connection error is detected, it will attempt to reconnect the port-forward.
func (dc *DebugClient) WaitForConfig(ctx context.Context, timeout time.Duration) (map[string]interface{}, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	var lastErr error
	for {
		select {
		case <-ctx.Done():
			if lastErr != nil {
				return nil, fmt.Errorf("timeout waiting for config to become available (last error: %w)", lastErr)
			}
			return nil, fmt.Errorf("timeout waiting for config to become available")

		case <-ticker.C:
			config, err := dc.GetConfig(ctx)
			if err != nil {
				lastErr = err
				// If it's a connection error, try to reconnect the port-forward
				if isConnectionError(err) {
					if reconnErr := dc.Reconnect(ctx); reconnErr != nil {
						lastErr = fmt.Errorf("reconnect failed: %w (original error: %v)", reconnErr, err)
					}
				}
				continue // Retry on error
			}

			// Config is available
			return config, nil
		}
	}
}

// WaitForConfigVersion waits for the controller to load a specific config version.
// If a connection error is detected, it will attempt to reconnect the port-forward.
func (dc *DebugClient) WaitForConfigVersion(ctx context.Context, expectedVersion string, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	var lastErr error
	for {
		select {
		case <-ctx.Done():
			if lastErr != nil {
				return fmt.Errorf("timeout waiting for config version %q (last error: %w)", expectedVersion, lastErr)
			}
			return fmt.Errorf("timeout waiting for config version %q", expectedVersion)

		case <-ticker.C:
			config, err := dc.GetConfig(ctx)
			if err != nil {
				lastErr = err
				// If it's a connection error, try to reconnect the port-forward
				if isConnectionError(err) {
					if reconnErr := dc.Reconnect(ctx); reconnErr != nil {
						lastErr = fmt.Errorf("reconnect failed: %w (original error: %v)", reconnErr, err)
					}
				}
				continue // Retry on error
			}

			if version, ok := config["version"].(string); ok && version == expectedVersion {
				return nil
			}
		}
	}
}

// WaitForRenderedConfigContains waits for the rendered config to contain a specific string.
// If a connection error is detected, it will attempt to reconnect the port-forward.
func (dc *DebugClient) WaitForRenderedConfigContains(ctx context.Context, expectedSubstring string, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	var lastErr error
	for {
		select {
		case <-ctx.Done():
			if lastErr != nil {
				return fmt.Errorf("timeout waiting for rendered config to contain %q (last error: %w)", expectedSubstring, lastErr)
			}
			return fmt.Errorf("timeout waiting for rendered config to contain %q", expectedSubstring)

		case <-ticker.C:
			rendered, err := dc.GetRenderedConfig(ctx)
			if err != nil {
				lastErr = err
				// If it's a connection error, try to reconnect the port-forward
				if isConnectionError(err) {
					if reconnErr := dc.Reconnect(ctx); reconnErr != nil {
						lastErr = fmt.Errorf("reconnect failed: %w (original error: %v)", reconnErr, err)
					}
				}
				continue // Retry on error
			}

			if contains(rendered, expectedSubstring) {
				return nil
			}
		}
	}
}

// WaitForRenderedConfigContainsAny waits for the rendered config to contain any of the expected strings.
// If a connection error is detected, it will attempt to reconnect the port-forward.
// On timeout, returns an error with the last seen config for debugging.
func (dc *DebugClient) WaitForRenderedConfigContainsAny(ctx context.Context, expectedSubstrings []string, timeout time.Duration) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	var lastErr error
	var lastConfig string
	for {
		select {
		case <-ctx.Done():
			errMsg := fmt.Sprintf("timeout waiting for rendered config to contain any of %v", expectedSubstrings)
			if lastConfig != "" {
				errMsg += fmt.Sprintf("\nlast seen config:\n%s", lastConfig)
			}
			if lastErr != nil {
				return "", fmt.Errorf("%s (last error: %w)", errMsg, lastErr)
			}
			return "", fmt.Errorf("%s", errMsg)

		case <-ticker.C:
			rendered, err := dc.GetRenderedConfig(ctx)
			if err != nil {
				lastErr = err
				// If it's a connection error, try to reconnect the port-forward
				if isConnectionError(err) {
					if reconnErr := dc.Reconnect(ctx); reconnErr != nil {
						lastErr = fmt.Errorf("reconnect failed: %w (original error: %v)", reconnErr, err)
					}
				}
				continue // Retry on error
			}

			lastConfig = rendered
			for _, expected := range expectedSubstrings {
				if contains(rendered, expected) {
					return expected, nil
				}
			}
		}
	}
}

// PipelineStatus represents the reconciliation pipeline status.
type PipelineStatus struct {
	LastTrigger *TriggerStatus    `json:"last_trigger"`
	Rendering   *RenderingStatus  `json:"rendering"`
	Validation  *ValidationStatus `json:"validation"`
	Deployment  *DeploymentStatus `json:"deployment"`
}

// TriggerStatus represents what triggered the last reconciliation.
type TriggerStatus struct {
	Timestamp string `json:"timestamp"`
	Reason    string `json:"reason"`
}

// RenderingStatus represents the template rendering phase status.
type RenderingStatus struct {
	Status      string `json:"status"`
	Timestamp   string `json:"timestamp"`
	DurationMs  int64  `json:"duration_ms"`
	ConfigBytes int    `json:"config_bytes"`
	Error       string `json:"error,omitempty"`
}

// ValidationStatus represents the HAProxy validation phase status.
type ValidationStatus struct {
	Status     string   `json:"status"`
	Timestamp  string   `json:"timestamp"`
	DurationMs int64    `json:"duration_ms"`
	Errors     []string `json:"errors,omitempty"`
	Warnings   []string `json:"warnings,omitempty"`
}

// DeploymentStatus represents the deployment phase status.
type DeploymentStatus struct {
	Status             string           `json:"status"`
	Reason             string           `json:"reason,omitempty"`
	Timestamp          string           `json:"timestamp"`
	DurationMs         int64            `json:"duration_ms,omitempty"`
	EndpointsTotal     int              `json:"endpoints_total"`
	EndpointsSucceeded int              `json:"endpoints_succeeded"`
	EndpointsFailed    int              `json:"endpoints_failed"`
	FailedEndpoints    []FailedEndpoint `json:"failed_endpoints,omitempty"`
}

// FailedEndpoint contains details about a failed deployment endpoint.
type FailedEndpoint struct {
	URL   string `json:"url"`
	Error string `json:"error"`
}

// ValidatedConfigInfo contains information about the last successfully validated config.
type ValidatedConfigInfo struct {
	Config               string `json:"config"`
	Timestamp            string `json:"timestamp"`
	ConfigBytes          int    `json:"config_bytes"`
	ValidationDurationMs int64  `json:"validation_duration_ms"`
}

// ErrorSummary provides an aggregated view of recent errors.
type ErrorSummary struct {
	ConfigParseError       *ErrorInfo  `json:"config_parse_error,omitempty"`
	TemplateRenderError    *ErrorInfo  `json:"template_render_error,omitempty"`
	HAProxyValidationError *ErrorInfo  `json:"haproxy_validation_error,omitempty"`
	DeploymentErrors       []ErrorInfo `json:"deployment_errors,omitempty"`
	LastErrorTimestamp     string      `json:"last_error_timestamp,omitempty"`
}

// ErrorInfo contains details about a specific error.
type ErrorInfo struct {
	Timestamp string   `json:"timestamp"`
	Errors    []string `json:"errors"`
}

// GetPipelineStatus retrieves the reconciliation pipeline status.
func (dc *DebugClient) GetPipelineStatus(ctx context.Context) (*PipelineStatus, error) {
	url := fmt.Sprintf("http://localhost:%d/debug/vars/pipeline", dc.localPort)

	data, err := dc.getJSON(ctx, url)
	if err != nil {
		return nil, err
	}

	// Re-marshal and unmarshal to convert map to struct
	jsonBytes, err := json.Marshal(data)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal pipeline status: %w", err)
	}

	var status PipelineStatus
	if err := json.Unmarshal(jsonBytes, &status); err != nil {
		return nil, fmt.Errorf("failed to unmarshal pipeline status: %w", err)
	}

	return &status, nil
}

// GetPipelineStatusWithRetry retrieves the pipeline status with retry logic.
// This is useful in parallel test execution where port-forward connections may be transiently unavailable.
// If a connection error is detected, it will attempt to reconnect the port-forward.
func (dc *DebugClient) GetPipelineStatusWithRetry(ctx context.Context, timeout time.Duration) (*PipelineStatus, error) {
	deadline := time.Now().Add(timeout)
	var lastErr error

	for time.Now().Before(deadline) {
		status, err := dc.GetPipelineStatus(ctx)
		if err == nil {
			return status, nil
		}
		lastErr = err

		// If it's a connection error, try to reconnect the port-forward
		if isConnectionError(err) {
			if reconnErr := dc.Reconnect(ctx); reconnErr != nil {
				lastErr = fmt.Errorf("reconnect failed: %w (original error: %v)", reconnErr, err)
			}
		}

		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(1 * time.Second):
			// Retry after 1 second
		}
	}

	return nil, fmt.Errorf("timeout waiting for pipeline status: %w", lastErr)
}

// GetValidatedConfig retrieves the last successfully validated HAProxy configuration.
func (dc *DebugClient) GetValidatedConfig(ctx context.Context) (*ValidatedConfigInfo, error) {
	url := fmt.Sprintf("http://localhost:%d/debug/vars/validated", dc.localPort)

	data, err := dc.getJSON(ctx, url)
	if err != nil {
		return nil, err
	}

	// Re-marshal and unmarshal to convert map to struct
	jsonBytes, err := json.Marshal(data)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal validated config: %w", err)
	}

	var info ValidatedConfigInfo
	if err := json.Unmarshal(jsonBytes, &info); err != nil {
		return nil, fmt.Errorf("failed to unmarshal validated config: %w", err)
	}

	return &info, nil
}

// GetErrors retrieves the error summary.
func (dc *DebugClient) GetErrors(ctx context.Context) (*ErrorSummary, error) {
	url := fmt.Sprintf("http://localhost:%d/debug/vars/errors", dc.localPort)

	data, err := dc.getJSON(ctx, url)
	if err != nil {
		return nil, err
	}

	// Re-marshal and unmarshal to convert map to struct
	jsonBytes, err := json.Marshal(data)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal error summary: %w", err)
	}

	var summary ErrorSummary
	if err := json.Unmarshal(jsonBytes, &summary); err != nil {
		return nil, fmt.Errorf("failed to unmarshal error summary: %w", err)
	}

	return &summary, nil
}

// WaitForValidationStatus waits for the validation status to match the expected value.
// If a connection error is detected, it will attempt to reconnect the port-forward.
func (dc *DebugClient) WaitForValidationStatus(ctx context.Context, expected string, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	var lastErr error
	for {
		select {
		case <-ctx.Done():
			if lastErr != nil {
				return fmt.Errorf("timeout waiting for validation status %q (last error: %w)", expected, lastErr)
			}
			return fmt.Errorf("timeout waiting for validation status %q", expected)

		case <-ticker.C:
			status, err := dc.GetPipelineStatus(ctx)
			if err != nil {
				lastErr = err
				// If it's a connection error, try to reconnect the port-forward
				if isConnectionError(err) {
					if reconnErr := dc.Reconnect(ctx); reconnErr != nil {
						lastErr = fmt.Errorf("reconnect failed: %w (original error: %v)", reconnErr, err)
					}
				}
				continue // Retry on error
			}

			if status.Validation != nil && status.Validation.Status == expected {
				return nil
			}
		}
	}
}

// getJSON fetches JSON from the debug server.
func (dc *DebugClient) getJSON(ctx context.Context, url string) (map[string]interface{}, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", url, http.NoBody)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	client := &http.Client{
		Timeout: 10 * time.Second,
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch from debug server: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("debug server returned status %d: %s", resp.StatusCode, string(body))
	}

	var data map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return nil, fmt.Errorf("failed to decode JSON response: %w", err)
	}

	return data, nil
}

// contains checks if a string contains a substring.
func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > len(substr) && indexOf(s, substr) >= 0)
}

// indexOf returns the index of substr in s, or -1 if not found.
func indexOf(s, substr string) int {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}
