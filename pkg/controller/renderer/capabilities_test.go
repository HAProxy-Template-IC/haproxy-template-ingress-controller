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
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	config "gitlab.com/haproxy-haptic/haptic/pkg/core/config"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane"
	"gitlab.com/haproxy-haptic/haptic/pkg/templating"
)

func fanoutService(t *testing.T) *RenderService {
	t.Helper()
	cfg := &config.Config{
		HAProxyConfig: config.HAProxyConfig{Template: "global\n    daemon\n"},
		Dataplane:     testDataplaneConfig(),
	}
	engine, err := templating.New(map[string]string{"haproxy.cfg": cfg.HAProxyConfig.Template}, nil)
	require.NoError(t, err)
	return NewRenderService(&RenderServiceConfig{
		Engine: engine, Config: cfg, Logger: slog.Default(), Capabilities: defaultCapabilities(),
	})
}

// The webhook's render service is built after the reconciliation one and may be
// built after the fleet has already reported, so registering has to hand it the
// current value — not leave it on the controller image's own HAProxy.
func TestCapabilitiesFanoutSeedsAndUpdatesEveryService(t *testing.T) {
	fleet := dataplane.Capabilities{SupportsCrtList: false, SupportsMapStorage: true}
	fanout := NewCapabilitiesFanout(dataplane.Capabilities{SupportsCrtList: true})
	early := fanoutService(t)
	fanout.Add(early)

	fanout.SetCapabilities(fleet)
	late := fanoutService(t)
	fanout.Add(late)

	assert.Equal(t, fleet, early.currentCapabilities())
	assert.Equal(t, fleet, late.currentCapabilities(), "a service that registers later still renders what the fleet runs")
	assert.Equal(t, fleet, fanout.Capabilities())
}
