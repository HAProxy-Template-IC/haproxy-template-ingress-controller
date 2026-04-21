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

import (
	"context"
	"fmt"
)

// validateDependencies checks that all dependencies exist and there are no cycles.
// Must be called with r.mu held.
func (r *Registry) validateDependencies(components []*registeredComponent, startSet map[string]bool) error {
	// Check for missing dependencies
	for _, comp := range components {
		for _, depName := range comp.config.dependencies {
			dep, exists := r.byName[depName]
			if !exists {
				return fmt.Errorf("component %q depends on unknown component %q",
					comp.component.Name(), depName)
			}

			// Dependency must be in the start set or already running
			if !startSet[depName] && dep.status != StatusRunning {
				return fmt.Errorf("component %q depends on %q which is not being started",
					comp.component.Name(), depName)
			}
		}
	}

	// Check for cycles using DFS
	return r.detectCycles(components)
}

// detectCycles uses DFS to detect circular dependencies.
// Must be called with r.mu held.
func (r *Registry) detectCycles(components []*registeredComponent) error {
	// Build set of components being started
	inSet := make(map[string]bool)
	for _, comp := range components {
		inSet[comp.component.Name()] = true
	}

	visited := make(map[string]bool)
	recStack := make(map[string]bool)

	var dfs func(name string) error
	dfs = func(name string) error {
		visited[name] = true
		recStack[name] = true

		comp, exists := r.byName[name]
		if !exists {
			return nil // Unknown component, skip
		}

		for _, depName := range comp.config.dependencies {
			if !inSet[depName] {
				continue // Dependency not in current start set, skip
			}

			if !visited[depName] {
				if err := dfs(depName); err != nil {
					return err
				}
			} else if recStack[depName] {
				return fmt.Errorf("circular dependency detected: %s -> %s", name, depName)
			}
		}

		recStack[name] = false
		return nil
	}

	for _, comp := range components {
		name := comp.component.Name()
		if !visited[name] {
			if err := dfs(name); err != nil {
				return err
			}
		}
	}

	return nil
}

// waitForDependencies waits for all dependencies to reach StatusRunning.
func (r *Registry) waitForDependencies(ctx context.Context, comp *registeredComponent) error {
	for _, depName := range comp.config.dependencies {
		r.mu.RLock()
		dep, exists := r.byName[depName]
		r.mu.RUnlock()

		if !exists {
			// Should not happen after validation, but handle gracefully
			return fmt.Errorf("dependency %q not found", depName)
		}

		r.logger.Debug("Waiting for dependency",
			"component", comp.component.Name(),
			"dependency", depName)

		select {
		case <-dep.ready:
			r.logger.Debug("Dependency ready",
				"component", comp.component.Name(),
				"dependency", depName)
		case <-ctx.Done():
			return fmt.Errorf("context cancelled while waiting for dependency %q: %w", depName, ctx.Err())
		}
	}

	return nil
}
