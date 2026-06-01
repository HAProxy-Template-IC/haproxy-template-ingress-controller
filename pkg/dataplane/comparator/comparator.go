package comparator

import (
	"errors"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/comparator/sections"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/parser"
)

const (
	parentTypeFrontend = "frontend"
	parentTypeBackend  = "backend"
	parentTypeDefaults = "defaults"
)

// Comparator performs fine-grained comparison between HAProxy configurations.
//
// It generates the minimal set of operations needed to transform a current
// configuration into a desired configuration, using attribute-level granularity
// to minimize API calls and avoid unnecessary HAProxy reloads.
type Comparator struct{}

// New creates a new Comparator instance.
func New() *Comparator {
	return &Comparator{}
}

// appendOperationsIfNotEmpty is a helper method that appends operations and marks as modified if operations exist.
// This reduces cyclomatic complexity by extracting the common pattern used throughout comparison functions.
func appendOperationsIfNotEmpty(dst *[]Operation, src []Operation, modified *bool) {
	if len(src) > 0 {
		*dst = append(*dst, src...)
		*modified = true
	}
}

// updateSummaryFromOperations updates the summary counts based on the operations.
// This is extracted to reduce statement count in the Compare function.
func updateSummaryFromOperations(summary *DiffSummary, operations []Operation) {
	for _, op := range operations {
		switch op.Type() {
		case sections.OperationCreate:
			summary.TotalCreates++
		case sections.OperationUpdate:
			summary.TotalUpdates++
		case sections.OperationDelete:
			summary.TotalDeletes++
		}
	}
}

// compareNamedSections is a generic helper for comparing named configuration sections.
// It handles the common pattern of:
//   - Converting slices to maps by Name
//   - Finding added, deleted, and modified items
//   - Generating appropriate operations
//
// Type Parameters:
//   - T: The section type (must have Name field and Equal method)
//
// Parameters:
//   - currentSlice: Current configuration sections
//   - desiredSlice: Desired configuration sections
//   - getName: Function to get the name from a section
//   - equal: Function to compare two sections for equality
//   - createOp: Factory function for create operations
//   - deleteOp: Factory function for delete operations
//   - updateOp: Factory function for update operations
func compareNamedSections[T any](
	currentSlice, desiredSlice []*T,
	getName func(*T) string,
	equal func(*T, *T) bool,
	createOp func(*T) Operation,
	deleteOp func(*T) Operation,
	updateOp func(*T) Operation,
) []Operation {
	currentMap := make(map[string]*T)
	for _, item := range currentSlice {
		if name := getName(item); name != "" {
			currentMap[name] = item
		}
	}
	desiredMap := make(map[string]*T)
	for _, item := range desiredSlice {
		if name := getName(item); name != "" {
			desiredMap[name] = item
		}
	}
	return compareNamedMaps(currentMap, desiredMap, equal, createOp, deleteOp, updateOp)
}

// compareNamedMaps compares two maps keyed by name, emitting create/delete/update
// operations via caller-supplied factories. Nil maps are treated as empty.
//
// Type parameter:
//   - V: The map's value type (typically a pointer to a section model).
func compareNamedMaps[V any](
	current, desired map[string]V,
	equal func(V, V) bool,
	create func(V) Operation,
	remove func(V) Operation,
	update func(V) Operation,
) []Operation {
	var operations []Operation
	for name, item := range desired {
		if _, exists := current[name]; !exists {
			operations = append(operations, create(item))
		}
	}
	for name, item := range current {
		if _, exists := desired[name]; !exists {
			operations = append(operations, remove(item))
		}
	}
	for name, desiredItem := range desired {
		if currentItem, exists := current[name]; exists && !equal(currentItem, desiredItem) {
			operations = append(operations, update(desiredItem))
		}
	}
	return operations
}

// Compare performs a deep comparison between current and desired configurations.
//
// It returns a ConfigDiff containing all operations needed to transform
// current into desired, along with a summary of changes.
//
// The comparison is performed at attribute-level granularity - if only a
// single attribute changes (e.g., server weight), only that attribute is
// updated rather than replacing the entire resource.
//
// Example:
//
//	// cmp is a *Comparator; the variable name avoids shadowing the
//	// imported `comparator` package so subsequent comparator.X type
//	// references in the same scope keep working.
//	cmp := comparator.New()
//	diff, err := cmp.Compare(currentConfig, desiredConfig)
//	if err != nil {
//	    slog.Error("comparison failed", "error", err)
//	    os.Exit(1)
//	}
//
//	fmt.Printf("Changes: %s\n", diff.Summary.String())
//	for _, op := range diff.Operations {
//	    fmt.Printf("- %s\n", op.Describe())
//	}
func (c *Comparator) Compare(current, desired *parser.StructuredConfig) (*ConfigDiff, error) {
	if current == nil {
		return nil, errors.New("current configuration is nil")
	}
	if desired == nil {
		return nil, errors.New("desired configuration is nil")
	}

	summary := NewDiffSummary()

	// Compute all section operations once so we can preallocate the merged slice
	// to the exact capacity instead of growing it incrementally.
	sectionOps := [][]Operation{
		c.compareGlobal(current, desired, &summary),
		c.compareDefaults(current, desired, &summary),
		c.compareHTTPErrors(current, desired),
		c.compareResolvers(current, desired),
		c.compareMailers(current, desired),
		c.comparePeers(current, desired),
		c.compareCaches(current, desired),
		c.compareRings(current, desired),
		c.compareUserlists(current, desired),
		c.comparePrograms(current, desired),
		c.compareLogForwards(current, desired),
		c.compareLogProfiles(current, desired),        // v3.1+ only
		c.compareTraces(current, desired),             // v3.1+ only, singleton
		c.compareAcmeProviders(current, desired),      // v3.2+ only
		c.compareEnterpriseSections(current, desired), // EE only
		c.compareFCGIApps(current, desired),
		c.compareCrtStores(current, desired),
		c.compareFrontends(current, desired, &summary),
		c.compareBackends(current, desired, &summary),
	}

	capacity := 0
	for _, ops := range sectionOps {
		capacity += len(ops)
	}
	operations := make([]Operation, 0, capacity)
	for _, ops := range sectionOps {
		operations = append(operations, ops...)
	}

	// Update summary counts from operations
	updateSummaryFromOperations(&summary, operations)

	return &ConfigDiff{
		Operations: operations,
		Summary:    summary,
	}, nil
}
