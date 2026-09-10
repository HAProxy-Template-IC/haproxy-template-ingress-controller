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

package reconciler

import (
	"context"
	"errors"
	"fmt"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/events"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/rendercycle"
	busevents "gitlab.com/haproxy-haptic/haptic/pkg/events"
)

func waitForResourceApplication(ctx context.Context, processed <-chan busevents.Event, occurrence *rendercycle.Occurrence) error {
	if occurrence == nil {
		return nil
	}
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case event, open := <-processed:
			if !open {
				return errors.New("resource completion subscription closed")
			}
			same, err := resourceApplicationMatches(event, occurrence)
			if err != nil {
				return fmt.Errorf("resource completion: %w", err)
			}
			if same {
				return ctx.Err()
			}
		}
	}
}

func resourceApplicationMatches(event busevents.Event, occurrence *rendercycle.Occurrence) (bool, error) {
	completion, ok := event.(*events.ResourcesProcessedEvent)
	if !ok || completion == nil {
		return false, errors.New("invalid event type")
	}
	completed, err := completion.RenderOccurrence()
	if err != nil {
		return false, err
	}
	return occurrence.Same(completed)
}
