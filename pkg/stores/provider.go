// Package stores provides abstractions for accessing and composing resource stores.
//
// This package defines:
//   - StoreProvider: Interface for accessing stores by name
//   - RealStoreProvider: Implementation that returns actual stores
//   - ValidationContext: Unified container for K8s and HTTP overlays
//   - OverlayStoreProvider: Implementation that applies ValidationContext overlays
//
// These abstractions enable "what if" scenarios by allowing components to render
// and validate configurations with proposed changes without modifying actual state.
package stores

import (
	"fmt"
)

// ContentOverlay is a marker interface for overlay types.
//
// Both K8s overlays (StoreOverlay) and HTTP overlays implement this interface,
// enabling unified handling in validation pipelines.
type ContentOverlay interface {
	// IsEmpty returns true if the overlay contains no pending changes.
	IsEmpty() bool
}

// HTTPContentOverlay represents HTTP content changes awaiting validation.
//
// This interface abstracts the HTTP overlay to avoid circular imports between
// pkg/stores and pkg/httpstore. The httpstore.HTTPOverlay type implements this.
type HTTPContentOverlay interface {
	ContentOverlay

	// GetContent returns content for the given URL.
	// Returns pending content if available, otherwise accepted content.
	GetContent(url string) (string, bool)

	// PendingURLs returns the list of URLs with pending content.
	PendingURLs() []string

	// HasPendingURL returns true if the given URL has pending content.
	HasPendingURL(url string) bool
}

// ValidationContext is a unified container for all overlay types.
//
// It holds both K8s store overlays and HTTP content overlays, enabling
// the render pipeline to apply proposed changes transparently during validation.
//
// Usage:
//
//	// For K8s-only validation (webhook admission)
//	ctx := NewValidationContext(k8sOverlays)
//
//	// For HTTP content validation
//	ctx := NewValidationContext(nil).WithHTTPOverlay(httpOverlay)
//
//	// For combined validation
//	ctx := NewValidationContext(k8sOverlays).WithHTTPOverlay(httpOverlay)
type ValidationContext struct {
	// K8sOverlays maps store names to their proposed changes.
	K8sOverlays map[string]*StoreOverlay

	// HTTPOverlay contains pending HTTP content changes.
	// Nil means no HTTP content changes to validate.
	HTTPOverlay HTTPContentOverlay
}

// NewValidationContext creates a ValidationContext with K8s overlays.
//
// Parameters:
//   - overlays: Map of store name to proposed K8s changes (can be nil)
//
// Returns a ValidationContext ready for use or further configuration.
func NewValidationContext(overlays map[string]*StoreOverlay) *ValidationContext {
	if overlays == nil {
		overlays = make(map[string]*StoreOverlay)
	}
	return &ValidationContext{
		K8sOverlays: overlays,
	}
}

// WithHTTPOverlay adds an HTTP overlay to the validation context.
//
// Returns the same ValidationContext for method chaining:
//
//	ctx := NewValidationContext(overlays).WithHTTPOverlay(httpOverlay)
func (c *ValidationContext) WithHTTPOverlay(overlay HTTPContentOverlay) *ValidationContext {
	c.HTTPOverlay = overlay
	return c
}

// IsEmpty returns true if the context contains no overlays.
//
// A context is empty if it has no K8s overlays AND no HTTP overlay.
func (c *ValidationContext) IsEmpty() bool {
	if c == nil {
		return true
	}

	// Check K8s overlays
	hasK8sOverlays := false
	for _, overlay := range c.K8sOverlays {
		if overlay != nil && !overlay.IsEmpty() {
			hasK8sOverlays = true
			break
		}
	}

	// Check HTTP overlay
	hasHTTPOverlay := c.HTTPOverlay != nil && !c.HTTPOverlay.IsEmpty()

	return !hasK8sOverlays && !hasHTTPOverlay
}

// HasK8sOverlays returns true if any K8s overlays are present.
func (c *ValidationContext) HasK8sOverlays() bool {
	if c == nil {
		return false
	}
	for _, overlay := range c.K8sOverlays {
		if overlay != nil && !overlay.IsEmpty() {
			return true
		}
	}
	return false
}

