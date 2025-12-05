package tests

import (
	"testing"

	"github.com/arch-go/arch-go/v2/api"
	"github.com/arch-go/arch-go/v2/api/configuration"
)

// TestArchitecture validates that the codebase follows the defined architectural constraints.
//
// This test enforces that:
//   - Only pkg/controller packages can depend on other top-level packages
//   - Top-level packages (core, dataplane, events, k8s, templating) do not depend on each other
//
// The architectural rules are defined in arch-go.yml in the project root.
//
// This test runs as part of the normal test suite and will fail CI if architecture
// constraints are violated.
//
//nolint:revive // cognitive-complexity: Architecture validation with detailed error reporting
func TestArchitecture(t *testing.T) {
	// Load module information
	moduleInfo := configuration.Load("haproxy-template-ic")

	// Load configuration from arch-go.yml
	config, err := configuration.LoadConfig("../arch-go.yml")
	if err != nil {
		t.Fatalf("Failed to load arch-go.yml configuration: %v", err)
	}

	// Run architecture validation
	result := api.CheckArchitecture(moduleInfo, *config)

	// Check if validation passed
	if !result.Pass {
		t.Errorf("Architecture validation failed!\n")

		// Print detailed violation information for dependencies rules
		if result.DependenciesRuleResult != nil && !result.DependenciesRuleResult.Passes {
			t.Errorf("Dependencies rule violations:")
			for _, ruleResult := range result.DependenciesRuleResult.Results {
				if !ruleResult.Passes {
					t.Errorf("\n  Rule: %s", ruleResult.Description)
					for _, verification := range ruleResult.Verifications {
						if !verification.Passes {
							t.Errorf("    Package: %s", verification.Package)
							for _, detail := range verification.Details {
								t.Errorf("      - %s", detail)
							}
						}
					}
				}
			}
		}

		t.Fatal("Architecture validation failed. See violations above.")
	}

	// Log successful validation
	t.Logf("Architecture validation passed!")
	t.Logf("Duration: %v", result.Duration)
}
