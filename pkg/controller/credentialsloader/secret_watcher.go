package credentialsloader

import (
	"context"
	"log/slog"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/events"
	busevents "gitlab.com/haproxy-haptic/haptic/pkg/events"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
)

// SecretWatcher watches a specific Secret and publishes SecretResourceChangedEvent.
//
// This component bridges the Kubernetes informer pattern with the controller's event-driven
// architecture. It watches a single Secret (referenced by HAProxyTemplateConfig.CredentialsSecretRef)
// and publishes events whenever the Secret is added, updated, or deleted.
//
// Architecture:
// - Uses core Kubernetes informers (not generated)
// - Publishes SecretResourceChangedEvent (consumed by CredentialsLoaderComponent)
// - Converts typed Secret to unstructured format for consistent handling.
type SecretWatcher struct {
	client          kubernetes.Interface
	informerFactory informers.SharedInformerFactory
	eventBus        *busevents.EventBus
	logger          *slog.Logger
	namespace       string
	name            string
	stopCh          chan struct{}
}

// NewSecretWatcher creates a new SecretWatcher.
//
// Parameters:
//   - client: Kubernetes client
//   - eventBus: EventBus to publish SecretResourceChangedEvent
//   - logger: Structured logger for diagnostics
//   - namespace: Namespace containing the Secret
//   - name: Name of the Secret to watch
//
// Returns:
//   - *SecretWatcher ready to start
func NewSecretWatcher(
	client kubernetes.Interface,
	eventBus *busevents.EventBus,
	logger *slog.Logger,
	namespace string,
	name string,
) *SecretWatcher {
	// Create informer factory for the specific namespace
	informerFactory := informers.NewSharedInformerFactoryWithOptions(
		client,
		0, // No resync period - we rely on watch events
		informers.WithNamespace(namespace),
	)

	return &SecretWatcher{
		client:          client,
		informerFactory: informerFactory,
		eventBus:        eventBus,
		logger:          logger,
		namespace:       namespace,
		name:            name,
		stopCh:          make(chan struct{}),
	}
}

// Start begins watching the Secret.
//
// This method blocks until Stop() is called or the context is canceled.
// It should typically be run in a goroutine.
//
// Example:
//
//	watcher := NewSecretWatcher(client, bus, logger, "default", "haproxy-credentials")
//	go watcher.Start(ctx)
func (w *SecretWatcher) Start(ctx context.Context) error {
	w.logger.Info("Starting Secret watcher",
		"namespace", w.namespace,
		"name", w.name)

	// Get the Secret informer
	informer := w.informerFactory.Core().V1().Secrets()

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
	w.logger.Info("Waiting for Secret informer cache sync")
	if !cache.WaitForCacheSync(w.stopCh, informer.Informer().HasSynced) {
		w.logger.Error("Failed to sync Secret informer cache")
		return ctx.Err()
	}

	w.logger.Info("Secret informer cache synced, watching for changes")

	// Block until stopped
	select {
	case <-ctx.Done():
		w.logger.Info("Secret watcher stopped", "reason", ctx.Err())
		return ctx.Err()
	case <-w.stopCh:
		w.logger.Info("Secret watcher stopped")
		return nil
	}
}

// Stop gracefully stops the watcher.
func (w *SecretWatcher) Stop() {
	close(w.stopCh)
}

// onAdd handles Secret add events.
func (w *SecretWatcher) onAdd(obj interface{}) {
	secret, ok := obj.(*corev1.Secret)
	if !ok {
		w.logger.Warn("onAdd: unexpected object type", "type", obj)
		return
	}

	// Filter: only watch the specific Secret name
	if secret.Name != w.name {
		w.logger.Debug("Ignoring add event for different Secret",
			"name", secret.Name,
			"expected", w.name)
		return
	}

	w.logger.Info("Secret added",
		"namespace", secret.Namespace,
		"name", secret.Name,
		"resourceVersion", secret.ResourceVersion)

	// Convert to unstructured for compatibility with CredentialsLoader
	unstructuredSecret, err := w.toUnstructured(secret)
	if err != nil {
		w.logger.Error("Failed to convert Secret to unstructured", "error", err)
		return
	}

	// Publish event
	w.eventBus.Publish(events.NewSecretResourceChangedEvent(unstructuredSecret))
}

// onUpdate handles Secret update events.
func (w *SecretWatcher) onUpdate(oldObj, newObj interface{}) {
	oldSecret, oldOk := oldObj.(*corev1.Secret)
	newSecret, newOk := newObj.(*corev1.Secret)

	if !oldOk || !newOk {
		w.logger.Warn("onUpdate: unexpected object type")
		return
	}

	// Filter: only watch the specific Secret name
	if newSecret.Name != w.name {
		w.logger.Debug("Ignoring update event for different Secret",
			"name", newSecret.Name,
			"expected", w.name)
		return
	}

	// Skip if resource version hasn't changed
	if oldSecret.ResourceVersion == newSecret.ResourceVersion {
		w.logger.Debug("Skipping update with same resourceVersion",
			"resourceVersion", newSecret.ResourceVersion)
		return
	}

	w.logger.Info("Secret updated",
		"namespace", newSecret.Namespace,
		"name", newSecret.Name,
		"oldResourceVersion", oldSecret.ResourceVersion,
		"newResourceVersion", newSecret.ResourceVersion)

	// Convert to unstructured for compatibility with CredentialsLoader
	unstructuredSecret, err := w.toUnstructured(newSecret)
	if err != nil {
		w.logger.Error("Failed to convert Secret to unstructured", "error", err)
		return
	}

	// Publish event
	w.eventBus.Publish(events.NewSecretResourceChangedEvent(unstructuredSecret))
}

// onDelete handles Secret delete events.
func (w *SecretWatcher) onDelete(obj interface{}) {
	secret, ok := obj.(*corev1.Secret)
	if !ok {
		// Handle DeletedFinalStateUnknown
		tombstone, ok := obj.(cache.DeletedFinalStateUnknown)
		if !ok {
			w.logger.Warn("onDelete: unexpected object type", "type", obj)
			return
		}
		secret, ok = tombstone.Obj.(*corev1.Secret)
		if !ok {
			w.logger.Warn("onDelete: tombstone contained unexpected object type")
			return
		}
	}

	// Filter: only watch the specific Secret name
	if secret.Name != w.name {
		w.logger.Debug("Ignoring delete event for different Secret",
			"name", secret.Name,
			"expected", w.name)
		return
	}

	w.logger.Warn("Secret deleted",
		"namespace", secret.Namespace,
		"name", secret.Name)

	// Note: We could publish a deletion event here, but the controller will
	// continue using the last known credentials. This matches ConfigMap behavior.
}

// toUnstructured converts a typed Secret to unstructured format.
//
// This maintains compatibility with existing CredentialsLoader code which expects
// unstructured resources.
func (w *SecretWatcher) toUnstructured(secret *corev1.Secret) (*unstructured.Unstructured, error) {
	// Convert to unstructured using runtime conversion
	unstructuredMap, err := runtime.DefaultUnstructuredConverter.ToUnstructured(secret)
	if err != nil {
		return nil, err
	}

	unstructuredSecret := &unstructured.Unstructured{Object: unstructuredMap}
	return unstructuredSecret, nil
}
