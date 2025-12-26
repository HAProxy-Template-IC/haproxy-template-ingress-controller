package configchange

import (
	"context"
	"log/slog"

	"gitlab.com/haproxy-haptic/haptic/pkg/apis/haproxytemplate/v1alpha1"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/events"
	busevents "gitlab.com/haproxy-haptic/haptic/pkg/events"
	"gitlab.com/haproxy-haptic/haptic/pkg/generated/clientset/versioned"
	informers "gitlab.com/haproxy-haptic/haptic/pkg/generated/informers/externalversions"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/cache"
)

// CRDWatcher watches HAProxyTemplateConfig CRD changes and publishes ConfigResourceChangedEvent.
//
// This component bridges the Kubernetes informer pattern with the controller's event-driven
// architecture. It watches a single HAProxyTemplateConfig CRD instance and publishes events
// whenever the resource is added, updated, or deleted.
//
// Architecture:
// - Uses generated informer from pkg/generated/informers
// - Publishes ConfigResourceChangedEvent (same as ConfigMap watcher for compatibility)
// - Converts typed CRD to unstructured format for consistent handling downstream.
type CRDWatcher struct {
	client          versioned.Interface
	informerFactory informers.SharedInformerFactory
	eventBus        *busevents.EventBus
	logger          *slog.Logger
	namespace       string
	name            string
	stopCh          chan struct{}
}

// NewCRDWatcher creates a new CRDWatcher.
//
// Parameters:
//   - client: Kubernetes client for HAProxyTemplateConfig CRD
//   - eventBus: EventBus to publish ConfigResourceChangedEvent
//   - logger: Structured logger for diagnostics
//   - namespace: Namespace containing the HAProxyTemplateConfig resource
//   - name: Name of the HAProxyTemplateConfig resource to watch
//
// Returns:
//   - *CRDWatcher ready to start
func NewCRDWatcher(
	client versioned.Interface,
	eventBus *busevents.EventBus,
	logger *slog.Logger,
	namespace string,
	name string,
) *CRDWatcher {
	// Create informer factory for the specific namespace
	informerFactory := informers.NewSharedInformerFactoryWithOptions(
		client,
		0, // No resync period - we rely on watch events
		informers.WithNamespace(namespace),
	)

	return &CRDWatcher{
		client:          client,
		informerFactory: informerFactory,
		eventBus:        eventBus,
		logger:          logger,
		namespace:       namespace,
		name:            name,
		stopCh:          make(chan struct{}),
	}
}

// Start begins watching the HAProxyTemplateConfig CRD.
//
// This method blocks until Stop() is called or the context is canceled.
// It should typically be run in a goroutine.
//
// Example:
//
//	watcher := NewCRDWatcher(client, bus, logger, "default", "haproxy-config")
//	go watcher.Start(ctx)
func (w *CRDWatcher) Start(ctx context.Context) error {
	w.logger.Info("Starting CRD watcher",
		"namespace", w.namespace,
		"name", w.name)

	// Get the HAProxyTemplateConfig informer
	informer := w.informerFactory.HaproxyTemplateIC().V1alpha1().HAProxyTemplateConfigs()

	// Register event handlers
	_, err := informer.Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc:    w.onAdd,
		UpdateFunc: w.onUpdate,
		DeleteFunc: w.onDelete,
	})
	if err != nil {
		w.logger.Error("Failed to add event handler", "error", err)
		return err
	}

	// Start the informer factory
	w.informerFactory.Start(w.stopCh)

	// Wait for cache sync
	w.logger.Info("Waiting for CRD informer cache sync")
	if !cache.WaitForCacheSync(w.stopCh, informer.Informer().HasSynced) {
		w.logger.Error("Failed to sync CRD informer cache")
		return ctx.Err()
	}

	w.logger.Info("CRD informer cache synced, watching for changes")

	// Block until stopped
	select {
	case <-ctx.Done():
		w.logger.Info("CRD watcher stopped", "reason", ctx.Err())
		return ctx.Err()
	case <-w.stopCh:
		w.logger.Info("CRD watcher stopped")
		return nil
	}
}

// Stop gracefully stops the watcher.
func (w *CRDWatcher) Stop() {
	close(w.stopCh)
}

