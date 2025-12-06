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
	leaderOnly   bool
	dependencies []string
	criticality  CriticalityLevel
	onError      ErrorHandler
}

// ErrorHandler is called when a component encounters an error.
type ErrorHandler func(componentName string, err error)

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

// DependsOn specifies components that must be running before this component starts.
//
// Dependencies are started in order, and this component won't start until
// all dependencies are in StatusRunning state.
//
// Example:
//
//	registry.Register(deployer.New(bus), lifecycle.DependsOn("validator", "renderer"))
func DependsOn(names ...string) Option {
	return func(c *registrationConfig) {
		c.dependencies = append(c.dependencies, names...)
	}
}

// Criticality sets the importance level of the component.
//
// Critical components cause system-wide health check failures if they fail.
// Degradable components allow the system to continue with reduced functionality.
// Optional components don't affect overall system health.
//
// Example:
//
//	registry.Register(metrics.New(), lifecycle.Criticality(lifecycle.CriticalityOptional))
func Criticality(level CriticalityLevel) Option {
	return func(c *registrationConfig) {
		c.criticality = level
	}
}

// OnError sets a custom error handler for the component.
//
// The handler is called when the component's Start method returns an error.
// This can be used for custom logging, alerting, or recovery logic.
//
// Example:
//
//	registry.Register(validator.New(bus), lifecycle.OnError(func(name string, err error) {
//	    logger.Error("Component failed", "component", name, "error", err)
//	    alerting.Send(fmt.Sprintf("Component %s failed: %v", name, err))
//	}))
func OnError(handler ErrorHandler) Option {
	return func(c *registrationConfig) {
		c.onError = handler
	}
}
