package comparator

import (
	"fmt"

	"haproxy-template-ic/pkg/dataplane/comparator/sections"
	"haproxy-template-ic/pkg/dataplane/parser"
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
	var operations []Operation

	// Convert slices to maps for easier comparison by Name
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

	// Find added sections (in desired but not in current)
	for name, item := range desiredMap {
		if _, exists := currentMap[name]; !exists {
			operations = append(operations, createOp(item))
		}
	}

	// Find deleted sections (in current but not in desired)
	for name, item := range currentMap {
		if _, exists := desiredMap[name]; !exists {
			operations = append(operations, deleteOp(item))
		}
	}

	// Find modified sections (in both, but different)
	for name, desiredItem := range desiredMap {
		if currentItem, exists := currentMap[name]; exists {
			if !equal(currentItem, desiredItem) {
				operations = append(operations, updateOp(desiredItem))
			}
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
		return nil, fmt.Errorf("current configuration is nil")
	}
	if desired == nil {
		return nil, fmt.Errorf("desired configuration is nil")
	}

	summary := NewDiffSummary()
	var operations []Operation

	// Compare global section
	globalOps := c.compareGlobal(current, desired, &summary)
	operations = append(operations, globalOps...)

	// Compare defaults sections
	defaultsOps := c.compareDefaults(current, desired, &summary)
	operations = append(operations, defaultsOps...)

	// Compare http-errors sections
	httpErrorsOps := c.compareHTTPErrors(current, desired)
	operations = append(operations, httpErrorsOps...)

	// Compare resolvers
	resolversOps := c.compareResolvers(current, desired)
	operations = append(operations, resolversOps...)

	// Compare mailers
	mailersOps := c.compareMailers(current, desired)
	operations = append(operations, mailersOps...)

	// Compare peers
	peersOps := c.comparePeers(current, desired)
	operations = append(operations, peersOps...)

	// Compare caches
	cachesOps := c.compareCaches(current, desired)
	operations = append(operations, cachesOps...)

	// Compare rings
	ringsOps := c.compareRings(current, desired)
	operations = append(operations, ringsOps...)

	// Compare userlists
	userlistsOps := c.compareUserlists(current, desired)
	operations = append(operations, userlistsOps...)

	// Compare programs
	programsOps := c.comparePrograms(current, desired)
	operations = append(operations, programsOps...)

	// Compare log-forwards
	logForwardsOps := c.compareLogForwards(current, desired)
	operations = append(operations, logForwardsOps...)

	// Compare log-profiles (v3.1+ only)
	logProfilesOps := c.compareLogProfiles(current, desired)
	operations = append(operations, logProfilesOps...)

	// Compare traces (v3.1+ only, singleton)
	tracesOps := c.compareTraces(current, desired)
	operations = append(operations, tracesOps...)

	// Compare acme-providers (v3.2+ only)
	acmeProvidersOps := c.compareAcmeProviders(current, desired)
	operations = append(operations, acmeProvidersOps...)

	// Compare Enterprise Edition sections (EE only)
	eeOps := c.compareEnterpriseSections(current, desired)
	operations = append(operations, eeOps...)

	// Compare fcgi-apps
	fcgiAppsOps := c.compareFCGIApps(current, desired)
	operations = append(operations, fcgiAppsOps...)

	// Compare crt-stores
	crtStoresOps := c.compareCrtStores(current, desired)
	operations = append(operations, crtStoresOps...)

	// Compare frontends
	frontendOps := c.compareFrontends(current, desired, &summary)
	operations = append(operations, frontendOps...)

	// Compare backends
	backendOps := c.compareBackends(current, desired, &summary)
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
