// Copyright 2025 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package renderer

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/events"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/testutil"
	"gitlab.com/haproxy-haptic/haptic/pkg/templating"
)

// publishRenderFailure is the single funnel through which every render
// error reaches the rest of the controller. It does three things in
// strict order:
//
//  1. Looks up the raw template content via engine.GetRawTemplate (the
//     LOOKUP MAY FAIL — the function deliberately discards that error
//     so that template-not-found cases still surface a useful failure
//     event instead of swallowing the original render error).
//  2. Formats the error with templating.FormatRenderError so the
//     subscriber gets a human-readable, multi-line error string instead
//     of the raw Scriggo error.
//  3. Publishes a *TemplateRenderFailedEvent carrying the formatted
//     string in Error and the bare template name in TemplateName.
//
// Three contracts pinned here:
//
//  1. Happy path with a known template — the published event MUST be a
//     *TemplateRenderFailedEvent (not e.g. *TemplateRenderedEvent), the
//     TemplateName MUST equal the input, and the formatted Error MUST
//     contain the original error message verbatim somewhere in the body.
//     Without this pin, a refactor that re-routes failures through a
//     new event type would leave every downstream subscriber (validator,
//     deployer, commentator) silently waiting for events that never
//     arrive and the pipeline would deadlock on every render error.
//
//  2. GetRawTemplate failure MUST be tolerated — the function still
//     publishes a *TemplateRenderFailedEvent even when the template is
//     not registered with the engine. This is load-bearing: a render
//     pipeline that fails because a referenced sub-template is missing
//     would otherwise throw on the lookup, swallow the original failure,
//     and again leave subscribers in the dark.
//
//  3. The published event carries the FORMATTED error (multi-line, with
//     "Error rendering template" header), not the raw err.Error() — the
//     raw error goes only into the structured log under "error_raw".
//     Subscribers (most importantly the commentator) rely on the
//     formatted version for human-readable logging.

func TestComponent_PublishRenderFailure(t *testing.T) {
	tests := []struct {
		name           string
		registerTpl    string // template body to register, "" to skip registration
		templateName   string // name to pass to publishRenderFailure
		err            error
		wantEventType  bool   // must be *TemplateRenderFailedEvent
		wantTplName    string // expected event.TemplateName
		wantErrorBody  string // substring required in event.Error (formatted)
		wantNotInError string // substring that MUST NOT appear (catches raw-error leakage when we expect formatting)
	}{
		{
			name:          "known template — formatted error contains original message",
			registerTpl:   "global\n    daemon\n",
			templateName:  "haproxy.cfg",
			err:           errors.New("undefined: missingVariable"),
			wantEventType: true,
			wantTplName:   "haproxy.cfg",
			wantErrorBody: "undefined: missingVariable",
		},
		{
			name:          "GetRawTemplate fails (template not registered) — event still published",
			registerTpl:   "", // do not register
			templateName:  "ghost.cfg",
			err:           errors.New("simulated render failure"),
			wantEventType: true,
			wantTplName:   "ghost.cfg",
			wantErrorBody: "simulated render failure",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			bus, logger := testutil.NewTestBusAndLogger()
			eventChan := bus.Subscribe("test-sub", 10)
			bus.Start()

			// Build a real engine. If registerTpl is empty, give the
			// engine some unrelated template so it constructs cleanly
			// while still failing the GetRawTemplate lookup for the
			// caller's template name.
			templates := map[string]string{}
			if tt.registerTpl != "" {
				templates[tt.templateName] = tt.registerTpl
			} else {
				templates["__placeholder__"] = "x"
			}
			engine, err := templating.New(templating.EngineTypeScriggo, templates, nil, nil, nil)
			require.NoError(t, err)

			c := &Component{
				eventBus: bus,
				engine:   engine,
				logger:   logger,
			}

			c.publishRenderFailure(tt.templateName, tt.err)

			evt := testutil.WaitForEvent[*events.TemplateRenderFailedEvent](
				t, eventChan, testutil.LongTimeout)
			require.NotNil(t, evt,
				"publishRenderFailure MUST publish a *TemplateRenderFailedEvent — "+
					"a regression that re-routes failures through a different "+
					"event type would silently break every downstream subscriber "+
					"(validator, deployer, commentator) and stall the pipeline")

			assert.Equal(t, tt.wantTplName, evt.TemplateName,
				"event TemplateName must match the input — subscribers correlate "+
					"failures with templates by name")
			assert.Contains(t, evt.Error, tt.wantErrorBody,
				"the formatted Error string must include the original render "+
					"error message somewhere in its body — the formatter wraps "+
					"with context but must NOT discard the original cause")

			if tt.wantNotInError != "" {
				assert.NotContains(t, evt.Error, tt.wantNotInError)
			}
		})
	}
}
