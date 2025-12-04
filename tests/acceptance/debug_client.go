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
	"strconv"
	"strings"
	"time"

	"k8s.io/client-go/kubernetes"

	"haproxy-template-ic/tests/testutil"
)

// DebugClient provides access to the controller's debug HTTP server via Kubernetes API proxy.
// This approach uses the API server's built-in service proxy, which routes requests through
// the API server to the service. This is more reliable than port-forwarding (SPDY) and
// doesn't require NodePort exposure through DinD extraPortMappings.
type DebugClient struct {
	clientset   kubernetes.Interface
	namespace   string
	serviceName string
	port        string
}

// NewDebugClient creates a new debug client for accessing the controller via API proxy.
// The clientset is used to make proxied HTTP requests through the Kubernetes API server.
func NewDebugClient(clientset kubernetes.Interface, namespace, serviceName string, port int32) *DebugClient {
	return &DebugClient{
		clientset:   clientset,
		namespace:   namespace,
		serviceName: serviceName,
		port:        strconv.Itoa(int(port)),
	}
}

// proxyGet makes an HTTP GET request through the Kubernetes API server proxy.
// Includes retry logic with exponential backoff for resilience during parallel test execution.
//
// The retry budget is intentionally kept small (3 retries, 2s max backoff) to ensure
// that higher-level Wait functions get enough retry opportunities. In CI with 17
// parallel tests, the API server can be temporarily overloaded. With a smaller
// retry budget (~4s worst case), the Wait function can make more attempts within
// its timeout budget.
func (dc *DebugClient) proxyGet(ctx context.Context, path string) ([]byte, error) {
	const (
		maxRetries     = 3                       // Reduced from 5 to allow more Wait-level retries
		initialBackoff = 100 * time.Millisecond
		maxBackoff     = 2 * time.Second         // Reduced from 5s for tighter retry budget
		minTimeForRetries = 3 * time.Second      // Minimum time needed for meaningful retries
	)

	// Check if we have enough time remaining for retries.
	// If deadline is tight, try once and fail fast to let the Wait function retry.
	if deadline, ok := ctx.Deadline(); ok {
		remaining := time.Until(deadline)
		if remaining < minTimeForRetries {
			// Not enough time for meaningful retries - try once and return
			return dc.clientset.CoreV1().Services(dc.namespace).ProxyGet(
				"http",
				dc.serviceName,
				dc.port,
				path,
				nil,
			).DoRaw(ctx)
		}
	}

	var lastErr error
	backoff := initialBackoff

	for attempt := 0; attempt < maxRetries; attempt++ {
		if attempt > 0 {
			// Wait before retry with exponential backoff
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(backoff):
				backoff *= 2
				if backoff > maxBackoff {
					backoff = maxBackoff
				}
			}
		}

		body, err := dc.clientset.CoreV1().Services(dc.namespace).ProxyGet(
			"http",
			dc.serviceName,
			dc.port,
			path,
			nil,
		).DoRaw(ctx)

		if err == nil {
			return body, nil
		}

		lastErr = err

		// Check if the parent context is cancelled - don't retry in that case
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}

		// Check if error is retryable (server overloaded, temporary unavailability)
		errStr := err.Error()
		if strings.Contains(errStr, "unable to handle the request") ||
			strings.Contains(errStr, "connection refused") ||
			strings.Contains(errStr, "no endpoints available") {
			// Retryable error, continue to next attempt
			continue
		}

		// Non-retryable error, return immediately
		return nil, err
	}

	return nil, fmt.Errorf("proxyGet failed after %d retries: %w", maxRetries, lastErr)
}

// GetConfig retrieves the current controller configuration from the debug server.
func (dc *DebugClient) GetConfig(ctx context.Context) (map[string]interface{}, error) {
	return dc.getJSON(ctx, "/debug/vars/config")
}