// onAdd handles CRD add events.
func (w *CRDWatcher) onAdd(obj interface{}) {
	config, ok := obj.(*v1alpha1.HAProxyTemplateConfig)
	if !ok {
		w.logger.Warn("onAdd: unexpected object type", "type", obj)
		return
	}

	// Filter: only watch the specific resource name
	if config.Name != w.name {
		w.logger.Debug("Ignoring add event for different resource",
			"name", config.Name,
			"expected", w.name)
		return
	}

	w.logger.Info("HAProxyTemplateConfig added",
		"namespace", config.Namespace,
		"name", config.Name,
		"resourceVersion", config.ResourceVersion)

	// Convert to unstructured for compatibility with ConfigLoader
	unstructuredConfig, err := w.toUnstructured(config)
	if err != nil {
		w.logger.Error("Failed to convert CRD to unstructured", "error", err)
		return
	}

	// Publish event
	w.eventBus.Publish(events.NewConfigResourceChangedEvent(unstructuredConfig))
}

// onUpdate handles CRD update events.
func (w *CRDWatcher) onUpdate(oldObj, newObj interface{}) {
	oldConfig, oldOk := oldObj.(*v1alpha1.HAProxyTemplateConfig)
	newConfig, newOk := newObj.(*v1alpha1.HAProxyTemplateConfig)

	if !oldOk || !newOk {
		w.logger.Warn("onUpdate: unexpected object type")
		return
	}

	// Filter: only watch the specific resource name
	if newConfig.Name != w.name {
		w.logger.Debug("Ignoring update event for different resource",
			"name", newConfig.Name,
			"expected", w.name)
		return
	}

	// Skip if resource version hasn't changed
	if oldConfig.ResourceVersion == newConfig.ResourceVersion {
		w.logger.Debug("Skipping update with same resourceVersion",
			"resourceVersion", newConfig.ResourceVersion)
		return
	}

	w.logger.Info("HAProxyTemplateConfig updated",
		"namespace", newConfig.Namespace,
		"name", newConfig.Name,
		"oldResourceVersion", oldConfig.ResourceVersion,
		"newResourceVersion", newConfig.ResourceVersion)

	// Convert to unstructured for compatibility with ConfigLoader
	unstructuredConfig, err := w.toUnstructured(newConfig)
	if err != nil {
		w.logger.Error("Failed to convert CRD to unstructured", "error", err)
		return
	}

	// Publish event
	w.eventBus.Publish(events.NewConfigResourceChangedEvent(unstructuredConfig))
}

// onDelete handles CRD delete events.
func (w *CRDWatcher) onDelete(obj interface{}) {
	config, ok := obj.(*v1alpha1.HAProxyTemplateConfig)
	if !ok {
		// Handle DeletedFinalStateUnknown
		tombstone, ok := obj.(cache.DeletedFinalStateUnknown)
		if !ok {
			w.logger.Warn("onDelete: unexpected object type", "type", obj)
			return
		}
		config, ok = tombstone.Obj.(*v1alpha1.HAProxyTemplateConfig)
		if !ok {
			w.logger.Warn("onDelete: tombstone contained unexpected object type")
			return
		}
	}

	// Filter: only watch the specific resource name
	if config.Name != w.name {
		w.logger.Debug("Ignoring delete event for different resource",
			"name", config.Name,
			"expected", w.name)
		return
	}

	w.logger.Warn("HAProxyTemplateConfig deleted",
		"namespace", config.Namespace,
		"name", config.Name)

	// Note: We could publish a deletion event here, but currently the controller
	// doesn't handle config deletion (it would continue using the last valid config).
	// This is consistent with ConfigMap watcher behavior.
}

// toUnstructured converts a typed HAProxyTemplateConfig to unstructured format.
//
// This maintains compatibility with existing ConfigLoader code which expects
// unstructured resources from the Kubernetes dynamic client.
func (w *CRDWatcher) toUnstructured(config *v1alpha1.HAProxyTemplateConfig) (*unstructured.Unstructured, error) {
	// Convert to unstructured using runtime conversion
	unstructuredMap, err := runtime.DefaultUnstructuredConverter.ToUnstructured(config)
	if err != nil {
		return nil, err
	}

	unstructuredConfig := &unstructured.Unstructured{Object: unstructuredMap}
	return unstructuredConfig, nil
}
