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

package configpublisher

import (
	"context"
	"sync"
	"time"
)

type delayedSignals struct {
	mu      sync.Mutex
	stopCh  chan struct{}
	stopped bool
	wg      sync.WaitGroup
}

func newDelayedSignals() *delayedSignals {
	return &delayedSignals{stopCh: make(chan struct{})}
}

func (s *delayedSignals) Schedule(ctx context.Context, delay time.Duration, target chan<- struct{}) {
	s.mu.Lock()
	if s.stopped || ctx.Err() != nil {
		s.mu.Unlock()
		return
	}
	stopCh := s.stopCh
	s.wg.Go(func() {
		timer := time.NewTimer(delay)
		defer timer.Stop()

		select {
		case <-timer.C:
			s.mu.Lock()
			defer s.mu.Unlock()
			if s.stopped || ctx.Err() != nil {
				return
			}
			select {
			case target <- struct{}{}:
			default:
			}
		case <-ctx.Done():
		case <-stopCh:
		}
	})
	s.mu.Unlock()
}

func (s *delayedSignals) Stop() {
	s.mu.Lock()
	if !s.stopped {
		s.stopped = true
		close(s.stopCh)
	}
	s.mu.Unlock()
	s.wg.Wait()
}
