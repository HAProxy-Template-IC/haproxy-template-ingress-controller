package watcher

import (
	"sync"
	"time"

	"gitlab.com/haproxy-template-ic/haproxy-template-ingress-controller/pkg/k8s/types"
)

// Debouncer batches rapid resource changes into a single callback invocation.
//
// This prevents overwhelming downstream consumers with rapid successive callbacks
// when many resources change in a short time (e.g., during initial sync or
// cluster restarts).
//
// Thread-safe for concurrent access.
type Debouncer struct {
	mu                 sync.Mutex
	interval           time.Duration
	timer              *time.Timer
	stats              types.ChangeStats
	callback           types.OnChangeCallback
	store              types.Store
	pending            bool
	syncMode           bool // True during initial synchronization
	suppressDuringSync bool // True to suppress callbacks during sync
}

// NewDebouncer creates a new debouncer with the specified interval and callback.
//
// The callback will be invoked at most once per interval, with aggregated
// statistics about all changes that occurred during that interval.
//
// Parameters:
//   - interval: Minimum time between callback invocations
//   - callback: Function to call with aggregated changes
//   - store: Store to pass to callback
//   - suppressDuringSync: If true, callbacks are suppressed during initial sync
func NewDebouncer(interval time.Duration, callback types.OnChangeCallback, store types.Store, suppressDuringSync bool) *Debouncer {
	return &Debouncer{
		interval:           interval,
		callback:           callback,
		store:              store,
		stats:              types.ChangeStats{},
		pending:            false,
		syncMode:           true, // Start in sync mode
		suppressDuringSync: suppressDuringSync,
	}
}

// RecordCreate records a resource creation.
func (d *Debouncer) RecordCreate() {
	d.mu.Lock()
	defer d.mu.Unlock()

	d.stats.Created++
	d.scheduleCallback()
}

// RecordUpdate records a resource update.
func (d *Debouncer) RecordUpdate() {
	d.mu.Lock()
	defer d.mu.Unlock()

	d.stats.Modified++
	d.scheduleCallback()
}

// RecordDelete records a resource deletion.
func (d *Debouncer) RecordDelete() {
	d.mu.Lock()
	defer d.mu.Unlock()

	d.stats.Deleted++
	d.scheduleCallback()
}

// scheduleCallback schedules a callback if not already pending.
// Must be called with lock held.
func (d *Debouncer) scheduleCallback() {
	if d.pending {
		// Timer already running, just update stats
		return
	}

	// Start new timer
	d.pending = true
	d.timer = time.AfterFunc(d.interval, func() {
		d.fireCallback()
	})
}

// fireCallback invokes the callback with aggregated statistics.
func (d *Debouncer) fireCallback() {
	d.mu.Lock()

	// Get stats and reset
	stats := d.stats
	stats.IsInitialSync = d.syncMode // Set sync context
	d.stats = types.ChangeStats{}
	d.pending = false

	// Check if we should suppress during sync
	suppress := d.syncMode && d.suppressDuringSync

	d.mu.Unlock()

	// Invoke callback outside lock (unless suppressed)
	if d.callback != nil && !stats.IsEmpty() && !suppress {
		d.callback(d.store, stats)
	}
}

// Flush immediately invokes the callback with current statistics.
//
// This is useful during shutdown or sync completion to ensure pending changes are processed.
// Flush always invokes the callback regardless of suppressDuringSync setting.
func (d *Debouncer) Flush() {
	d.mu.Lock()

	// Stop pending timer if any
	if d.timer != nil {
		d.timer.Stop()
	}

	// Get stats and reset
	stats := d.stats
	stats.IsInitialSync = d.syncMode // Set sync context
	d.stats = types.ChangeStats{}
	d.pending = false

	d.mu.Unlock()

	// Invoke callback outside lock (always, even if suppressed)
	if d.callback != nil && !stats.IsEmpty() {
		d.callback(d.store, stats)
	}
}

// Stop cancels any pending callback.
func (d *Debouncer) Stop() {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.timer != nil {
		d.timer.Stop()
		d.timer = nil
	}

	d.pending = false
}

// SetSyncMode enables or disables initial sync mode.
//
// When sync mode is enabled, callbacks receive IsInitialSync=true.
// When disabled, callbacks receive IsInitialSync=false (real-time changes).
func (d *Debouncer) SetSyncMode(enabled bool) {
	d.mu.Lock()
	defer d.mu.Unlock()

	d.syncMode = enabled
}

// GetInitialCount returns the number of resources created during initial sync.
//
// This should be called after SetSyncMode(false) to get the accurate count
// of pre-existing resources that were loaded.
func (d *Debouncer) GetInitialCount() int {
	d.mu.Lock()
	defer d.mu.Unlock()

	// Return the Created count from current stats
	// This will be the accumulated count during sync
	return d.stats.Created
}
