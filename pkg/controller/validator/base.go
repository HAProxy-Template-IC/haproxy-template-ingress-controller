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

package validator

import (
	"fmt"
	"log/slog"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/component"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/events"
	coreconfig "gitlab.com/haproxy-haptic/haptic/pkg/core/config"
	busevents "gitlab.com/haproxy-haptic/haptic/pkg/events"
)

// EventBufferSize is the size of the event subscription buffer.
// Low-volume component (~1 validation request per reconciliation).
const EventBufferSize = busevents.LowVolumeSubscriberBuffer

// ValidationHandler defines the interface for validator-specific validation logic.
//
// Each validator (basic, template, jsonpath) implements this interface to provide
// their specific validation logic while reusing the common event loop infrastructure.
type ValidationHandler interface {
	// HandleRequest processes a ConfigValidationRequest and publishes a response.
	// The implementation should validate the config and publish a ConfigValidationResponse
	// event to the bus.
	HandleRequest(req *events.ConfigValidationRequest)
}

// BaseValidator wraps component.Base with validator-specific dispatch:
// it forwards only ConfigValidationRequest events to the handler, and on
// panic it publishes a failure ConfigValidationResponse so the
// scatter-gather coordinator does not time out.
type BaseValidator struct {
	*component.Base
	name    string
	handler ValidationHandler
}

// NewBaseValidator creates a new base validator with the given configuration.
//
// Parameters:
//   - eventBus: The EventBus to subscribe to and publish on
//   - logger: Structured logger for diagnostics
//   - name: Validator name (for error messages and responses)
//   - description: Human-readable component description (unused; retained for
//     API compatibility with existing validator constructors)
//   - handler: ValidationHandler implementation for validator-specific logic
//
// Returns:
//   - *BaseValidator ready to start
func NewBaseValidator(
	eventBus *busevents.EventBus,
	logger *slog.Logger,
	name string,
	_ string,
	handler ValidationHandler,
) *BaseValidator {
	v := &BaseValidator{
		name:    name,
		handler: handler,
	}
	v.Base = component.New(&component.Config{
		EventBus:   eventBus,
		Logger:     logger,
		Name:       name + "-validator",
		BufferSize: EventBufferSize,
		Handler:    v,
		EventTypes: []string{events.EventTypeConfigValidationRequest},
	})
	return v
}

// HandleEvent implements component.EventHandler. We subscribed with a type
// filter so only ConfigValidationRequest events arrive, but the type
// assertion keeps things defensive in case the filter is widened later.
func (v *BaseValidator) HandleEvent(event busevents.Event) {
	if req, ok := event.(*events.ConfigValidationRequest); ok {
		v.handler.HandleRequest(req)
	}
}

// HandlePanic implements component.PanicHandler. Publishing a failure
// response on panic keeps the scatter-gather coordinator from waiting on a
// validator that has unwound. The outer recover in component.Base is still
// responsible for keeping the event loop alive.
func (v *BaseValidator) HandlePanic(recovered any, event busevents.Event) {
	req, ok := event.(*events.ConfigValidationRequest)
	if !ok {
		return
	}
	response := events.NewConfigValidationResponse(
		req.RequestID(),
		v.name,
		false,
		[]string{fmt.Sprintf("validator panicked: %v", recovered)},
	)
	v.EventBus().Publish(response)
}

// assertConfigType type-asserts req.Config to *coreconfig.Config. On a type
// mismatch it logs the error and publishes a failure ConfigValidationResponse
// so the scatter-gather coordinator does not hang waiting on this validator,
// then returns (nil, false). Callers should early-return on a false result.
func (v *BaseValidator) assertConfigType(req *events.ConfigValidationRequest) (*coreconfig.Config, bool) {
	cfg, ok := req.Config.(*coreconfig.Config)
	if ok {
		return cfg, true
	}
	v.Logger().Error("ConfigValidationRequest contains invalid config type",
		"expected", "*coreconfig.Config",
		"got", fmt.Sprintf("%T", req.Config))
	response := events.NewConfigValidationResponse(
		req.RequestID(),
		v.name,
		false,
		[]string{fmt.Sprintf("invalid config type: %T", req.Config)},
	)
	v.EventBus().Publish(response)
	return nil, false
}
