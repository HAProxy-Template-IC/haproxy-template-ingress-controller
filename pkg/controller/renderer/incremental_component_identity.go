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
	"slices"

	"gitlab.com/haproxy-haptic/haptic/pkg/templating"
)

func incrementalComponentsEqual(left, right *incrementalComponent) bool {
	return left.name == right.name &&
		left.entryPoint == right.entryPoint &&
		left.source == right.source &&
		left.root == right.root &&
		left.group == right.group &&
		exactStringSlicesEqual(left.consumes, right.consumes) &&
		exactStringSlicesEqual(left.optionalConsumes, right.optionalConsumes) &&
		exactExistenceJSONPathSlicesEqual(left.activationPaths, right.activationPaths) &&
		left.resourceProjection == right.resourceProjection &&
		left.deriveResource == right.deriveResource &&
		left.recordEvent == right.recordEvent &&
		left.backendPlan == right.backendPlan &&
		left.publishValue == right.publishValue &&
		left.statusPatch == right.statusPatch
}

func exactStringSlicesEqual(left, right []string) bool {
	return (left == nil) == (right == nil) && slices.Equal(left, right)
}

func exactExistenceJSONPathSlicesEqual(
	left, right []templating.ExistenceJSONPath,
) bool {
	return (left == nil) == (right == nil) && slices.EqualFunc(left, right, func(
		leftPath, rightPath templating.ExistenceJSONPath,
	) bool {
		return leftPath.Equal(rightPath)
	})
}
