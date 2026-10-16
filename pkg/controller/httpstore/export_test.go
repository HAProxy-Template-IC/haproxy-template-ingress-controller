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

package httpstore

// ProbeOutagesNow runs every scheduled critical-source refetch immediately.
func (c *Component) ProbeOutagesNow() {
	o := c.outages
	o.mu.Lock()
	due := make(map[string]uint64, len(o.sources))
	for url, outage := range o.sources {
		if outage.timer != nil && outage.timer.Stop() {
			o.probes.Done()
		}
		due[url] = outage.generation
	}
	o.mu.Unlock()
	for url, generation := range due {
		c.probeOutage(url, generation)
	}
}

// Started reports whether Start has installed the component's context.
func (c *Component) Started() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.ctx != nil
}

// RecoveredInputs counts refetched contents waiting for a render to adopt them.
func (c *Component) RecoveredInputs() int {
	c.outages.mu.Lock()
	defer c.outages.mu.Unlock()
	return len(c.outages.recovered)
}
