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

package renderer

import (
	"sync"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane"
)

// CapabilitiesFanout keeps every RenderService that feeds a gate on one
// capability set. Admission and production must render the same config for the
// same objects: a webhook still branching on the controller image's own HAProxy
// admits a config the leader then renders differently, and nothing checked that
// one. Services register at different points in startup, so one that registers
// later is seeded with what the fleet last reported.
type CapabilitiesFanout struct {
	mu       sync.Mutex
	current  dataplane.Capabilities
	services []*RenderService
}

// NewCapabilitiesFanout starts from the controller image's own HAProxy, which
// is what every render uses until the fleet has reported.
func NewCapabilitiesFanout(seed dataplane.Capabilities) *CapabilitiesFanout {
	return &CapabilitiesFanout{current: seed}
}

// Add registers a service and hands it what the fleet last reported.
func (f *CapabilitiesFanout) Add(service *RenderService) {
	if service == nil {
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.services = append(f.services, service)
	service.SetCapabilities(f.current)
}

// SetCapabilities re-sources every registered service from the fleet's lowest
// reported HAProxy version.
func (f *CapabilitiesFanout) SetCapabilities(capabilities dataplane.Capabilities) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.current = capabilities
	for _, service := range f.services {
		service.SetCapabilities(capabilities)
	}
}

// Capabilities is what the fleet last reported.
func (f *CapabilitiesFanout) Capabilities() dataplane.Capabilities {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.current
}

// SetCapabilities re-sources what templates read as `capabilities` from the
// fleet's lowest reported HAProxy version. The controller image's own binary
// seeds the value so the first render is not degraded; discovery replaces it
// once the pods have reported.
func (s *RenderService) SetCapabilities(capabilities dataplane.Capabilities) {
	s.capsMu.Lock()
	defer s.capsMu.Unlock()
	s.capabilities = capabilities
}

func (s *RenderService) currentCapabilities() dataplane.Capabilities {
	s.capsMu.RLock()
	defer s.capsMu.RUnlock()
	return s.capabilities
}
