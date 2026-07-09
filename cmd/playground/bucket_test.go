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

package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"gitlab.com/haproxy-haptic/haptic/pkg/core/config"
)

// bucketTestConfig watches ingresses (class-filtered) and Services under two
// names: `services` (unfiltered) and `controller_services` (label-filtered) —
// exactly the GVK-collision + selector case the ingress chart has.
func bucketTestConfig() *config.Config {
	return &config.Config{
		WatchedResources: map[string]config.WatchedResource{
			"ingresses": {
				APIVersion:    "networking.k8s.io/v1",
				Resources:     "ingresses",
				FieldSelector: "spec.ingressClassName=haptic",
			},
			"services": {APIVersion: "v1", Resources: "services"},
			"controller_services": {
				APIVersion:    "v1",
				Resources:     "services",
				LabelSelector: map[string]string{"app.kubernetes.io/name": "haptic"},
			},
		},
	}
}

var bucketTestByKey = map[string]schema.GroupVersionKind{
	"networking.k8s.io/v1|ingresses": {Group: "networking.k8s.io", Version: "v1", Kind: "Ingress"},
	"v1|services":                    {Version: "v1", Kind: "Service"},
}

func bucketCounts(fixtures map[string][]any) map[string]int {
	out := map[string]int{}
	for k, v := range fixtures {
		out[k] = len(v)
	}
	return out
}

func TestParseResources(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want map[string]int
	}{
		{
			name: "kubectl List buckets by kind, applies class fieldSelector",
			in: `
apiVersion: v1
kind: List
items:
  - {apiVersion: networking.k8s.io/v1, kind: Ingress, metadata: {name: a, namespace: n}, spec: {ingressClassName: haptic}}
  - {apiVersion: networking.k8s.io/v1, kind: Ingress, metadata: {name: b, namespace: n}, spec: {ingressClassName: nginx}}
  - {apiVersion: v1, kind: Service, metadata: {name: s, namespace: n}}
`,
			// b is filtered out (wrong class); the app Service lands only in `services`.
			want: map[string]int{"ingresses": 1, "services": 1},
		},
		{
			name: "controller Service (matching labels) lands in both service buckets",
			in: `
apiVersion: v1
kind: List
items:
  - apiVersion: v1
    kind: Service
    metadata: {name: lb, namespace: n, labels: {app.kubernetes.io/name: haptic}}
`,
			want: map[string]int{"services": 1, "controller_services": 1},
		},
		{
			name: "single object (not a List)",
			in:   `{apiVersion: networking.k8s.io/v1, kind: Ingress, metadata: {name: a}, spec: {ingressClassName: haptic}}`,
			want: map[string]int{"ingresses": 1},
		},
		{
			name: "multi-document stream",
			in: `apiVersion: networking.k8s.io/v1
kind: Ingress
metadata: {name: a}
spec: {ingressClassName: haptic}
---
apiVersion: v1
kind: Service
metadata: {name: s}
`,
			want: map[string]int{"ingresses": 1, "services": 1},
		},
		{
			name: "name-keyed fixtures shape is used verbatim (no selector filtering)",
			// class 'nginx' would be filtered by the fieldSelector on the kubectl path,
			// but the fixtures shape is trusted as-is (matches the testrunner).
			in: `
ingresses:
  - {apiVersion: networking.k8s.io/v1, kind: Ingress, metadata: {name: a}, spec: {ingressClassName: nginx}}
services: []
`,
			want: map[string]int{"ingresses": 1, "services": 0},
		},
		{
			name: "unknown kind is ignored",
			in:   `{apiVersion: v1, kind: ConfigMap, metadata: {name: c}}`,
			want: map[string]int{},
		},
		{
			name: "empty input",
			in:   "   ",
			want: map[string]int{},
		},
	}

	cfg := bucketTestConfig()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, report, err := parseResources(cfg, bucketTestByKey, []byte(tt.in))
			require.NoError(t, err)
			assert.Equal(t, tt.want, bucketCounts(got))
			require.NotNil(t, report)
			// Every object that landed in a bucket must be reported as matched
			// (not dropped); the matched count matches the bucketed object count.
			matched := 0
			for _, o := range report.Objects {
				if !o.Dropped {
					assert.NotEmpty(t, o.Buckets, "matched object must list its buckets")
					matched += len(o.Buckets)
				} else {
					assert.NotEmpty(t, o.Reason, "dropped object must carry a reason")
				}
			}
			total := 0
			for _, list := range got {
				total += len(list)
			}
			assert.Equal(t, total, matched, "report matched-bucket count must equal bucketed object count")
		})
	}
}