// GetRenderedConfig retrieves the rendered HAProxy configuration.
func (dc *DebugClient) GetRenderedConfig(ctx context.Context) (string, error) {
	data, err := dc.getJSON(ctx, "/debug/vars/rendered")
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
// This is useful when the controller may be temporarily unavailable during startup.
func (dc *DebugClient) GetRenderedConfigWithRetry(ctx context.Context, timeout time.Duration) (string, error) {
	var result string

	cfg := testutil.DefaultWaitConfig()
	cfg.Timeout = timeout

	err := testutil.WaitForConditionWithDescription(ctx, cfg, "rendered config",
		func(ctx context.Context) (bool, error) {
			config, err := dc.GetRenderedConfig(ctx)
			if err != nil {
				return false, err
			}
			result = config
			return true, nil
		})

	if err != nil {
		return "", err
	}
	return result, nil
}

// GetEvents retrieves recent events from the debug server.
func (dc *DebugClient) GetEvents(ctx context.Context) ([]map[string]interface{}, error) {
	data, err := dc.getJSON(ctx, "/debug/vars/events")
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
func (dc *DebugClient) WaitForConfig(ctx context.Context, timeout time.Duration) (map[string]interface{}, error) {
	var result map[string]interface{}

	cfg := testutil.DefaultWaitConfig()
	cfg.Timeout = timeout

	err := testutil.WaitForConditionWithDescription(ctx, cfg, "config available",
		func(ctx context.Context) (bool, error) {
			config, err := dc.GetConfig(ctx)
			if err != nil {
				return false, err
			}
			result = config
			return true, nil
		})

	if err != nil {
		return nil, err
	}
	return result, nil
}

// WaitForConfigVersion waits for the controller to load a specific config version.
func (dc *DebugClient) WaitForConfigVersion(ctx context.Context, expectedVersion string, timeout time.Duration) error {
	cfg := testutil.DefaultWaitConfig()
	cfg.Timeout = timeout

	return testutil.WaitForConditionWithDescription(ctx, cfg, fmt.Sprintf("config version %q", expectedVersion),
		func(ctx context.Context) (bool, error) {
			config, err := dc.GetConfig(ctx)
			if err != nil {
				return false, err
			}

			if version, ok := config["version"].(string); ok && version == expectedVersion {
				return true, nil
			}
			return false, nil
		})
}

// WaitForRenderedConfigContains waits for the rendered config to contain a specific string.
func (dc *DebugClient) WaitForRenderedConfigContains(ctx context.Context, expectedSubstring string, timeout time.Duration) error {
	cfg := testutil.DefaultWaitConfig()
	cfg.Timeout = timeout

	return testutil.WaitForConditionWithDescription(ctx, cfg, fmt.Sprintf("rendered config contains %q", expectedSubstring),
		func(ctx context.Context) (bool, error) {
			rendered, err := dc.GetRenderedConfig(ctx)
			if err != nil {
				return false, err
			}

			return strings.Contains(rendered, expectedSubstring), nil
		})
}

// WaitForRenderedConfigContainsAny waits for the rendered config to contain any of the expected strings.
// On timeout, returns an error with the last seen config for debugging.
func (dc *DebugClient) WaitForRenderedConfigContainsAny(ctx context.Context, expectedSubstrings []string, timeout time.Duration) (string, error) {
	var matched string
	var lastConfig string

	cfg := testutil.DefaultWaitConfig()
	cfg.Timeout = timeout

	err := testutil.WaitForCondition(ctx, cfg,
		func(ctx context.Context) (bool, error) {
			rendered, err := dc.GetRenderedConfig(ctx)
			if err != nil {
				return false, err
			}

			lastConfig = rendered
			for _, expected := range expectedSubstrings {
				if strings.Contains(rendered, expected) {
					matched = expected
					return true, nil
				}
			}
			return false, nil
		})

	if err != nil {
		errMsg := fmt.Sprintf("timeout waiting for rendered config to contain any of %v", expectedSubstrings)
		if lastConfig != "" {
			errMsg += fmt.Sprintf("\nlast seen config:\n%s", lastConfig)
		}
		return "", fmt.Errorf("%s: %w", errMsg, err)
	}
	return matched, nil
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
	data, err := dc.getJSON(ctx, "/debug/vars/pipeline")
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
// This is useful when the controller may be temporarily unavailable during startup.
func (dc *DebugClient) GetPipelineStatusWithRetry(ctx context.Context, timeout time.Duration) (*PipelineStatus, error) {
	var result *PipelineStatus

	cfg := testutil.DefaultWaitConfig()
	cfg.Timeout = timeout

	err := testutil.WaitForConditionWithDescription(ctx, cfg, "pipeline status",
		func(ctx context.Context) (bool, error) {
			status, err := dc.GetPipelineStatus(ctx)
			if err != nil {
				return false, err
			}
			result = status
			return true, nil
		})

	if err != nil {
		return nil, err
	}
	return result, nil
}

// GetValidatedConfig retrieves the last successfully validated HAProxy configuration.
func (dc *DebugClient) GetValidatedConfig(ctx context.Context) (*ValidatedConfigInfo, error) {
	data, err := dc.getJSON(ctx, "/debug/vars/validated")
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
	data, err := dc.getJSON(ctx, "/debug/vars/errors")
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
func (dc *DebugClient) WaitForValidationStatus(ctx context.Context, expected string, timeout time.Duration) error {
	cfg := testutil.DefaultWaitConfig()
	cfg.Timeout = timeout

	return testutil.WaitForConditionWithDescription(ctx, cfg, fmt.Sprintf("validation status %q", expected),
		func(ctx context.Context) (bool, error) {
			status, err := dc.GetPipelineStatus(ctx)
			if err != nil {
				return false, err
			}

			if status.Validation != nil && status.Validation.Status == expected {
				return true, nil
			}
			return false, nil
		})
}

// getJSON fetches JSON from the debug server via API proxy.
func (dc *DebugClient) getJSON(ctx context.Context, path string) (map[string]interface{}, error) {
	body, err := dc.proxyGet(ctx, path)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch from debug server: %w", err)
	}

	var data map[string]interface{}
	if err := json.Unmarshal(body, &data); err != nil {
		return nil, fmt.Errorf("failed to decode JSON response: %w", err)
	}

	return data, nil
}

// GetAuxiliaryFiles retrieves the auxiliary files from the debug endpoint.
func (dc *DebugClient) GetAuxiliaryFiles(ctx context.Context) (map[string]interface{}, error) {
	return dc.getJSON(ctx, "/debug/vars/auxfiles")
}

// GetGeneralFileContent retrieves the content of a specific general file from auxiliary files.
// Returns the content string and any error encountered.
func (dc *DebugClient) GetGeneralFileContent(ctx context.Context, fileName string) (string, error) {
	auxFiles, err := dc.GetAuxiliaryFiles(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to get auxiliary files: %w", err)
	}

	files, ok := auxFiles["files"].(map[string]interface{})
	if !ok {
		return "", fmt.Errorf("files field not found or wrong type")
	}

	// The struct field is GeneralFiles (not general_files) - Go JSON serialization uses struct field names
	generalFiles, ok := files["GeneralFiles"].([]interface{})
	if !ok {
		return "", fmt.Errorf("GeneralFiles field not found or wrong type")
	}

	for _, file := range generalFiles {
		fileMap, ok := file.(map[string]interface{})
		if !ok {
			continue
		}
		// The struct field is Filename (not Name)
		name, ok := fileMap["Filename"].(string)
		if !ok {
			continue
		}
		if name == fileName {
			content, ok := fileMap["Content"].(string)
			if !ok {
				return "", fmt.Errorf("content field not found or wrong type for file %s", fileName)
			}
			return content, nil
		}
	}

	return "", fmt.Errorf("file %s not found in auxiliary files", fileName)
}

// WaitForAuxFileContains waits until a specific auxiliary file contains the expected content.
func (dc *DebugClient) WaitForAuxFileContains(ctx context.Context, fileName, expectedContent string, timeout time.Duration) error {
	var lastContent string

	cfg := testutil.DefaultWaitConfig()
	cfg.Timeout = timeout

	err := testutil.WaitForCondition(ctx, cfg,
		func(ctx context.Context) (bool, error) {
			content, err := dc.GetGeneralFileContent(ctx, fileName)
			if err != nil {
				return false, err
			}
			lastContent = content

			if strings.Contains(content, expectedContent) {
				return true, nil
			}
			return false, nil
		})

	if err != nil {
		return fmt.Errorf("waiting for file %s to contain %q (last content: %q): %w", fileName, expectedContent, lastContent, err)
	}
	return nil
}

// WaitForAuxFileNotContains waits until a specific auxiliary file does NOT contain the specified content.
// This is useful for verifying that invalid content was rejected.
func (dc *DebugClient) WaitForAuxFileNotContains(ctx context.Context, fileName, unexpectedContent string, timeout time.Duration) error {
	var lastContent string

	cfg := testutil.DefaultWaitConfig()
	cfg.Timeout = timeout

	err := testutil.WaitForCondition(ctx, cfg,
		func(ctx context.Context) (bool, error) {
			content, err := dc.GetGeneralFileContent(ctx, fileName)
			if err != nil {
				return false, err
			}
			lastContent = content

			if !strings.Contains(content, unexpectedContent) {
				return true, nil
			}
			return false, nil
		})

	if err != nil {
		return fmt.Errorf("file %s still contains %q (last content: %q): %w", fileName, unexpectedContent, lastContent, err)
	}
	return nil
}

// WaitForAuxFileContentStable waits and verifies that the file content stays unchanged for the duration.
// This is useful for verifying that invalid updates are rejected and old content is preserved.
//
// Note: This function uses a fixed 500ms polling interval to ensure frequent stability checks.
// Exponential backoff would be counter-productive here as we need to verify content hasn't changed.
func (dc *DebugClient) WaitForAuxFileContentStable(ctx context.Context, fileName string, expectedContent string, stableDuration time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, stableDuration+5*time.Second)
	defer cancel()

	// Use a fast fixed interval for stability checking - we need frequent checks
	// to verify content hasn't changed, not exponential backoff
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	stableStart := time.Now()

	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("timeout while verifying content stability for file %s", fileName)

		case <-ticker.C:
			content, err := dc.GetGeneralFileContent(ctx, fileName)
			if err != nil {
				return fmt.Errorf("error getting file content: %w", err)
			}

			if !strings.Contains(content, expectedContent) {
				return fmt.Errorf("file content changed unexpectedly, expected to contain %q but got %q", expectedContent, content)
			}

			if time.Since(stableStart) >= stableDuration {
				// Content has been stable for the required duration
				return nil
			}
		}
	}
}
