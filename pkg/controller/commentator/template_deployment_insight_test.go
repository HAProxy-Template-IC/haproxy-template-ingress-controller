// Copyright 2025 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package commentator

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	ctlevents "gitlab.com/haproxy-haptic/haptic/pkg/controller/events"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane"
)

// templateInsight and deploymentInsight produce the operator-facing
// log messages that explain what happened in each pipeline phase.
// These messages flow into log scrapers and observability dashboards,
// so each format/branch is part of the operator-visible contract.
//
// Both functions had no direct test coverage despite a combined ~10
// distinct branches across event types and conditional message
// fragments. Each branch corresponds to a log message operators read
// to understand cycle activity.
//
// Three branches are particularly load-bearing:
//
// (templateInsight TemplateRenderedEvent)
//   The "(trigger: <reason>)" fragment is conditionally appended ONLY
//   when TriggerReason is non-empty. A regression that always (or
//   never) appended would break the observability pattern operators
//   use to filter logs by trigger source.
//
// (deploymentInsight InstanceDeployedEvent)
//   The "(reload triggered)" fragment is conditionally appended ONLY
//   when ReloadRequired is true. This signal is critical for
//   distinguishing zero-reload runtime updates (the optimization
//   target) from reload-causing structural changes — operators
//   measure deployment efficiency by counting these.
//
// (deploymentInsight InstanceDeploymentFailedEvent)
//   The "(retryable)" fragment is conditionally appended ONLY when
//   Retryable is true. The on-call response differs entirely between
//   retryable failures (auto-recovers) vs permanent ones (requires
//   manual intervention). A regression that flipped these would
//   silently change pager response patterns.

// emptyECommentator returns an EventCommentator with just the fields
// templateInsight / deploymentInsight touch. Neither function uses
// the ringBuffer or eventBus directly — they're pure formatters.
func emptyECommentator() *EventCommentator {
	return &EventCommentator{ringBuffer: NewRingBuffer(8)}
}

func TestTemplateInsight_RenderedEvent_TriggerReasonConditional(t *testing.T) {
	tests := []struct {
		name          string
		triggerReason string
		wantFragment  string
		whyContains   string // expected substring of the formatted insight
	}{
		{
			name:          "trigger reason present → (trigger: ...) fragment appended",
			triggerReason: "config_change",
			wantFragment:  "(trigger: config_change)",
			whyContains:   "config_change",
		},
		{
			name:          "trigger reason empty → fragment must be ABSENT (not stale brackets)",
			triggerReason: "",
			wantFragment:  "(trigger:",
			whyContains:   "Template rendered",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ec := emptyECommentator()
			evt := ctlevents.NewTemplateRenderedEvent(
				"some haproxy config text",
				nil, nil, 5, // 5 aux files
				42, // 42ms render
				tt.triggerReason,
				"checksum",
				true,
			)

			insight, _ := ec.templateInsight(evt, nil)

			require.NotEmpty(t, insight,
				"every TemplateRenderedEvent must produce a non-empty operator log message")

			assert.Contains(t, insight, "Template rendered:",
				"the message must start with the documented prefix so log "+
					"scrapers can grep for template-render events")
			assert.Contains(t, insight, "42ms",
				"render duration must always appear in the message — it's "+
					"the primary observability signal for template performance")

			if tt.triggerReason != "" {
				assert.Contains(t, insight, tt.wantFragment,
					"non-empty TriggerReason MUST surface as '(trigger: %s)' so "+
						"operators can filter logs by what kicked off the render — "+
						"a regression that dropped this would break that pattern",
					tt.triggerReason)
			} else {
				assert.NotContains(t, insight, tt.wantFragment,
					"empty TriggerReason MUST NOT produce a stale '(trigger:' "+
						"fragment — a regression that emitted '(trigger: )' would "+
						"clutter logs with empty parentheses")
			}
		})
	}
}

func TestTemplateInsight_RenderFailedEvent_PassesErrorThrough(t *testing.T) {
	// The renderer pre-formats the error message; templateInsight
	// just wraps it with "Template rendering failed:\n". A regression
	// that re-formatted (e.g. truncated, single-lined) the inner error
	// would silently strip context the renderer carefully assembled.
	ec := emptyECommentator()
	const innerErr = "line 42:\n  unknown filter 'foobar'\n  did you mean 'foobaz'?"
	evt := ctlevents.NewTemplateRenderFailedEvent("haproxy.cfg", innerErr, "stack")

	insight, attrs := ec.templateInsight(evt, nil)

	require.True(t, len(attrs) >= 2 && attrs[0] == "template",
		"the template name attribute must be in the structured args so "+
			"observability tooling can group failures by template")
	assert.Equal(t, "haproxy.cfg", attrs[1],
		"template name must be the actual TemplateName, not the inner error or other field")
	assert.Contains(t, insight, "Template rendering failed:\n",
		"the error must be prefixed with the documented header so log scrapers "+
			"can identify render-failure entries")
	assert.Contains(t, insight, innerErr,
		"the renderer's pre-formatted multi-line error MUST pass through "+
			"verbatim — a regression that single-lined or truncated would "+
			"strip the carefully-assembled context (line numbers, hints)")
}

