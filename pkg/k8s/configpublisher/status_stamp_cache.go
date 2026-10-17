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

import "sync"

// stampKey identifies one pod's status entry on one HAProxyCfg or auxiliary-file CR.
type stampKey struct {
	kind      string
	namespace string
	name      string
	podName   string
}

// stampedEntry is the last per-pod status apply that succeeded for a key.
type stampedEntry struct {
	// payload is the SSA body that was applied.
	payload string
	// footprint is what that apply left on the object (see podStatusFootprint);
	// empty when the response did not show it.
	footprint string
}

// statusStampCache remembers the last successful per-pod status apply per CR,
// so an apply that provably writes nothing is not sent (issues #163, #376).
//
// Departed pods, a deleted aux-file CR, and a leadership transition each
// invalidate entries; every invalidation bumps generation so an apply in flight
// past its (unlocked) Patch cannot record a value the CR no longer carries.
//
// The zero value is ready to use.
type statusStampCache struct {
	mu      sync.Mutex
	entries map[stampKey]stampedEntry
	// generation counts invalidations; commitStamp records only if it is
	// unchanged since the matching beginStamp.
	generation uint64
}

// beginStamp returns the last apply recorded for key and the generation to
// hand back to commitStamp.
func (c *statusStampCache) beginStamp(key stampKey) (last stampedEntry, ok bool, gen uint64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	last, ok = c.entries[key]
	return last, ok, c.generation
}

// commitStamp records value for key unless an invalidation bumped generation
// since beginStamp: a cleanup that raced the Patch may have removed the entry.
func (c *statusStampCache) commitStamp(key stampKey, value stampedEntry, gen uint64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.generation != gen {
		return
	}
	if c.entries == nil {
		c.entries = make(map[stampKey]stampedEntry)
	}
	c.entries[key] = value
}

// forgetPod drops every entry for podName so a recycled pod name re-stamps. It
// bumps generation even when nothing is cached: it runs before the CR is
// mutated and must cancel a stamp whose Patch landed but is not yet recorded.
func (c *statusStampCache) forgetPod(podName string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.generation++
	for key := range c.entries {
		if key.podName == podName {
			delete(c.entries, key)
		}
	}
}

// retainRunningPods drops entries for pods not in running, so a pod that left
// the fleet re-stamps if it returns with the same identity.
func (c *statusStampCache) retainRunningPods(running map[string]PodIdentity) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.generation++
	for key := range c.entries {
		if _, ok := running[key.podName]; !ok {
			delete(c.entries, key)
		}
	}
}

// forgetAuxFile drops every entry for one aux-file CR across all pods: content
// oscillation (A→B→A) recreates the SAME content-hashed CR with an empty status.
func (c *statusStampCache) forgetAuxFile(kind, namespace, name string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.generation++
	for key := range c.entries {
		if key.kind == kind && key.namespace == namespace && key.name == name {
			delete(c.entries, key)
		}
	}
}

// retainLivePodKeys drops podName's entries whose key is not in live, so the
// content-hashed names of a superseded auxiliary set don't accumulate.
//
// It deliberately does NOT bump generation: it runs on every status update and
// bumping would drop concurrent records for other pods. That is safe only
// because callers are single-writer-per-pod (see UpdateDeploymentStatus).
func (c *statusStampCache) retainLivePodKeys(podName string, live map[stampKey]struct{}) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for key := range c.entries {
		if key.podName != podName {
			continue
		}
		if _, ok := live[key]; !ok {
			delete(c.entries, key)
		}
	}
}

// reset clears the whole cache: a new leader must not trust entries it did not
// record.
func (c *statusStampCache) reset() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.generation++
	c.entries = nil
}
