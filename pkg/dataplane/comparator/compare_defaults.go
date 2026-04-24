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
	ops := compareNamedSections(
		current.Defaults,
		desired.Defaults,
		func(d *models.Defaults) string { return d.Name },
		func(a, b *models.Defaults) bool { return a.Equal(*b) },
		sections.NewDefaultsCreate,
		sections.NewDefaultsDelete,
		sections.NewDefaultsUpdate,
	)
	if len(ops) > 0 {
		summary.DefaultsChanged = true
	}
	return ops
}
