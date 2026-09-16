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
	"context"
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

// ValidationHandler validates a typed config without owning event responses.
type ValidationHandler interface {
	Validate(ctx context.Context, cfg *coreconfig.Config, version string) (valid bool, errors []string)
}

// BaseValidator owns request decoding and response publishing for a ValidationHandler.
// A panic produces a rejection so the scatter-gather coordinator can finish.
type BaseValidator struct {
	*component.Base
	name    string
	handler ValidationHandler
}

// NewBaseValidator subscribes the handler to config validation requests.
func NewBaseValidator(
	eventBus *busevents.EventBus,
	logger *slog.Logger,
	name string,
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

// HandleEvent implements component.EventHandler.
func (v *BaseValidator) HandleEvent(event busevents.Event) {
	if req, ok := event.(*events.ConfigValidationRequest); ok {
		v.HandleRequest(req)
	}
}

// HandleRequest owns config decoding and the single response for each validation.
func (v *BaseValidator) HandleRequest(req *events.ConfigValidationRequest) {
	cfg, ok := req.Config.(*coreconfig.Config)
	if !ok || cfg == nil {
		v.Logger().Error("ConfigValidationRequest contains invalid config type",
			"expected", "non-nil *coreconfig.Config",
			"got", fmt.Sprintf("%T", req.Config))
		v.respond(req, false, []string{fmt.Sprintf("invalid config type: %T", req.Config)})
		return
	}
	valid, errors := v.handler.Validate(v.LifecycleContext(), cfg, req.Version)
	v.respond(req, valid, errors)
}

// HandlePanic rejects the request so scatter-gather does not wait for a lost response.
func (v *BaseValidator) HandlePanic(recovered any, event busevents.Event) {
	req, ok := event.(*events.ConfigValidationRequest)
	if !ok {
		return
	}
	v.respond(req, false, []string{fmt.Sprintf("validator panicked: %v", recovered)})
}

func (v *BaseValidator) respond(req *events.ConfigValidationRequest, valid bool, errors []string) {
	v.EventBus().Publish(events.NewConfigValidationResponse(req.RequestID(), v.name, valid, errors))
}
