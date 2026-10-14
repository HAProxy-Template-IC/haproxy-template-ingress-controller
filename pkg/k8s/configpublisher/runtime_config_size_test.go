package configpublisher

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRuntimeConfigCompressionIncludesRecoveryPlan(t *testing.T) {
	const storageThreshold = 1 << 20
	config := strings.Repeat("backend example\n  server app 127.0.0.1:8080\n", 22000)
	plan := strings.Repeat("p", 650000)
	require.Less(t, len(config), storageThreshold)
	require.Greater(t, len(config)+len(plan), 3*storageThreshold/2)

	for _, tc := range []struct {
		name       string
		threshold  int64
		plan       string
		compressed bool
	}{
		{name: "combined payload", threshold: storageThreshold, plan: plan, compressed: true},
		{name: "config without recovery plan", threshold: storageThreshold},
		{name: "combined payload below threshold", threshold: 2 * storageThreshold, plan: plan},
		{name: "recovery plan consumes budget", threshold: 100, plan: plan, compressed: true},
		{name: "compression disabled", plan: plan},
		{name: "compression disabled with negative threshold", threshold: -1, plan: plan},
	} {
		t.Run(tc.name, func(t *testing.T) {
			publisher := &Publisher{logger: testLogger()}
			req := basePublishRequest()
			req.Config = config
			req.RetainedPlan = tc.plan
			req.CompressionThreshold = tc.threshold
			resource := publisher.buildRuntimeConfig("checkpoint", &req)
			assert.Equal(t, tc.compressed, resource.Spec.Compressed)
			recovered, err := decodeRetainedContent(resource.Spec.Content, resource.Spec.Compressed)
			require.NoError(t, err)
			assert.Equal(t, config, recovered)
			assert.Equal(t, req.Checksum, resource.Spec.Checksum)
			assert.Equal(t, tc.plan, resource.Spec.RetainedPlan)
			if tc.compressed {
				encoded, err := json.Marshal(resource)
				require.NoError(t, err)
				assert.Less(t, len(encoded), storageThreshold)
			}
		})
	}
}
