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

// stampKey identifies one pod's status entry on one auxiliary-file CR.
type stampKey struct {
	kind      string
	namespace string
	name      string
	podName   string
}

// stampedEntry is the per-pod status value last SSA-applied to an auxiliary-file
// CR. It is exactly what applyPodStatusToAuxiliaryFiles writes for a pod (the
// podName lives in the key), so an equal value means the next SSA Patch would
// write a byte-identical entry.
type stampedEntry struct {
	podUID       string
	podRuntimeID string
	checksum     string
}

// auxStampCache elides redundant per-pod status re-stamps on auxiliary-file CRs.
//
// Aux-file CR names are content-hashed, so a given (kind, namespace, name) has
// immutable content and a fixed per-pod entry; every re-stamp after the first
// writes an identical value through a throttled SSA Patch and buys nothing but a
// rate-limited API call (issue #163). The cache records the last value applied
// per key so the caller can skip the Patch when the value is unchanged.
//
// RULE #2: this never weakens status.deployedToPods. Only a WRITE of a value
// identical to the one already stored is elided; every value change is still
// written. High-frequency redundant re-stamps are elided; the authoritative
// periodic re-stamp still happens every drift interval (see beginStamp's force
// path), so an out-of-band strip of a pod entry self-heals within one interval
// exactly as before the cache existed. Departed pods, a deleted aux-file CR, and
// a leadership transition each invalidate the cache; every invalidation bumps
// generation so a stamp in flight past its (unlocked) Patch cannot re-cache a
// value the CR no longer carries (see commitStamp).
//
// The zero value is ready to use; a Publisher built as a struct literal needs no
// initialisation.
type auxStampCache struct {
	mu      sync.Mutex
	entries map[stampKey]stampedEntry
	// generation counts invalidations. commitStamp caches only if it is
	// unchanged since the matching beginStamp, so a removal that raced the Patch
	// cannot be masked by a late record.
	generation uint64
}

// beginStamp reports whether the SSA Patch for key can be elided — its value is
// already the last one applied and this is not a forced (drift-check) re-stamp —
// and returns the generation to hand back to commitStamp.
func (c *auxStampCache) beginStamp(key stampKey, value stampedEntry, force bool) (skip bool, gen uint64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !force {
		if prev, ok := c.entries[key]; ok && prev == value {
			return true, c.generation
		}
	}
	return false, c.generation
}

// commitStamp records value for key, unless an invalidation bumped generation
// since beginStamp. In that race a concurrent cleanup/reconcile may have removed
// the pod from the CR between the Patch and here, so caching value would assert
// a stamp the CR lacks and elide it forever; dropping the record makes the next
// update re-stamp.
func (c *auxStampCache) commitStamp(key stampKey, value stampedEntry, gen uint64) {
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
// bumps generation unconditionally: forgetPod runs before the CR is mutated and
// may find no entry yet, but it must still cancel a stamp for podName whose Patch
// already landed and whose record has not (the check-then-act race).
func (c *auxStampCache) forgetPod(podName string) {
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
// the fleet re-stamps if it returns — even a pod removed by a transient
// discovery blip that comes back with the same identity, which the per-entry
// value comparison alone would wrongly keep skipping. Bumps generation (same
// race guard as forgetPod).
func (c *auxStampCache) retainRunningPods(running map[string]PodIdentity) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.generation++
	for key := range c.entries {
		if _, ok := running[key.podName]; !ok {
			delete(c.entries, key)
		}
	}
}

// forgetAuxFile drops every entry for one aux-file CR across all pods. Aux-file
// names and checksums are deterministic functions of content, so a delete +
// recreate under content oscillation (A→B→A) brings back the SAME content-hashed
// CR with an empty status; without this the pre-delete stamps would elide the
// re-stamp and the recreated CR would permanently lack the pod. Called from the
// aux-CR delete path. Bumps generation (same race guard as forgetPod).
func (c *auxStampCache) forgetAuxFile(kind, namespace, name string) {
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
// content-hashed names of a superseded auxiliary set don't accumulate across
// set-id changes. live is the set of keys for the aux files currently referenced
// for this pod.
//
// It deliberately does NOT bump generation: it runs on every status update, so
// bumping would make concurrent stamps for other pods drop their record and
// defeat the elision. Skipping the bump is safe ONLY because it removes just
// THIS pod's superseded keys and callers are single-writer-per-pod, so no
// concurrent record for the same pod is mid-flight on a key it removes (see
// applyPodStatusToAuxiliaryFiles' precondition). A caller that runs two status
// updates for one pod concurrently must add per-pod locking or make this bump
// the generation, or the removed key could race a same-pod record and elide it.
func (c *auxStampCache) retainLivePodKeys(podName string, live map[stampKey]struct{}) {
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

// reset clears the whole cache. A new leader must re-stamp every (pod, file)
// rather than trust a cache it did not populate. Bumps generation (same race
// guard as forgetPod).
func (c *auxStampCache) reset() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.generation++
	c.entries = nil
}
