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

package webhook

import (
	"context"
	"errors"
	"sync"
	"time"
)

type requestActivity struct {
	mu       sync.Mutex
	active   int
	last     time.Time
	draining bool
	changed  chan struct{}
}

func (a *requestActivity) notify() {
	a.last = time.Now()
	if a.changed != nil {
		select {
		case a.changed <- struct{}{}:
		default:
		}
	}
}

func (a *requestActivity) start() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.active++
	a.notify()
	return a.draining
}

func (a *requestActivity) finish() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.active--
	a.notify()
}

// WaitForQuiet keeps validating until requests stop arriving and all active requests finish.
// The caller withdraws readiness first and bounds the wait with ctx.
func (s *Server) WaitForQuiet(ctx context.Context, quietPeriod time.Duration) error {
	a := &s.activity
	a.mu.Lock()
	a.draining = true
	a.changed = make(chan struct{}, 1)
	a.last = time.Now()
	changed := a.changed
	a.mu.Unlock()

	timer := time.NewTimer(quietPeriod)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-changed:
		case <-timer.C:
		}
		a.mu.Lock()
		active, quietFor := a.active, time.Since(a.last)
		a.mu.Unlock()
		if active == 0 && quietFor >= quietPeriod {
			return nil
		}
		timer.Reset(max(quietPeriod-quietFor, quietPeriod/10))
	}
}

// Shutdown closes the listener and finishes active responses before retiring validators.
// The caller must keep validator dependencies alive until it returns.
func (s *Server) Shutdown(ctx context.Context) error {
	s.mu.RLock()
	server := s.httpServer
	s.mu.RUnlock()
	if server == nil {
		return nil
	}
	s.shutdownOnce.Do(func() {
		close(s.shutdownStarted)
		s.shutdownErr = server.Shutdown(ctx)
		if s.shutdownErr != nil {
			s.shutdownErr = errors.Join(s.shutdownErr, server.Close())
		}
		close(s.shutdownDone)
	})
	return s.shutdownErr
}
