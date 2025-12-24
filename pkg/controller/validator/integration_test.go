package validator

import (
	"context"
	"log/slog"
	"os"
	"testing"
	"time"

	"gitlab.com/haproxy-template-ic/haproxy-template-ingress-controller/pkg/controller/events"
	coreconfig "gitlab.com/haproxy-template-ic/haproxy-template-ingress-controller/pkg/core/config"
	busevents "gitlab.com/haproxy-template-ic/haproxy-template-ingress-controller/pkg/events"
)

// TestValidationScatterGather tests the full scatter-gather validation flow
// with all three validators (basic, template, jsonpath) running concurrently.
func TestValidationScatterGather(t *testing.T) {
	ctx, bus := setupValidationTest(t)
	cfg := createInvalidTestConfig()

	// Send validation request using scatter-gather
	req := events.NewConfigValidationRequest(cfg, "test-version")
	result, err := bus.Request(ctx, req, busevents.RequestOptions{
		Timeout:            2 * time.Second,
		ExpectedResponders: AllValidatorNames(),
	})

	if err != nil {
		t.Fatalf("Request() returned error: %v", err)
	}

	// Verify we got responses from all three validators
	if len(result.Responses) != 3 {
		t.Errorf("Expected 3 responses, got %d", len(result.Responses))
	}

	// Collect and verify responses
	responses := collectValidationResponses(t, result.Responses)
	verifyInvalidResponse(t, responses, ValidatorNameBasic, "empty match_labels")
	verifyInvalidResponse(t, responses, ValidatorNameTemplate, "unclosed tag")
	verifyInvalidResponse(t, responses, ValidatorNameJSONPath, "invalid[[JSONPath")

	// Verify no missing responders
	if len(result.Errors) > 0 {
		t.Errorf("Expected no missing responders, got errors: %v", result.Errors)
	}
}

// TestValidationScatterGather_ValidConfig tests scatter-gather with a valid config.
func TestValidationScatterGather_ValidConfig(t *testing.T) {
	ctx, bus := setupValidationTest(t)
	cfg := createValidTestConfig()

	// Send validation request
	req := events.NewConfigValidationRequest(cfg, "test-version")
	result, err := bus.Request(ctx, req, busevents.RequestOptions{
		Timeout:            2 * time.Second,
		ExpectedResponders: AllValidatorNames(),
	})

	if err != nil {
		t.Fatalf("Request() returned error: %v", err)
	}

	// Verify we got responses from all three validators
	if len(result.Responses) != 3 {
		t.Errorf("Expected 3 responses, got %d", len(result.Responses))
	}

	// Verify all validators returned valid=true
	for _, resp := range result.Responses {
		validationResp, ok := resp.(*events.ConfigValidationResponse)
		if !ok {
			t.Errorf("Response is not ConfigValidationResponse: %T", resp)
			continue
		}

		if !validationResp.Valid {
			t.Errorf("Validator %q reported invalid config, errors: %v",
				validationResp.ValidatorName, validationResp.Errors)
		}

		if len(validationResp.Errors) > 0 {
			t.Errorf("Validator %q returned errors for valid config: %v",
				validationResp.ValidatorName, validationResp.Errors)
		}
	}
}

// setupValidationTest creates and starts the test environment with all validators.
func setupValidationTest(t *testing.T) (context.Context, *busevents.EventBus) {
	t.Helper()

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: slog.LevelError,
	}))

	bus := busevents.NewEventBus(100)
	bus.Start()

	basicValidator := NewBasicValidator(bus, logger)
	templateValidator := NewTemplateValidator(bus, logger)
	jsonpathValidator := NewJSONPathValidator(bus, logger)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)

	go basicValidator.Start(ctx)
	go templateValidator.Start(ctx)
	go jsonpathValidator.Start(ctx)

	time.Sleep(100 * time.Millisecond) // Give validators time to subscribe

	return ctx, bus
}

// createInvalidTestConfig creates a config with errors for all validators.
func createInvalidTestConfig() *coreconfig.Config {
	return &coreconfig.Config{
		PodSelector: coreconfig.PodSelector{
			MatchLabels: map[string]string{}, // Invalid: empty (basic validator should catch)
		},
		Controller: coreconfig.ControllerConfig{
			HealthzPort: 8080,
			MetricsPort: 9090,
		},
		Logging: coreconfig.LoggingConfig{
			Level: "INFO",
		},
		WatchedResourcesIgnoreFields: []string{
			"invalid[[JSONPath", // Invalid JSONPath (jsonpath validator should catch)
		},
		WatchedResources: map[string]coreconfig.WatchedResource{
			"ingresses": {
				APIVersion: "networking.k8s.io/v1",
				Resources:  "ingresses",
				IndexBy: []string{
					"metadata.namespace",
				},
			},
		},
		HAProxyConfig: coreconfig.HAProxyConfig{
			Template: "{{ unclosed tag", // Invalid template (template validator should catch)
		},
	}
}

// createValidTestConfig creates a valid config.
func createValidTestConfig() *coreconfig.Config {
	cfg := &coreconfig.Config{
		PodSelector: coreconfig.PodSelector{
			MatchLabels: map[string]string{
				"app": "haproxy",
			},
		},
		Controller: coreconfig.ControllerConfig{
			HealthzPort: 8080,
			MetricsPort: 9090,
		},
		Logging: coreconfig.LoggingConfig{
			Level: "INFO",
		},
		WatchedResourcesIgnoreFields: []string{
			"metadata.managedFields",
		},
		WatchedResources: map[string]coreconfig.WatchedResource{
			"ingresses": {
				APIVersion: "networking.k8s.io/v1",
				Resources:  "ingresses",
				IndexBy: []string{
					"metadata.namespace",
				},
			},
		},
		HAProxyConfig: coreconfig.HAProxyConfig{
			Template: "frontend http\n  bind *:80\n",
		},
	}

	// Apply defaults before validation
	coreconfig.SetDefaults(cfg)

	return cfg
}

// collectValidationResponses collects and maps responses by validator name.
func collectValidationResponses(t *testing.T, responses []busevents.Response) map[string]*events.ConfigValidationResponse {
	t.Helper()

	result := make(map[string]*events.ConfigValidationResponse)
	for _, resp := range responses {
		validationResp, ok := resp.(*events.ConfigValidationResponse)
		if !ok {
			t.Errorf("Response is not ConfigValidationResponse: %T", resp)
			continue
		}
		result[validationResp.ValidatorName] = validationResp
	}
	return result
}

// verifyInvalidResponse verifies that a validator found errors.
func verifyInvalidResponse(t *testing.T, responses map[string]*events.ConfigValidationResponse, validatorName, errorContext string) {
	t.Helper()

	resp, ok := responses[validatorName]
	if !ok {
		t.Errorf("Missing response from %s validator", validatorName)
		return
	}

	if resp.Valid {
		t.Errorf("%s validator should have found errors (%s)", validatorName, errorContext)
	}
	if len(resp.Errors) == 0 {
		t.Errorf("%s validator should have returned errors", validatorName)
	}
	t.Logf("%s validator errors: %v", validatorName, resp.Errors)
}