// HasHTTPOverlay returns true if an HTTP overlay is present.
func (c *ValidationContext) HasHTTPOverlay() bool {
	return c != nil && c.HTTPOverlay != nil && !c.HTTPOverlay.IsEmpty()
}

// Store defines the interface for storing and retrieving indexed resources.
//
// This interface mirrors pkg/k8s/Store but is defined here to maintain
// package independence. Go's implicit interface satisfaction means any type
// implementing these methods (like k8s/Store) automatically satisfies this.
type Store interface {
	// Get retrieves all resources matching the provided index keys.
	Get(keys ...string) ([]any, error)

	// List returns all resources in the store.
	List() ([]any, error)

	// Add inserts a new resource into the store with the provided index keys.
	Add(resource any, keys []string) error

	// Update modifies an existing resource in the store.
	Update(resource any, keys []string) error

	// Delete removes a resource from the store using its index keys.
	Delete(keys ...string) error

	// Clear removes all resources from the store.
	Clear() error
}

// TypesStoreAdapter wraps any type implementing the Store interface methods.
//
// This adapter is needed because k8s/types.Store and stores.Store are identical
// interfaces defined in different packages. Go's structural typing means concrete
// types implement both, but interface VALUES cannot be directly assigned across
// package boundaries.
//
// Example:
//
//	typesStore := someManager.GetStore("ingresses") // returns types.Store
//	storesStore := &TypesStoreAdapter{Inner: typesStore} // now implements stores.Store
type TypesStoreAdapter struct {
	// Inner is the underlying store to delegate to.
	// It must implement Get, List, Add, Update, Delete, Clear methods
	// with the same signatures as stores.Store.
	Inner interface {
		Get(keys ...string) ([]any, error)
		List() ([]any, error)
		Add(resource any, keys []string) error
		Update(resource any, keys []string) error
		Delete(keys ...string) error
		Clear() error
	}
}

func (a *TypesStoreAdapter) Get(keys ...string) ([]any, error) { return a.Inner.Get(keys...) }
func (a *TypesStoreAdapter) List() ([]any, error)              { return a.Inner.List() }
func (a *TypesStoreAdapter) Delete(keys ...string) error       { return a.Inner.Delete(keys...) }
func (a *TypesStoreAdapter) Clear() error                      { return a.Inner.Clear() }
func (a *TypesStoreAdapter) Add(resource any, keys []string) error {
	return a.Inner.Add(resource, keys)
}
func (a *TypesStoreAdapter) Update(resource any, keys []string) error {
	return a.Inner.Update(resource, keys)
}

// Verify TypesStoreAdapter implements Store.
var _ Store = (*TypesStoreAdapter)(nil)

// StoreProvider provides access to stores by name.
//
// This abstraction decouples consumers from how stores are obtained,
// enabling composition and overlaying of proposed changes.
type StoreProvider interface {
	// GetStore returns the store for the given name.
	// Returns nil if no store exists with that name.
	GetStore(name string) Store

	// StoreNames returns the names of all available stores.
	StoreNames() []string
}

// RealStoreProvider provides access to actual (non-overlaid) stores.
//
// This is the default implementation used during normal reconciliation.
type RealStoreProvider struct {
	stores map[string]Store
}

// NewRealStoreProvider creates a new RealStoreProvider with the given stores.
func NewRealStoreProvider(stores map[string]Store) *RealStoreProvider {
	if stores == nil {
		stores = make(map[string]Store)
	}
	return &RealStoreProvider{stores: stores}
}

func (p *RealStoreProvider) GetStore(name string) Store { return p.stores[name] }

func (p *RealStoreProvider) StoreNames() []string {
	names := make([]string, 0, len(p.stores))
	for name := range p.stores {
		names = append(names, name)
	}
	return names
}

