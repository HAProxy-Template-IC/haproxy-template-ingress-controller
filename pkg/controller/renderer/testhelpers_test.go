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

package renderer

import "gitlab.com/haproxy-haptic/haptic/pkg/dataplane"

// defaultCapabilities returns HAProxy 3.2+ capabilities for tests.
func defaultCapabilities() dataplane.Capabilities {
	return dataplane.CapabilitiesFromVersion(&dataplane.Version{Major: 3, Minor: 2, Full: "3.2.0"})
}
