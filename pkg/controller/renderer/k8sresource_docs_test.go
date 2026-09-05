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
	"testing"

	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/templating"
)

// An unquoted RFC3339 scalar is a YAML timestamp, which yaml.v3 decodes as
// time.Time. Kubernetes objects are JSON-shaped and carry these as strings, so
// the document has to register and reach the applier as one. The bundled
// ingress library emits an Event whose lastTimestamp is exactly this shape;
// failing here denies a legitimate Ingress at admission.
func TestRegisterK8sResourceDocsAcceptsYAMLTimestamps(t *testing.T) {
	const doc = `apiVersion: v1
kind: Event
metadata:
  name: api.backend-unresolved
  namespace: default
type: Warning
reason: BackendUnresolved
lastTimestamp: 2026-09-05T20:20:00Z
firstTimestamp: 2026-09-05T20:19:59.25Z
series:
  - lastObservedTime: 2026-09-05T20:20:00Z
`

	collector := templating.NewRenderedResourceCollector()
	err := RegisterK8sResourceDocs("ingress-degraded-backend-events", doc, collector, nil)
	require.NoError(t, err)

	resources := collector.Resources()
	require.Len(t, resources, 1)
	object := resources[0].Object
	require.Equal(t, "2026-09-05T20:20:00Z", object["lastTimestamp"],
		"a YAML timestamp must reach the applier as the RFC3339 string the Kubernetes API expects")
	require.Equal(t, "2026-09-05T20:19:59.25Z", object["firstTimestamp"],
		"a sub-second fraction must survive rather than being truncated to the second")

	series, ok := object["series"].([]any)
	require.True(t, ok, "series should stay a list, got %T", object["series"])
	nested, ok := series[0].(map[string]any)
	require.True(t, ok, "series entry should stay a map, got %T", series[0])
	require.Equal(t, "2026-09-05T20:20:00Z", nested["lastObservedTime"],
		"timestamps nested under lists and maps must be normalized too")
}
