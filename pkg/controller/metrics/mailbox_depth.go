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

package metrics

import (
	"github.com/prometheus/client_golang/prometheus"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/component"
)

// mailboxDepthCollector reads every running component mailbox at scrape
// time. A queued event pins the render it carries, so a deep mailbox is
// memory as much as it is latency.
type mailboxDepthCollector struct {
	desc *prometheus.Desc
}

func newMailboxDepthCollector() *mailboxDepthCollector {
	return &mailboxDepthCollector{desc: prometheus.NewDesc(
		"haptic_component_mailbox_depth",
		"Events queued in a component's coalescing mailbox, by component. Each queued event pins the render it carries.",
		[]string{"component"}, nil,
	)}
}

func (c *mailboxDepthCollector) Describe(descs chan<- *prometheus.Desc) {
	descs <- c.desc
}

func (c *mailboxDepthCollector) Collect(metrics chan<- prometheus.Metric) {
	for name, depth := range component.MailboxDepths() {
		metrics <- prometheus.MustNewConstMetric(c.desc, prometheus.GaugeValue, float64(depth), name)
	}
}
