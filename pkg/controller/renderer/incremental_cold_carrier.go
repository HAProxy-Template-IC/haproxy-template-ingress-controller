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

package renderer

import (
	"errors"
	"fmt"
	"sync"

	"gitlab.com/haproxy-haptic/haptic/pkg/incremental"
)

type incrementalColdCarrierRenderLane struct {
	entryPoint string
	component  *incrementalComponent
	indexes    []int
	arenaSlots []int
	prepared   []*preparedIncrementalComponent
	vector     *preparedIncrementalVectorRender
}

type incrementalColdCarrierWork struct {
	lanes     []incrementalColdCarrierRenderLane
	lifecycle *incrementalColdCarrierLifecycle
}

type incrementalColdCarrierStageResult struct {
	indexes []int
	results []incremental.ExactResult
}

type incrementalColdCarrierSegment struct {
	start     int
	end       int
	execution *incrementalVectorExecution
}

type incrementalColdCarrierLifecycle struct {
	seal     *incrementalColdCarrierLifecycle
	segments []incrementalColdCarrierSegment
	total    int

	mu       sync.Mutex
	active   int
	terminal error
	aborted  bool
}

func incrementalColdCarrierWorkerLimit(gomaxprocs int) int {
	return max(gomaxprocs, 1)
}

func newIncrementalColdCarrierLifecycle(
	lanes []incrementalColdCarrierRenderLane,
) (*incrementalColdCarrierLifecycle, error) {
	if len(lanes) == 0 {
		return nil, errors.New("incremental cold component carrier has no lifecycle lanes")
	}
	lifecycle := &incrementalColdCarrierLifecycle{active: -1}
	for index := range lanes {
		lane := &lanes[index]
		if lane.vector == nil || lane.vector.execution == nil || len(lane.prepared) == 0 ||
			len(lane.prepared) != len(lane.indexes) {
			return nil, fmt.Errorf("incremental cold component carrier lane %d is incomplete", index)
		}
		start := lifecycle.total
		lifecycle.total += len(lane.prepared)
		lifecycle.segments = append(lifecycle.segments, incrementalColdCarrierSegment{
			start:     start,
			end:       lifecycle.total,
			execution: lane.vector.execution,
		})
	}
	lifecycle.seal = lifecycle
	return lifecycle, nil
}

func (l *incrementalColdCarrierLifecycle) Begin(index int) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.validLocked() || l.terminal != nil || l.aborted {
		return errors.New("incremental cold component carrier lifecycle is terminal")
	}
	if l.active >= 0 {
		err := fmt.Errorf("incremental cold component carrier item %d is already active", l.active)
		l.abortLocked(err)
		return err
	}
	segment, local, ok := l.locateLocked(index)
	if !ok {
		err := fmt.Errorf("incremental cold component carrier item %d is invalid", index)
		l.abortLocked(err)
		return err
	}
	if err := segment.execution.Begin(local); err != nil {
		l.abortLocked(err)
		return err
	}
	l.active = index
	return nil
}

func (l *incrementalColdCarrierLifecycle) End(index int, output string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.validLocked() || l.terminal != nil || l.aborted {
		return errors.New("incremental cold component carrier lifecycle is terminal")
	}
	if l.active != index {
		err := fmt.Errorf("incremental cold component carrier item %d is not active", index)
		l.abortLocked(err)
		return err
	}
	segment, local, ok := l.locateLocked(index)
	if !ok {
		err := fmt.Errorf("incremental cold component carrier item %d is invalid", index)
		l.abortLocked(err)
		return err
	}
	if err := segment.execution.End(local, output); err != nil {
		l.abortLocked(err)
		return err
	}
	l.active = -1
	return nil
}

func (l *incrementalColdCarrierLifecycle) Abort(activeIndex int, cause error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.validLocked() || l.aborted {
		return
	}
	if activeIndex >= 0 && l.active != activeIndex && cause == nil {
		cause = fmt.Errorf("incremental cold component carrier abort item %d is not active", activeIndex)
	}
	l.abortLocked(cause)
}

func (l *incrementalColdCarrierLifecycle) validateComplete() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.validLocked() {
		return errors.New("incremental cold component carrier lifecycle has invalid provenance")
	}
	if l.terminal != nil {
		return l.terminal
	}
	if l.aborted || l.active >= 0 {
		return errors.New("incremental cold component carrier lifecycle did not complete")
	}
	return nil
}

func (l *incrementalColdCarrierLifecycle) validLocked() bool {
	return l != nil && l.seal == l && l.total > 0 && len(l.segments) > 0
}

func (l *incrementalColdCarrierLifecycle) locateLocked(
	index int,
) (*incrementalColdCarrierSegment, int, bool) {
	if index < 0 || index >= l.total {
		return nil, 0, false
	}
	for segmentIndex := range l.segments {
		segment := &l.segments[segmentIndex]
		if index >= segment.start && index < segment.end {
			return segment, index - segment.start, true
		}
	}
	return nil, 0, false
}

func (l *incrementalColdCarrierLifecycle) abortLocked(cause error) {
	if cause == nil {
		cause = errors.New("incremental cold component carrier aborted")
	}
	if l.terminal == nil {
		l.terminal = cause
	}
	if l.aborted {
		return
	}
	l.aborted = true
	for segmentIndex := range l.segments {
		segment := &l.segments[segmentIndex]
		local := -1
		if l.active >= segment.start && l.active < segment.end {
			local = l.active - segment.start
		}
		segment.execution.Abort(local, l.terminal)
	}
	l.active = -1
}

func abortIncrementalColdCarrierWorks(works []incrementalColdCarrierWork, cause error) {
	for index := range works {
		if works[index].lifecycle != nil {
			works[index].lifecycle.Abort(-1, cause)
		}
	}
}
