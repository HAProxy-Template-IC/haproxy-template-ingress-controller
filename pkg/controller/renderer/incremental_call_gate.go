// Copyright 2026 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package renderer

import "sync"

// incrementalCallGate keeps lifecycle changes out of in-flight capability
// calls. It isn't a sync.RWMutex because calls nest (shared.Publish, then its
// recorder): a nested read lock behind a waiting writer deadlocks, so here only
// a writer that holds the gate blocks readers.
type incrementalCallGate struct {
	mu             sync.Mutex
	changed        *sync.Cond
	readers        int
	writing        bool
	waitingWriters int
}

func (g *incrementalCallGate) RLock() {
	g.mu.Lock()
	defer g.mu.Unlock()
	for g.writing {
		g.cond().Wait()
	}
	g.readers++
}

func (g *incrementalCallGate) RUnlock() {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.readers == 0 {
		panic("incremental call gate released more reads than it granted")
	}
	g.readers--
	if g.readers == 0 {
		g.cond().Broadcast()
	}
}

func (g *incrementalCallGate) Lock() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.waitingWriters++
	for g.writing || g.readers > 0 {
		g.cond().Wait()
	}
	g.waitingWriters--
	g.writing = true
}

func (g *incrementalCallGate) Unlock() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.writing = false
	g.cond().Broadcast()
}

// cond is created on first use so the zero gate is ready; callers hold g.mu.
func (g *incrementalCallGate) cond() *sync.Cond {
	if g.changed == nil {
		g.changed = sync.NewCond(&g.mu)
	}
	return g.changed
}
