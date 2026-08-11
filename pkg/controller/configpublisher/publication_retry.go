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
	"time"
)

const (
	initialPublicationRetryBackoff = time.Second
	maxPublicationRetryBackoff     = 30 * time.Second
)

func waitForPublicationRetry(ctx context.Context, delay time.Duration, superseded <-chan struct{}) bool {
	timer := time.NewTimer(delay)
	defer timer.Stop()

	select {
	case <-timer.C:
		return true
	case <-superseded:
		return false
	case <-ctx.Done():
		return false
	}
}

func publicationRetryBackoff(retry int) time.Duration {
	backoff := initialPublicationRetryBackoff
	for range retry - 1 {
		if backoff >= maxPublicationRetryBackoff/2 {
			return maxPublicationRetryBackoff
		}
		backoff *= 2
	}
	return backoff
}

func withPublicationRetryWait(wait func(context.Context, time.Duration, <-chan struct{}) bool) Option {
	return func(c *Component) {
		c.publicationRetryWait = wait
	}
}

func supersedePublication(previous chan struct{}) chan struct{} {
	if previous != nil {
		close(previous)
	}
	return make(chan struct{})
}

func (c *Component) assignPublishAuthority(deployDriven bool) (generation, term uint64, superseded <-chan struct{}) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if deployDriven {
		return 0, c.publicationTerm, nil
	}
	c.nextPublishGeneration++
	c.latestPublishGeneration = c.nextPublishGeneration
	c.publishSuperseded = supersedePublication(c.publishSuperseded)
	return c.nextPublishGeneration, c.publicationTerm, c.publishSuperseded
}

func (c *Component) assignInvalidGeneration() (generation, term uint64, superseded <-chan struct{}) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.nextInvalidGeneration++
	c.latestInvalidGeneration = c.nextInvalidGeneration
	c.invalidSuperseded = supersedePublication(c.invalidSuperseded)
	return c.nextInvalidGeneration, c.publicationTerm, c.invalidSuperseded
}

func (c *Component) publishWorkCurrent(work *publishWorkItem) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.publishWorkCurrentLocked(work)
}

func (c *Component) publishWorkCurrentLocked(work *publishWorkItem) bool {
	if work.term != 0 && work.term != c.publicationTerm {
		return false
	}
	return work.deployDriven || work.generation == 0 || work.generation == c.latestPublishGeneration
}

func (c *Component) invalidWorkCurrent(work *validationFailedWorkItem) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.invalidWorkCurrentLocked(work)
}

func (c *Component) invalidWorkCurrentLocked(work *validationFailedWorkItem) bool {
	if work.term != 0 && work.term != c.publicationTerm {
		return false
	}
	return work.generation == 0 || work.generation == c.latestInvalidGeneration
}
