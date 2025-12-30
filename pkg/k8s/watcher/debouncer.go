package watcher

import (
	"sync"
	"time"

	"gitlab.com/haproxy-haptic/haptic/pkg/k8s/types"
)

// Debouncer batches rapid resource changes into a single callback invocation.
//
// Uses leading-edge triggering with a refractory period: if no callback has fired
// recently (within interval), the first change triggers an immediate callback.
// Subsequent changes within the refractory period are batched until the interval
// expires.
//
// This ensures fast response to isolated changes while still batching rapid
// successive changes (e.g., during initial sync or cluster restarts).
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
	lastFired          time.Time // Track when callback last fired for refractory period
	syncMode           bool      // True during initial synchronization
	suppressDuringSync bool      // True to suppress callbacks during sync
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

// scheduleCallback schedules a callback using leading-edge triggering.
//
// If no callback has fired recently (outside refractory period), fires immediately.
// If within refractory period, schedules callback for when interval expires.
// Must be called with lock held.
func (d *Debouncer) scheduleCallback() {
	if d.pending {
		// Already pending, just accumulate stats
		return
	}

	d.pending = true

	// Check if enough time has passed since last fire (refractory period)
	timeSinceLastFire := time.Since(d.lastFired)

	if timeSinceLastFire >= d.interval {
		// No recent activity - fire immediately (leading edge)
		go d.fireCallback()
	} else {
		// In refractory period - schedule for when interval expires
		remaining := d.interval - timeSinceLastFire
		d.timer = time.AfterFunc(remaining, func() {
			d.fireCallback()
		})
	}
}

// fireCallback invokes the callback with aggregated statistics.
func (d *Debouncer) fireCallback() {
	d.mu.Lock()

	// Check if we should suppress during sync
	suppress := d.syncMode && d.suppressDuringSync

	if suppress {
		// Don't fire, don't clear stats (they'll be flushed when sync ends).
		// Reset pending so new events can schedule callbacks.
		d.pending = false
		d.mu.Unlock()
		return
	}

	// Get stats and reset
	stats := d.stats
	stats.IsInitialSync = d.syncMode
	d.stats = types.ChangeStats{}
	d.pending = false
	d.lastFired = time.Now() // Record fire time for refractory period

	d.mu.Unlock()

	// Invoke callback outside lock
	if d.callback != nil && !stats.IsEmpty() {
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
	d.lastFired = time.Now() // Record fire time for refractory period

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
//
// When transitioning from sync mode to normal mode, any pending changes
// are flushed to ensure ADD events accumulated during sync are delivered.
func (d *Debouncer) SetSyncMode(enabled bool) {
	d.mu.Lock()
	wasSyncMode := d.syncMode
	d.syncMode = enabled
	hasPending := !d.stats.IsEmpty()
	d.mu.Unlock()

	// When exiting sync mode with pending changes, flush them.
	// This ensures ADD events accumulated during sync are delivered,
	// even if earlier fireCallback() calls were suppressed.
	//
	// Calling Flush() outside the lock is safe: Flush() takes its own lock,
	// and any events arriving between unlock and Flush will either be
	// included in this flush or start a new debounce cycle.
	if wasSyncMode && !enabled && hasPending {
		d.Flush()
	}
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
