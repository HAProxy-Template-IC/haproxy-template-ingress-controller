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

// Option configures a component registration.
type Option func(*registrationConfig)

// registrationConfig holds configuration for a component registration.
type registrationConfig struct {
	leaderOnly bool
}

// LeaderOnly marks the component to only run when this instance is the leader.
//
// Leader-only components are started when leadership is acquired and stopped
// when leadership is lost.
//
// Example:
//
//	registry.Register(deployer.New(bus), lifecycle.LeaderOnly())
func LeaderOnly() Option {
	return func(c *registrationConfig) {
		c.leaderOnly = true
	}
}
