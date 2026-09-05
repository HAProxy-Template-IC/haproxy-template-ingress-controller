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

package templating

import (
	"errors"
	"fmt"
	"slices"
	"strings"
)

type renderedEventSnapshotStorage struct {
	events []RenderedEvent
}

type renderedEventSnapshotAuthentication struct {
	owner   *RenderedEventSnapshot
	storage *renderedEventSnapshotStorage
	count   int
}

// RenderedEventSnapshot is an authenticated immutable event set.
type RenderedEventSnapshot struct {
	storage *renderedEventSnapshotStorage
	auth    renderedEventSnapshotAuthentication
	seal    *RenderedEventSnapshot
}

// Snapshot freezes the collector and reuses previous when every event is exact.
func (c *EventCollector) Snapshot(previous ...*RenderedEventSnapshot) (*RenderedEventSnapshot, error) {
	if c == nil {
		return nil, errors.New("recordEvent: collector is nil")
	}
	if len(previous) > 1 {
		return nil, errors.New("recordEvent: more than one previous snapshot")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.snapshot != nil {
		if err := c.snapshot.validate(); err != nil {
			return nil, err
		}
		return c.snapshot, nil
	}
	var prior *RenderedEventSnapshot
	if len(previous) == 1 && previous[0] != nil {
		if err := previous[0].validate(); err != nil {
			return nil, fmt.Errorf("recordEvent: previous snapshot: %w", err)
		}
		prior = previous[0]
	}
	events := make([]RenderedEvent, 0, len(c.events))
	for event := range c.events {
		events = append(events, cloneRenderedEvent(&event))
	}
	sortRenderedEvents(events)
	if prior != nil && slices.Equal(events, prior.storage.events) {
		c.frozen = true
		c.snapshot = prior
		return prior, nil
	}
	storage := &renderedEventSnapshotStorage{events: events}
	snapshot := &RenderedEventSnapshot{storage: storage}
	snapshot.auth = renderedEventSnapshotAuthentication{owner: snapshot, storage: storage, count: len(events)}
	snapshot.seal = snapshot
	c.frozen = true
	c.snapshot = snapshot
	return snapshot, nil
}

// ValidateAuthentication verifies the exact private ownership chain.
func (s *RenderedEventSnapshot) ValidateAuthentication() error {
	return s.validate()
}

func (s *RenderedEventSnapshot) validate() error {
	if s == nil || s.seal != s || s.storage == nil || s.auth.owner != s ||
		s.auth.storage != s.storage || s.auth.count != len(s.storage.events) {
		return errors.New("rendered event snapshot has invalid provenance")
	}
	return nil
}

// Len returns the number of events.
func (s *RenderedEventSnapshot) Len() (int, error) {
	if err := s.validate(); err != nil {
		return 0, err
	}
	return len(s.storage.events), nil
}

// SameRoot reports authenticated storage identity.
func (s *RenderedEventSnapshot) SameRoot(other *RenderedEventSnapshot) (bool, error) {
	if err := s.validate(); err != nil {
		return false, err
	}
	if err := other.validate(); err != nil {
		return false, err
	}
	return s.storage == other.storage, nil
}

// ExactEqual compares every event without trusting a digest.
func (s *RenderedEventSnapshot) ExactEqual(other *RenderedEventSnapshot) (bool, error) {
	if err := s.validate(); err != nil {
		return false, err
	}
	if err := other.validate(); err != nil {
		return false, err
	}
	return s.storage == other.storage || slices.Equal(s.storage.events, other.storage.events), nil
}

// AddedSince returns events absent from the previous authenticated set.
func (s *RenderedEventSnapshot) AddedSince(previous *RenderedEventSnapshot) ([]RenderedEvent, error) {
	if err := s.validate(); err != nil {
		return nil, err
	}
	if previous == nil {
		return cloneRenderedEvents(s.storage.events), nil
	}
	if err := previous.validate(); err != nil {
		return nil, err
	}
	if s.storage == previous.storage {
		return nil, nil
	}

	var added []RenderedEvent
	currentIndex, previousIndex := 0, 0
	for currentIndex < len(s.storage.events) {
		if previousIndex == len(previous.storage.events) {
			for ; currentIndex < len(s.storage.events); currentIndex++ {
				added = append(added, cloneRenderedEvent(&s.storage.events[currentIndex]))
			}
			break
		}
		switch compared := compareRenderedEvents(
			&s.storage.events[currentIndex], &previous.storage.events[previousIndex],
		); {
		case compared < 0:
			added = append(added, cloneRenderedEvent(&s.storage.events[currentIndex]))
			currentIndex++
		case compared > 0:
			previousIndex++
		default:
			currentIndex++
			previousIndex++
		}
	}
	return added, nil
}

// Events returns a detached compatibility view.
func (s *RenderedEventSnapshot) Events() ([]RenderedEvent, error) {
	if err := s.validate(); err != nil {
		return nil, err
	}
	return cloneRenderedEvents(s.storage.events), nil
}

func cloneRenderedEvents(events []RenderedEvent) []RenderedEvent {
	cloned := make([]RenderedEvent, len(events))
	for index := range events {
		cloned[index] = cloneRenderedEvent(&events[index])
	}
	return cloned
}

func cloneRenderedEvent(event *RenderedEvent) RenderedEvent {
	return RenderedEvent{
		Namespace: strings.Clone(event.Namespace), Name: strings.Clone(event.Name),
		APIVersion: strings.Clone(event.APIVersion), Kind: strings.Clone(event.Kind),
		Type: strings.Clone(event.Type), Reason: strings.Clone(event.Reason), Message: strings.Clone(event.Message),
	}
}
