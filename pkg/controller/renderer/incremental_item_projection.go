// Copyright 2026 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package renderer

import (
	"context"
	"fmt"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/rendercontext"
	"gitlab.com/haproxy-haptic/haptic/pkg/incremental"
)

func (r *incrementalRenderSession) projectComponentItem(
	ctx context.Context,
	reader incremental.Reader,
	component *incrementalComponent,
	source string,
	item map[string]any,
) (projected map[string]any, encoded []byte, applied bool, err error) {
	if component.deriveResource {
		return item, nil, false, nil
	}
	if _, supported := r.state.deriveSources[source]; !supported {
		return item, nil, false, nil
	}
	owner, err := reader.ExactInput(deriveOwnerInputKey(source))
	if err != nil {
		return nil, nil, false, err
	}
	if !owner.Found {
		return item, nil, false, nil
	}
	componentOwner, exists := r.bindingPlan.owners[source]
	if !exists || string(owner.Value) != componentOwner.name {
		return nil, nil, false, fmt.Errorf(
			"incremental deriveResource owner for %q does not match its binding",
			source,
		)
	}
	projected, encoded, err = projectComponentItem(r.incrementalDerivedResources(ctx, reader), source, item)
	return projected, encoded, true, err
}

func (r *incrementalRenderSession) projectActivationItem(
	ctx context.Context,
	reader incremental.Reader,
	source string,
	item map[string]any,
	encoded []byte,
) (projected map[string]any, projectedBytes []byte, err error) {
	if _, supported := r.state.deriveSources[source]; !supported {
		return item, encoded, nil
	}
	owner, err := reader.ExactInput(deriveOwnerInputKey(source))
	if err != nil {
		return nil, nil, err
	}
	if !owner.Found {
		return item, encoded, nil
	}
	componentOwner, exists := r.bindingPlan.owners[source]
	if !exists || string(owner.Value) != componentOwner.name {
		return nil, nil, fmt.Errorf("incremental deriveResource owner for %q does not match its binding", source)
	}
	return projectComponentItem(r.incrementalDerivedResources(ctx, reader), source, item)
}

func projectComponentItem(
	view *rendercontext.DerivedResourceView,
	source string,
	item map[string]any,
) (result map[string]any, resultBytes []byte, err error) {
	projected, err := view.Project(source, []any{item})
	if err != nil {
		return nil, nil, err
	}
	if len(projected) != 1 {
		return nil, nil, fmt.Errorf("incremental source %q projection returned %d objects", source, len(projected))
	}
	result, ok := projected[0].(map[string]any)
	if !ok {
		return nil, nil, fmt.Errorf("incremental source %q projection returned %T", source, projected[0])
	}
	encoded, err := encodeResourceValue(result)
	if err != nil {
		return nil, nil, err
	}
	return result, encoded, nil
}
