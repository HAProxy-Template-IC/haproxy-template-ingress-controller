package configloader

import (
	"fmt"
	"log/slog"
	"sync"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/conversion"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/events"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/resourceloader"
	busevents "gitlab.com/haproxy-haptic/haptic/pkg/events"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

const (
	// ComponentName is the unique identifier for this component.
	ComponentName = "configloader"

	// EventBufferSize is the size of the event subscription buffer.
	// Low-volume component (~1 event per config change).
	EventBufferSize = busevents.StandardSubscriberBuffer
)

// ConfigLoaderComponent subscribes to ConfigResourceChangedEvent and parses config data.
//
// This component is responsible for:
// - Converting HAProxyTemplateConfig CRD Spec to config.Config
// - Publishing ConfigParsedEvent for successfully parsed configs
// - Logging errors for conversion failures
//
// Architecture:
// This is a pure event-driven component with no knowledge of watchers or
// Kubernetes. It simply reacts to ConfigResourceChangedEvent and produces
// ConfigParsedEvent.
type ConfigLoaderComponent struct {
	*resourceloader.BaseLoader

	// name is the HAProxyTemplateConfig this controller serves. A change event
	// for any other name is ignored.
	name string

	// mu guards config and snippets, which arrive from separate watchers and so
	// change one object at a time.
	mu sync.Mutex

	// config is the latest observed HAProxyTemplateConfig. Its spec.libraryRefs
	// is the single authority for which snippets are merged and in what order.
	config *unstructured.Unstructured

	// snippets holds every observed HAProxyTemplateLibrary by name, including
	// ones nothing references — a config edit can start referencing one without
	// the object itself changing. Replaced wholesale from each snapshot, so a
	// deleted object disappears with no delete handling of its own.
	snippets map[string]*unstructured.Unstructured

	// snippetsSeen reports whether a snapshot has arrived yet. An empty map is
	// a legitimate snapshot (no snippets exist), so it cannot stand in for
	// "not yet observed" — without this the loader would render a config whose
	// references are merely unseen as though they were unresolvable.
	snippetsSeen bool
}

// NewConfigLoaderComponent creates a new ConfigLoader component.
//
// Parameters:
//   - eventBus: The EventBus to subscribe to and publish on
//   - crdName: the HAProxyTemplateConfig this controller serves
//   - logger: Structured logger for diagnostics
//
// Returns:
//   - *ConfigLoaderComponent ready to start
func NewConfigLoaderComponent(
	eventBus *busevents.EventBus,
	crdName string,
	logger *slog.Logger,
) *ConfigLoaderComponent {
	c := &ConfigLoaderComponent{
		name: crdName,
	}
	c.BaseLoader = resourceloader.NewBaseLoader(
		eventBus, logger, ComponentName, EventBufferSize, c,
		events.EventTypeConfigResourceChanged,
		events.EventTypeLibrarySetChanged,
	)
	return c
}

// ProcessEvent handles a single event from the EventBus.
func (c *ConfigLoaderComponent) ProcessEvent(event busevents.Event) {
	switch e := event.(type) {
	case *events.ConfigResourceChangedEvent:
		c.processConfigChange(e)
	case *events.LibrarySetChangedEvent:
		c.processSnippetsChange(e)
	}
}

// processConfigChange records the changed config and, once every reference it
// declares resolves, re-merges the whole set and publishes the result.
//
// Only one object changes per event, so the rest are replayed from the held
// set. A `helm upgrade` writes them one at a time, producing a burst of events
// and therefore a burst of intermediate merges; the ConfigChangeHandler's
// reinit debounce collapses those into one reinitialisation.
func (c *ConfigLoaderComponent) processConfigChange(event *events.ConfigResourceChangedEvent) {
	resource, ok := c.AssertUnstructured("ConfigResourceChangedEvent", event.Resource)
	if !ok {
		return
	}

	name := resource.GetName()
	c.Logger().Debug("Processing config resource change",
		"name", name,
		"api_version", resource.GetAPIVersion(),
		"kind", resource.GetKind(),
		"version", resource.GetResourceVersion())

	sources, complete := c.recordConfig(name, resource)
	if !complete {
		return
	}

	c.publish(sources)
}

// processSnippetsChange adopts a whole-set snapshot and re-assembles.
func (c *ConfigLoaderComponent) processSnippetsChange(event *events.LibrarySetChangedEvent) {
	observed := make(map[string]*unstructured.Unstructured, len(event.Snippets))
	for _, entry := range event.Snippets {
		// The multi-object Watcher stores indexer.ConvertedResource, which is a
		// plain map[string]any — only SingleWatcher hands out
		// *unstructured.Unstructured. Accepting just the latter silently
		// dropped every snapshot, so the loader waited for a set that had
		// already arrived and the controller never rendered.
		var object map[string]any
		switch typed := entry.(type) {
		case *unstructured.Unstructured:
			object = typed.Object
		case map[string]any:
			object = typed
		default:
			// A member we cannot read makes the snapshot unusable: treating it
			// as absent would silently unresolve a reference.
			c.Logger().Error("LibrarySetChangedEvent carries an unreadable member; ignoring this snapshot",
				"type", fmt.Sprintf("%T", entry))
			return
		}
		resource := &unstructured.Unstructured{Object: object}
		observed[resource.GetName()] = resource
	}

	sources, complete := c.recordSnippets(observed)
	if !complete {
		return
	}

	c.publish(sources)
}

// publish merges an assembled set and publishes the parsed configuration.
func (c *ConfigLoaderComponent) publish(sources []*unstructured.Unstructured) {
	merged, overrides, err := conversion.MergeSpecs(sources)
	if err != nil {
		// Fail open: the previously published config keeps serving. A torn
		// read during a rolling upgrade resolves itself on the next event.
		c.Logger().Error("Failed to merge config resources", "error", err, "config", c.name)
		return
	}
	for _, override := range overrides {
		c.Logger().Info("Config entry overridden by the last config in the merge order",
			"section", override.Section,
			"name", override.Name,
			"overridden_from", override.PreviousSource,
			"defined_by", override.WinningSource)
	}

	// ParseCRD validates the GVK (kind + apiVersion) itself.
	cfg, templateConfig, err := conversion.ParseCRD(merged)
	if err != nil {
		c.Logger().Error("Failed to process config resource", "error", err, "config", c.name)
		return
	}

	version := conversion.CompositeVersion(sources)
	c.Logger().Info("Configuration processed successfully",
		"config", c.name,
		"version", version)

	// Publish ConfigParsedEvent with both parsed config and original CRD
	// Note: SecretVersion will be empty here - it gets populated later when
	// the ConfigChangeHandler correlates with credentials.
	parsedEvent := events.NewConfigParsedEvent(cfg, templateConfig, version, "")
	parsedEvent.Sources = sourceRefs(sources)
	c.EventBus().Publish(parsedEvent)
}

// recordConfig stores the observed config and re-assembles. A config under a
// name this controller does not serve is dropped.
func (c *ConfigLoaderComponent) recordConfig(name string, resource *unstructured.Unstructured) ([]*unstructured.Unstructured, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if name != c.name {
		c.Logger().Warn("Ignoring change to a HAProxyTemplateConfig this controller does not serve",
			"name", name, "serves", c.name)
		return nil, false
	}
	// Last write wins, with no staleness check: BaseLoader dispatches
	// ProcessEvent from a single goroutine and the informer delivers events for
	// one object in order, so the newest observation is always the last one to
	// arrive. resourceVersion is opaque and not ordered, so it cannot be
	// compared to do better than this.
	c.config = resource

	return c.assemble()
}

// recordSnippets adopts the snapshot and re-assembles.
func (c *ConfigLoaderComponent) recordSnippets(observed map[string]*unstructured.Unstructured) ([]*unstructured.Unstructured, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.snippets = observed
	c.snippetsSeen = true

	return c.assemble()
}

// assemble returns the merge-ordered set the config references, or reports the
// set incomplete.
//
// A reference resolves only when the named object is present AND its
// spec.revision equals the revision the reference names. The revisions are
// compared as opaque strings and never recomputed from the content: a writer
// that applies both objects together stamps the same value on each, so a
// half-applied set shows up as a mismatch, while an operator editing a
// snippet's content in place leaves the revision alone and the edit takes
// effect immediately.
//
// An incomplete set is not rendered. Libraries deliberately override one
// another, so a missing member silently changes behaviour rather than merely
// removing it — the caller keeps serving the last-good configuration instead.
func (c *ConfigLoaderComponent) assemble() ([]*unstructured.Unstructured, bool) {
	if c.config == nil {
		c.Logger().Debug("Waiting for the HAProxyTemplateConfig", "config", c.name)
		return nil, false
	}
	refs, err := conversion.LibraryRefsOf(c.config)
	if err != nil {
		c.Logger().Error("Cannot read spec.libraryRefs", "config", c.name, "error", err)
		return nil, false
	}

	// Only wait for a snapshot when something is actually referenced. A
	// self-contained config needs no libraries, and in a cluster with none —
	// or without the CRD at all — the watch may never sync, so requiring a
	// snapshot there means never rendering.
	if len(refs) > 0 && !c.snippetsSeen {
		c.Logger().Debug("Waiting for the first HAProxyTemplateLibrary snapshot",
			"config", c.name, "references", len(refs))
		return nil, false
	}

	// The config is the last source, so its inline content wins over every
	// referenced snippet regardless of ref order.
	sources := make([]*unstructured.Unstructured, 0, len(refs)+1)
	for _, ref := range refs {
		observed, seen := c.snippets[ref.Name]
		if !seen {
			c.Logger().Info("Holding the last-good configuration: a referenced HAProxyTemplateLibrary is missing",
				"config", c.name, "snippets", ref.Name, "want_revision", ref.Revision)
			return nil, false
		}
		if got := conversion.RevisionOf(observed); got != ref.Revision {
			c.Logger().Info("Holding the last-good configuration: a referenced HAProxyTemplateLibrary is at a different revision",
				"config", c.name, "snippets", ref.Name,
				"want_revision", ref.Revision, "got_revision", got)
			return nil, false
		}
		sources = append(sources, observed)
	}
	sources = append(sources, c.config)
	return sources, true
}

// sourceRefs captures the HAProxyTemplateConfig's identity and the generation
// the merge observed, for status stamping.
//
// Only the config. The status updater writes HAProxyTemplateConfigStatus, so a
// HAProxyTemplateLibrary ref would send it Getting a name that does not exist
// as that kind — leaving the config unstamped and `kubectl get htplcfg`
// reporting nothing. ADR-0016 stamped every source because every source WAS a
// config and any of them might be the one an operator edited; under ADR-0017
// there is exactly one config and it is the object an operator owns, so
// stamping it is the whole of that guarantee.
func sourceRefs(sources []*unstructured.Unstructured) []events.ConfigSourceRef {
	config := conversion.ConfigOf(sources)
	if config == nil {
		return nil
	}
	return []events.ConfigSourceRef{{
		Namespace:  config.GetNamespace(),
		Name:       config.GetName(),
		Generation: config.GetGeneration(),
	}}
}
