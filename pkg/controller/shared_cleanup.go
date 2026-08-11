// Copyright 2026 Philipp Hossner
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

package controller

import "sync"

type sharedCleanup struct {
	mu      sync.Mutex
	refs    int
	cleanup func()
}

func newSharedCleanup(cleanup func()) *sharedCleanup {
	return &sharedCleanup{refs: 1, cleanup: cleanup}
}

func (c *sharedCleanup) Retain() func() {
	c.mu.Lock()
	if c.refs == 0 {
		c.mu.Unlock()
		panic("retain after cleanup")
	}
	c.refs++
	c.mu.Unlock()

	var once sync.Once
	return func() {
		once.Do(c.Release)
	}
}

func (c *sharedCleanup) Release() {
	c.mu.Lock()
	if c.refs == 0 {
		c.mu.Unlock()
		return
	}
	c.refs--
	var cleanup func()
	if c.refs == 0 {
		cleanup = c.cleanup
		c.cleanup = nil
	}
	c.mu.Unlock()

	if cleanup != nil {
		cleanup()
	}
}
