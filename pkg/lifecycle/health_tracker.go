// Copyright 2025 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package lifecycle

import (
	"fmt"
	"sync"
	"time"
)

// DefaultProcessingTimeout is the default timeout for event-driven components (2 minutes).
const DefaultProcessingTimeout = 2 * time.Minute

// HealthTracker provides stall detection for controller components.
//
// It supports two tracking modes that can be used independently or together:
//
// Activity-based tracking (for timer-based components like DriftMonitor):
//   - Call RecordActivity() whenever the timer fires
//   - CheckActivity() returns error if no activity for > timeout
//   - Use ActivityStallTimeout() to calculate timeout from interval
//
// Processing-based tracking (for event-driven components like ConfigPublisher):
//   - Call StartProcessing() before handling an event
//   - Call EndProcessing() when done (including on error paths!)
//   - CheckProcessing() returns error if processing takes > timeout
//   - Idle state (no active processing) is always healthy
//
// Components should call Check() which combines both checks.
type HealthTracker struct {
	mu              sync.RWMutex
	lastActivity    time.Time     // For activity-based tracking
	processingStart time.Time     // For processing-based tracking (zero = idle)
	activityTimeout time.Duration // Timeout for activity-based check (0 = disabled)
	processTimeout  time.Duration // Timeout for processing-based check (0 = disabled)
	componentName   string        // For error messages
}

// NewActivityTracker creates a HealthTracker for timer-based components.
// The timeout should be interval × 1.5 to allow for jitter.
func NewActivityTracker(componentName string, timeout time.Duration) *HealthTracker {
	return &HealthTracker{
		componentName:   componentName,
		activityTimeout: timeout,
		lastActivity:    time.Now(), // Initialize to avoid false positives at startup
	}
}

// NewProcessingTracker creates a HealthTracker for event-driven components.
// Default timeout is 2 minutes.
func NewProcessingTracker(componentName string, timeout time.Duration) *HealthTracker {
	if timeout == 0 {
		timeout = DefaultProcessingTimeout
	}
	return &HealthTracker{
		componentName:  componentName,
		processTimeout: timeout,
	}
}

// ActivityStallTimeout calculates the stall timeout for timer-based components.
// Returns interval × 1.5 to allow for jitter.
func ActivityStallTimeout(interval time.Duration) time.Duration {
	return time.Duration(float64(interval) * 1.5)
}

// RecordActivity updates the last activity timestamp.
// Call this whenever a timer fires or periodic work completes.
func (t *HealthTracker) RecordActivity() {
	t.mu.Lock()
	t.lastActivity = time.Now()
	t.mu.Unlock()
}

// StartProcessing marks the start of event processing.
// Call this before handling an event.
func (t *HealthTracker) StartProcessing() {
	t.mu.Lock()
	t.processingStart = time.Now()
	t.mu.Unlock()
}

// EndProcessing marks the end of event processing.
// Call this when done handling an event (including error paths!).
// Use defer immediately after StartProcessing() to ensure it's always called.
func (t *HealthTracker) EndProcessing() {
	t.mu.Lock()
	t.processingStart = time.Time{} // Zero value = idle
	t.mu.Unlock()
}

// Check performs health check and returns an error if the component appears stalled.
// This combines both activity-based and processing-based checks.
// Returns nil if healthy.
func (t *HealthTracker) Check() error {
	t.mu.RLock()
	defer t.mu.RUnlock()

	// Activity-based check (for timer components)
	if err := t.checkStall("no activity for", t.lastActivity, t.activityTimeout); err != nil {
		return err
	}

	// Processing-based check (for event-driven components)
	if err := t.checkStall("processing for", t.processingStart, t.processTimeout); err != nil {
		return err
	}

	return nil
}

// checkStall returns an error if the duration since "since" exceeds timeout.
// A zero timeout disables the check; a zero "since" timestamp means the
// component is not currently in the tracked state (e.g. processing-idle) and
// is reported healthy. label is the slug embedded in the error ("no activity
// for", "processing for") so callers can express both kinds of stall checks
// without duplicating the if/format/return pattern. Caller must hold t.mu.
func (t *HealthTracker) checkStall(label string, since time.Time, timeout time.Duration) error {
	if timeout == 0 || since.IsZero() {
		return nil
	}
	duration := time.Since(since)
	if duration <= timeout {
		return nil
	}
	return fmt.Errorf("%s stalled: %s %v (timeout: %v)",
		t.componentName, label, duration.Round(time.Second), timeout)
}
