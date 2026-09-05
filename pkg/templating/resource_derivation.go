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

package templating

import (
	"fmt"

	"gitlab.com/haproxy-haptic/scriggo/native"
)

// ResourceDeriverContextName is the render-context key for ResourceDeriver.
const ResourceDeriverContextName = "resourceDeriver"

// ResourceDeriver publishes a transformed resource into the current render's view.
type ResourceDeriver interface {
	DeriveResource(resource string, item any, path string, value any) (any, error)
}

func scriggoDeriveResource(env native.Env, resource string, item any, path string, value any) any {
	ctx := env.Context()
	if ctx == nil {
		env.Stop(fmt.Errorf("deriveResource(%q) has no render context", resource))
		return nil
	}
	candidate, _ := lookupRenderContextValue(ctx, ResourceDeriverContextName)
	deriver, ok := candidate.(ResourceDeriver)
	if !ok || deriver == nil {
		env.Stop(fmt.Errorf("deriveResource(%q) has no derived resource view", resource))
		return nil
	}
	inputs := []any{item, value}
	for index := range inputs {
		detached, err := cloneIncrementalSerialization(inputs[index])
		if err != nil {
			env.Stop(fmt.Errorf("deriveResource(%q): detaching input: %w", resource, err))
			return nil
		}
		inputs[index] = detached
	}
	derived, err := deriver.DeriveResource(resource, inputs[0], path, inputs[1])
	if err != nil {
		env.Stop(err)
		return nil
	}
	return derived
}
