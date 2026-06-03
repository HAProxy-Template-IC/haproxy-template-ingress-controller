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

package testrunner

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSingularizeResourceType(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		// -es plural after a sibilant (s, x, z, ch, sh)
		{name: "ingresses (sibilant -ss)", in: "ingresses", want: "Ingress"},
		{name: "boxes (sibilant -x)", in: "boxes", want: "Box"},
		{name: "buzzes (sibilant -z)", in: "buzzes", want: "Buzz"},
		{name: "branches (sibilant -ch)", in: "branches", want: "Branch"},
		{name: "dishes (sibilant -sh)", in: "dishes", want: "Dish"},

		// -s plural after a non-sibilant: "-es" must NOT be greedily stripped
		{name: "services (non-sibilant)", in: "services", want: "Service"},
		{name: "namespaces (non-sibilant)", in: "namespaces", want: "Namespace"},
		{name: "pods", in: "pods", want: "Pod"},
		{name: "configmaps", in: "configmaps", want: "Configmap"},
		{name: "deployments", in: "deployments", want: "Deployment"},

		// Already singular / unknown ending
		{name: "single character", in: "x", want: "X"},
		{name: "no suffix found", in: "pod", want: "Pod"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, SingularizeResourceType(tt.in))
		})
	}
}
