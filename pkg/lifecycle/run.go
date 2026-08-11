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

package lifecycle

import "sync"

// ComponentRun owns the completion of one set of started components.
type ComponentRun struct {
	done chan struct{}

	mu  sync.RWMutex
	err error
}

func newComponentRun() *ComponentRun {
	return &ComponentRun{done: make(chan struct{})}
}

func (r *ComponentRun) finish(err error) {
	r.mu.Lock()
	r.err = err
	r.mu.Unlock()
	close(r.done)
}

// Done closes only after every Component.Start call in the run has returned.
func (r *ComponentRun) Done() <-chan struct{} {
	return r.done
}

// Wait blocks until every Component.Start call has returned.
func (r *ComponentRun) Wait() error {
	<-r.done
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.err
}
