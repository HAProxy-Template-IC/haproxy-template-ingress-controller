// Copyright 2026 Philipp Hossner
// SPDX-License-Identifier: Apache-2.0

package testrunner

import (
	"context"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/core/config"
	"gitlab.com/haproxy-haptic/haptic/pkg/templating"
)

type clockAdvancingEngine struct {
	templating.Engine
}

func (e *clockAdvancingEngine) Render(ctx context.Context, name string, values map[string]any) (string, error) {
	if name == "haproxy.cfg" {
		time.Sleep(20 * time.Minute)
	}
	return e.Engine.Render(ctx, name, values)
}

func TestDeterministicRenderKeepsCycleTimeAcrossBucketBoundary(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		const resourceTemplate = `apiVersion: example.com/v1
kind: Observation
metadata:
  name: sample
spec:
  timestamp: {{ cycleTimeBucket(1200, "2006-01-02T15:04:05Z07:00") }}
`
		engine, err := templating.New(map[string]string{
			"haproxy.cfg": "global\n",
			"observation": resourceTemplate,
		}, nil)
		require.NoError(t, err)
		runner := determinismTestRunner(t)
		runner.config.K8sResources = map[string]config.K8sResource{
			"observation": {Template: resourceTemplate},
		}
		started := time.Now()
		paths := determinismRenderDeps(t, "global\n").ValidationPaths
		result, incomplete := runner.runSingleTest(t.Context(), "clock-boundary", &config.ValidationTest{}, &clockAdvancingEngine{Engine: engine}, paths)
		require.False(t, incomplete)
		assert.True(t, result.Passed, "%+v", result.Assertions)
		assert.Contains(t, result.RenderedK8sResources["observation"], started.UTC().Format(time.RFC3339))
		assert.Equal(t, 40*time.Minute, time.Since(started))
	})
}
