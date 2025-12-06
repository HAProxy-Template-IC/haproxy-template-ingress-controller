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

// RegistryBuilder provides a fluent interface for registering multiple components.
//
// The builder pattern makes component registration more organized and readable,
// especially when registering many components with different options.
//
// Example:
//
//	registry.Build().
//	    AllReplica(reconciler, renderer, validator, executor).
//	    LeaderOnly(deployer, scheduler, driftMonitor).
//	    Done()
type RegistryBuilder struct {
	registry   *Registry
	allReplica []Component
	leaderOnly []Component
}

// Build creates a new RegistryBuilder for fluent registration.
func (r *Registry) Build() *RegistryBuilder {
	return &RegistryBuilder{
		registry:   r,
		allReplica: make([]Component, 0),
		leaderOnly: make([]Component, 0),
	}
}

// AllReplica adds components that run on all replicas (not leader-only).
// These components are started when StartAll() is called.
func (b *RegistryBuilder) AllReplica(components ...Component) *RegistryBuilder {
	b.allReplica = append(b.allReplica, components...)
	return b
}

// LeaderOnly adds components that only run on the leader instance.
// These components are started when StartLeaderOnlyComponents() is called
// after leadership is acquired.
func (b *RegistryBuilder) LeaderOnly(components ...Component) *RegistryBuilder {
	b.leaderOnly = append(b.leaderOnly, components...)
	return b
}

// Done completes the registration and adds all components to the registry.
// Returns the total number of components registered.
func (b *RegistryBuilder) Done() int {
	for _, c := range b.allReplica {
		b.registry.Register(c)
	}
	for _, c := range b.leaderOnly {
		b.registry.Register(c, LeaderOnly())
	}

	total := len(b.allReplica) + len(b.leaderOnly)

	b.registry.logger.Info("Components registered with lifecycle registry",
		"all_replica_count", len(b.allReplica),
		"leader_only_count", len(b.leaderOnly),
		"total", total)

	return total
}
