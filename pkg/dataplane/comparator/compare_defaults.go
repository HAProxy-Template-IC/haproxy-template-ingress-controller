package comparator

import (
	"github.com/haproxytech/client-native/v6/models"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/comparator/sections"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/parser"
)

// compareGlobal compares global section configurations between current and desired.
// The global section is a singleton - it always exists and can only be updated, not created or deleted.
func (c *Comparator) compareGlobal(current, desired *parser.StructuredConfig, summary *DiffSummary) []Operation {
	var operations []Operation

	// Both configs should have a global section (even if empty)
	// If either is nil, we skip comparison
	if current.Global == nil || desired.Global == nil {
		return operations
	}

	// Compare using built-in Equal() method
	// This automatically compares all global attributes (log settings, stats socket, maxconn, etc.)
	if !current.Global.Equal(*desired.Global) {
		operations = append(operations, sections.NewGlobalUpdate(desired.Global))
		summary.GlobalChanged = true
	}

	return operations
}

// compareDefaults compares defaults section configurations between current and desired.
// HAProxy can have multiple defaults sections (identified by name).
func (c *Comparator) compareDefaults(current, desired *parser.StructuredConfig, summary *DiffSummary) []Operation {
	var operations []Operation

	// Build maps for easier comparison
	currentDefaults := make(map[string]*models.Defaults)
	for _, defaults := range current.Defaults {
		if defaults.Name != "" {
			currentDefaults[defaults.Name] = defaults
		}
	}

	desiredDefaults := make(map[string]*models.Defaults)
	for _, defaults := range desired.Defaults {
		if defaults.Name != "" {
			desiredDefaults[defaults.Name] = defaults
		}
	}

	// Find added defaults sections
	for name, defaults := range desiredDefaults {
		if _, exists := currentDefaults[name]; !exists {
			operations = append(operations, sections.NewDefaultsCreate(defaults))
			summary.DefaultsChanged = true
		}
	}

	// Find deleted defaults sections
	for name, defaults := range currentDefaults {
		if _, exists := desiredDefaults[name]; !exists {
			operations = append(operations, sections.NewDefaultsDelete(defaults))
			summary.DefaultsChanged = true
		}
	}

	// Find modified defaults sections
	for name, desiredDefaults := range desiredDefaults {
		if currentDefaults, exists := currentDefaults[name]; exists {
			// Compare using built-in Equal() method
			// This automatically compares all defaults attributes (mode, timeouts, options, etc.)
			if !currentDefaults.Equal(*desiredDefaults) {
				operations = append(operations, sections.NewDefaultsUpdate(desiredDefaults))
				summary.DefaultsChanged = true
			}
		}
	}

	return operations
}
