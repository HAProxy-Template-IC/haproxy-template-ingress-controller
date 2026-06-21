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

package lifecycle

// updateStatus updates the status of a component by name.
func (r *Registry) updateStatus(name string, status Status, err error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if comp, exists := r.byName[name]; exists {
		comp.status = status
		comp.lastError = err
	}
}

// Status returns the current status of all registered components.
func (r *Registry) Status() map[string]ComponentInfo {
	r.mu.RLock()
	defer r.mu.RUnlock()

	result := make(map[string]ComponentInfo, len(r.components))

	for _, comp := range r.components {
		info := ComponentInfo{
			Name:       comp.component.Name(),
			Status:     comp.status,
			LeaderOnly: comp.leaderOnly,
		}

		if comp.lastError != nil {
			info.Error = comp.lastError.Error()
		}

		// Check health if supported, but only for running components.
		// Components in StatusStandby or StatusPending cannot meaningfully report health.
		if comp.status == StatusRunning {
			if checker, ok := comp.component.(HealthChecker); ok {
				healthy := checker.HealthCheck() == nil
				info.Healthy = &healthy
			}
		}

		result[info.Name] = info
	}

	return result
}