// OverlayStoreProvider applies ValidationContext overlays to a base provider.
//
// In addition to applying K8s overlays, this provider also exposes the
// HTTP overlay for use by the render service.
//
// Usage:
//
//	ctx := NewValidationContext(k8sOverlays).WithHTTPOverlay(httpOverlay)
//	provider := NewOverlayStoreProvider(baseProvider, ctx)
//
//	// K8s stores automatically have overlays applied
//	store := provider.GetStore("ingress")  // Returns CompositeStore if overlay exists
//
//	// HTTP overlay is accessible for render service
//	httpOverlay := provider.GetHTTPOverlay()  // Returns HTTPContentOverlay
type OverlayStoreProvider struct {
	base    StoreProvider
	context *ValidationContext
}

// NewOverlayStoreProvider creates a provider that applies ValidationContext overlays.
//
// Parameters:
//   - base: The underlying store provider
//   - context: ValidationContext containing K8s and/or HTTP overlays
//
// If context is nil, behaves identically to the base provider.
func NewOverlayStoreProvider(base StoreProvider, context *ValidationContext) *OverlayStoreProvider {
	return &OverlayStoreProvider{
		base:    base,
		context: context,
	}
}

// GetStore returns a store for the given name.
//
// If a K8s overlay exists for this store in the ValidationContext,
// returns a CompositeStore that applies the overlay on top of the base store.
// Otherwise, returns the base store unchanged.
func (p *OverlayStoreProvider) GetStore(name string) Store {
	baseStore := p.base.GetStore(name)
	if baseStore == nil {
		return nil
	}

	// No context or no K8s overlays - return base store
	if p.context == nil || p.context.K8sOverlays == nil {
		return baseStore
	}

	// Check for overlay on this store
	overlay, hasOverlay := p.context.K8sOverlays[name]
	if !hasOverlay || overlay == nil {
		return baseStore
	}

	return NewCompositeStore(baseStore, overlay)
}

// StoreNames returns the names of all available stores.
func (p *OverlayStoreProvider) StoreNames() []string {
	if p.context == nil || p.context.K8sOverlays == nil {
		return p.base.StoreNames()
	}
	return mergeStoreNames(p.base.StoreNames(), p.context.K8sOverlays)
}

// GetHTTPOverlay returns the HTTP content overlay from the ValidationContext.
//
// Returns nil if:
//   - ValidationContext is nil
//   - No HTTP overlay was set
//
// The render service uses this to determine whether to include pending HTTP content.
func (p *OverlayStoreProvider) GetHTTPOverlay() HTTPContentOverlay {
	if p.context == nil {
		return nil
	}
	return p.context.HTTPOverlay
}

// IsValidationMode returns true if this provider has a non-empty ValidationContext.
//
// This can be used by components to detect whether they're rendering for
// validation (with proposed changes) vs production (with actual state).
func (p *OverlayStoreProvider) IsValidationMode() bool {
	return p.context != nil && !p.context.IsEmpty()
}

// Validate checks that all K8s overlays reference valid stores in the base provider.
func (p *OverlayStoreProvider) Validate() error {
	if p.context == nil || p.context.K8sOverlays == nil {
		return nil
	}
	return validateOverlayStores(p.base, p.context.K8sOverlays)
}

// mergeStoreNames deduplicates base store names with additional overlay names.
func mergeStoreNames[V any](baseNames []string, overlays map[string]V) []string {
	nameSet := make(map[string]struct{}, len(baseNames)+len(overlays))
	for _, name := range baseNames {
		nameSet[name] = struct{}{}
	}
	for name := range overlays {
		nameSet[name] = struct{}{}
	}
	names := make([]string, 0, len(nameSet))
	for name := range nameSet {
		names = append(names, name)
	}
	return names
}

// validateOverlayStores checks that all overlay keys reference stores that exist
// in the base provider. Returns an error for the first non-existent store found.
func validateOverlayStores[V any](base StoreProvider, overlays map[string]V) error {
	for name := range overlays {
		if base.GetStore(name) == nil {
			return fmt.Errorf("overlay references non-existent store: %s", name)
		}
	}
	return nil
}
