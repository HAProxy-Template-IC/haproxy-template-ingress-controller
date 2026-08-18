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

import "gitlab.com/haproxy-haptic/haptic/pkg/dataplane"

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
