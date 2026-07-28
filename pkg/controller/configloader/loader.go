package configloader

import (
	"log/slog"
	"slices"
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

	// names is the configured merge order. A change event for a name that is
	// not in this list is ignored: the controller merges exactly the configs it
	// was told about.
	names []string

	// mu guards sources, which accumulates the latest observed object per name.
	// One watcher per name means the set arrives (and later changes) one object
	// at a time, so the component holds the others to re-merge against.
	mu      sync.Mutex
	sources map[string]*unstructured.Unstructured
}

// NewConfigLoaderComponent creates a new ConfigLoader component.
//
// Parameters:
//   - eventBus: The EventBus to subscribe to and publish on
//   - crdNames: the HAProxyTemplateConfig names to merge, in merge order
//   - logger: Structured logger for diagnostics
//
// Returns:
//   - *ConfigLoaderComponent ready to start
func NewConfigLoaderComponent(eventBus *busevents.EventBus, crdNames []string, logger *slog.Logger) *ConfigLoaderComponent {
	c := &ConfigLoaderComponent{
		names:   slices.Clone(crdNames),
		sources: make(map[string]*unstructured.Unstructured, len(crdNames)),
	}
	c.BaseLoader = resourceloader.NewBaseLoader(
		eventBus, logger, ComponentName, EventBufferSize, c,
		events.EventTypeConfigResourceChanged,
	)
	return c
}

// ProcessEvent handles a single event from the EventBus.
func (c *ConfigLoaderComponent) ProcessEvent(event busevents.Event) {
	if configEvent, ok := event.(*events.ConfigResourceChangedEvent); ok {
		c.processConfigChange(configEvent)
	}
}

// processConfigChange records the changed config and, once every configured
// name has been observed, re-merges the whole set and publishes the result.
//
// Only one object changes per event, so the others are replayed from the held
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

	sources, complete := c.record(name, resource)
	if !complete {
		return
	}

	merged, overrides, err := conversion.MergeSpecs(sources)
	if err != nil {
		// Fail open: the previously published config keeps serving. A torn
		// read during a rolling upgrade resolves itself on the next event.
		c.Logger().Error("Failed to merge config resources", "error", err, "names", c.names)
		return
	}
	for _, override := range overrides {
		c.Logger().Info("Template snippet overridden by a later config",
			"snippet", override.Name,
			"overridden_from", override.PreviousSource,
			"defined_by", override.WinningSource)
	}

	// ParseCRD validates the GVK (kind + apiVersion) itself.
	cfg, templateConfig, err := conversion.ParseCRD(merged)
	if err != nil {
		c.Logger().Error("Failed to process config resource", "error", err, "names", c.names)
		return
	}

	version := conversion.CompositeVersion(sources)
	c.Logger().Info("Configuration processed successfully",
		"names", c.names,
		"version", version)

	// Publish ConfigParsedEvent with both parsed config and original CRD
	// Note: SecretVersion will be empty here - it gets populated later when
	// the ConfigChangeHandler correlates with credentials.
	parsedEvent := events.NewConfigParsedEvent(cfg, templateConfig, version, "")
	c.EventBus().Publish(parsedEvent)
}

// record stores the observed object under its name and returns the full set in
// merge order, plus whether every configured name has been seen yet. An object
// whose name is not configured is dropped.
func (c *ConfigLoaderComponent) record(name string, resource *unstructured.Unstructured) ([]*unstructured.Unstructured, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if !slices.Contains(c.names, name) {
		c.Logger().Warn("Ignoring change to a HAProxyTemplateConfig this controller was not configured to merge",
			"name", name, "configured", c.names)
		return nil, false
	}
	// Last write wins, with no staleness check: BaseLoader dispatches
	// ProcessEvent from a single goroutine and the informer delivers events for
	// one object in order, so the newest observation is always the last one to
	// arrive. resourceVersion is opaque and not ordered, so it cannot be
	// compared to do better than this.
	c.sources[name] = resource

	sources := make([]*unstructured.Unstructured, 0, len(c.names))
	for _, configured := range c.names {
		source, seen := c.sources[configured]
		if !seen {
			c.Logger().Debug("Waiting for the rest of the configured HAProxyTemplateConfigs",
				"missing", configured)
			return nil, false
		}
		sources = append(sources, source)
	}
	return sources, true
}
