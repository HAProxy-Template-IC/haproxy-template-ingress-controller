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
type Comparator struct {
	// Future: Add section-specific comparators here
	// backendComparator *sections.BackendComparator
	// serverComparator  *sections.ServerComparator
}

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

// compareMapEntries is a generic helper for comparing map-based child entries (nameservers, mailer entries, peer entries).
// This reduces code duplication for the common pattern of comparing map[string]T entries.
func compareMapEntries[T any](
	currentMap, desiredMap map[string]T,
	createOp func(*T) Operation,
	deleteOp func(*T) Operation,
	updateOp func(*T) Operation,
	equalFunc func(*T, *T) bool,
) []Operation {
	var operations []Operation

	// Handle nil maps
	if currentMap == nil {
		currentMap = make(map[string]T)
	}
	if desiredMap == nil {
		desiredMap = make(map[string]T)
	}

	// Find added entries
	for name := range desiredMap {
		if _, exists := currentMap[name]; !exists {
			entry := desiredMap[name]
			operations = append(operations, createOp(&entry))
		}
	}

	// Find deleted entries
	for name := range currentMap {
		if _, exists := desiredMap[name]; !exists {
			entry := currentMap[name]
			operations = append(operations, deleteOp(&entry))
		}
	}

	// Find modified entries
	for name := range desiredMap {
		currentEntry, exists := currentMap[name]
		if !exists {
			continue
		}
		desiredEntry := desiredMap[name]

		// Compare entry attributes
		if !equalFunc(&currentEntry, &desiredEntry) {
			operations = append(operations, updateOp(&desiredEntry))
		}
	}

	return operations
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
//	comparator := comparator.New()
//	diff, err := comparator.Compare(currentConfig, desiredConfig)
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

	// Compute all section operations before allocating to allow exact preallocation.
	globalOps := c.compareGlobal(current, desired, &summary)
	defaultsOps := c.compareDefaults(current, desired, &summary)
	httpErrorsOps := c.compareHTTPErrors(current, desired)
	resolversOps := c.compareResolvers(current, desired)
	mailersOps := c.compareMailers(current, desired)
	peersOps := c.comparePeers(current, desired)
	cachesOps := c.compareCaches(current, desired)
	ringsOps := c.compareRings(current, desired)
	userlistsOps := c.compareUserlists(current, desired)
	programsOps := c.comparePrograms(current, desired)
	logForwardsOps := c.compareLogForwards(current, desired)
	logProfilesOps := c.compareLogProfiles(current, desired)     // v3.1+ only
	tracesOps := c.compareTraces(current, desired)               // v3.1+ only, singleton
	acmeProvidersOps := c.compareAcmeProviders(current, desired) // v3.2+ only
	eeOps := c.compareEnterpriseSections(current, desired)       // EE only
	fcgiAppsOps := c.compareFCGIApps(current, desired)
	crtStoresOps := c.compareCrtStores(current, desired)
	frontendOps := c.compareFrontends(current, desired, &summary)
	backendOps := c.compareBackends(current, desired, &summary)

	capacity := len(globalOps) + len(defaultsOps) + len(httpErrorsOps) + len(resolversOps) +
		len(mailersOps) + len(peersOps) + len(cachesOps) + len(ringsOps) + len(userlistsOps) +
		len(programsOps) + len(logForwardsOps) + len(logProfilesOps) + len(tracesOps) +
		len(acmeProvidersOps) + len(eeOps) + len(fcgiAppsOps) + len(crtStoresOps) +
		len(frontendOps) + len(backendOps)
	operations := make([]Operation, 0, capacity)
	operations = append(operations, globalOps...)
	operations = append(operations, defaultsOps...)
	operations = append(operations, httpErrorsOps...)
	operations = append(operations, resolversOps...)
	operations = append(operations, mailersOps...)
	operations = append(operations, peersOps...)
	operations = append(operations, cachesOps...)
	operations = append(operations, ringsOps...)
	operations = append(operations, userlistsOps...)
	operations = append(operations, programsOps...)
	operations = append(operations, logForwardsOps...)
	operations = append(operations, logProfilesOps...)
	operations = append(operations, tracesOps...)
	operations = append(operations, acmeProvidersOps...)
	operations = append(operations, eeOps...)
	operations = append(operations, fcgiAppsOps...)
	operations = append(operations, crtStoresOps...)
	operations = append(operations, frontendOps...)
	operations = append(operations, backendOps...)

	// Update summary counts from operations
	updateSummaryFromOperations(&summary, operations)

	// Order operations by dependencies
	orderedOps := OrderOperations(operations)

	return &ConfigDiff{
		Operations: orderedOps,
		Summary:    summary,
	}, nil
}