func TestDeploymentInsight_InstanceDeployedEvent_ReloadFragmentConditional(t *testing.T) {
	tests := []struct {
		name           string
		reloadRequired bool
		wantFragment   string
		notFragment    string
	}{
		{
			name:           "reload required → '(reload triggered)' fragment appended",
			reloadRequired: true,
			wantFragment:   "(reload triggered)",
			notFragment:    "", // n/a
		},
		{
			name:           "no reload → fragment must be ABSENT (NOT '(reload not triggered)' or empty parens)",
			reloadRequired: false,
			wantFragment:   "",
			notFragment:    "(reload",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ec := emptyECommentator()
			evt := ctlevents.NewInstanceDeployedEvent(
				dataplane.Endpoint{URL: "http://1.2.3.4:5555"},
				75, // 75ms
				tt.reloadRequired,
			)

			insight, _ := ec.deploymentInsight(evt, nil)

			assert.Contains(t, insight, "75ms",
				"deploy duration must always appear")

			if tt.reloadRequired {
				assert.Contains(t, insight, tt.wantFragment,
					"reload-required deployments MUST be tagged so operators can "+
						"distinguish zero-reload runtime updates (the optimization "+
						"target) from structural changes that needed a reload")
			} else {
				assert.NotContains(t, insight, tt.notFragment,
					"non-reload deployments MUST NOT carry any reload fragment — "+
						"a regression that emitted '(reload not triggered)' or "+
						"empty parens would muddy the signal operators rely on for "+
						"counting reloads via log grep")
			}
		})
	}
}

func TestDeploymentInsight_InstanceDeploymentFailedEvent_RetryableFragmentConditional(t *testing.T) {
	// The retryable flag determines on-call response: retryable
	// failures auto-recover (no immediate action), non-retryable
	// require manual intervention. Pin both branches.
	tests := []struct {
		name       string
		retryable  bool
		wantTagged bool
		errMsg     string
	}{
		{
			name:       "retryable failure → '(retryable)' fragment appended",
			retryable:  true,
			wantTagged: true,
			errMsg:     "connection timeout",
		},
		{
			name:       "permanent failure → fragment must be ABSENT",
			retryable:  false,
			wantTagged: false,
			errMsg:     "auth failed: 401 Unauthorized",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ec := emptyECommentator()
			evt := ctlevents.NewInstanceDeploymentFailedEvent(
				dataplane.Endpoint{URL: "http://1.2.3.4:5555"},
				tt.errMsg,
				tt.retryable,
			)

			insight, _ := ec.deploymentInsight(evt, nil)

			assert.Contains(t, insight, "Instance deployment failed",
				"the failure header must be present so log scrapers can identify "+
					"failed instance deployments")
			assert.Contains(t, insight, tt.errMsg,
				"the inner error message must always surface to operators")

			if tt.wantTagged {
				assert.Contains(t, insight, "(retryable)",
					"retryable failures MUST be tagged — on-call ignores these "+
						"because the retry will recover; without the tag the on-call "+
						"would treat retryable failures as actionable incidents")
			} else {
				assert.NotContains(t, insight, "(retryable)",
					"permanent failures MUST NOT be tagged retryable — operator "+
						"would otherwise wait for a retry that won't come, missing "+
						"the manual-intervention requirement")
			}
		})
	}
}

func TestDeploymentInsight_DeploymentStartedEvent_CountsEndpoints(t *testing.T) {
	ec := emptyECommentator()
	evt := ctlevents.NewDeploymentStartedEvent([]dataplane.Endpoint{
		{URL: "http://1.2.3.4:5555"},
		{URL: "http://5.6.7.8:5555"},
		{URL: "http://9.10.11.12:5555"},
	})

	insight, attrs := ec.deploymentInsight(evt, nil)

	assert.Contains(t, insight, "3 HAProxy instances",
		"the instance count is the operator's primary signal for "+
			"deployment scope; a regression that hard-coded the count or "+
			"used the wrong endpoint slice would silently misreport")
	require.True(t, len(attrs) >= 2 && attrs[0] == "instance_count",
		"instance_count must be in structured attrs for metrics scraping")
	assert.Equal(t, 3, attrs[1],
		"the structured attribute value must match the message text — "+
			"a regression that showed N in text but M in attrs would break "+
			"correlation between log search and metrics dashboards")
}
