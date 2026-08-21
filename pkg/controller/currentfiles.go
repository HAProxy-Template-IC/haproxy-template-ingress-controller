// Copyright 2025 Philipp Hossner
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

package controller

import (
	"maps"
	"sync"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane"
)

// currentFilesAuthority owns the accepted auxiliary output for one leader term.
type currentFilesAuthority struct {
	mu sync.RWMutex

	published   *publishedAuxFiles
	generation  uint64
	active      bool
	hasAccepted bool
	accepted    map[string]string

	// confirmed is the last baseline HAProxy accepted, and pendingPlanID names
	// the render whose files are provisional. A refusal rolls `accepted` back
	// to `confirmed`, so the term's idea of "what is deployed" never keeps a
	// render the fleet was reverted away from.
	hasConfirmed  bool
	confirmed     map[string]string
	pendingPlanID string
}

func newCurrentFilesAuthority(published *publishedAuxFiles) *currentFilesAuthority {
	return &currentFilesAuthority{published: published}
}

func (a *currentFilesAuthority) BeginTerm() uint64 {
	a.mu.Lock()
	defer a.mu.Unlock()

	a.generation++
	a.active = true
	a.hasAccepted = false
	a.accepted = nil
	a.hasConfirmed = false
	a.confirmed = nil
	a.pendingPlanID = ""
	if a.published != nil {
		a.published.beginLeaderTerm()
	}
	return a.generation
}

func (a *currentFilesAuthority) EndTerm(generation uint64) {
	a.mu.Lock()
	defer a.mu.Unlock()

	if !a.active || a.generation != generation {
		return
	}
	a.active = false
	a.hasAccepted = false
	a.accepted = nil
	a.hasConfirmed = false
	a.confirmed = nil
	a.pendingPlanID = ""
	if a.published != nil {
		a.published.endLeaderTerm()
	}
}

func (a *currentFilesAuthority) Snapshot(generation uint64) (map[string]string, error) {
	a.mu.RLock()
	if a.active && a.generation == generation && a.hasAccepted {
		files := maps.Clone(a.accepted)
		a.mu.RUnlock()
		if err := a.publishedAvailabilityError(); err != nil {
			return nil, err
		}
		return files, nil
	}
	a.mu.RUnlock()

	return a.publishedSnapshot()
}

func (a *currentFilesAuthority) publishedAvailabilityError() error {
	if a.published == nil {
		return nil
	}
	return a.published.availabilityError()
}

// Accept records the files a render produced as the term's working baseline.
//
// The next render reads them back — that is what lets a template keep the
// content it rotated instead of re-seeding it every cycle — but they are only
// PROVISIONAL until HAProxy has judged the render they came from. Confirm and
// Rollback settle them.
func (a *currentFilesAuthority) Accept(
	generation uint64, planID string, auxiliaryFiles *dataplane.AuxiliaryFiles,
) {
	files := auxiliaryFiles.CurrentFiles()

	a.mu.Lock()
	defer a.mu.Unlock()

	if !a.active || a.generation != generation {
		return
	}
	a.hasAccepted = true
	a.accepted = maps.Clone(files)
	a.pendingPlanID = planID
}

// Confirm settles the provisional baseline once the render gate passed the
// render it came from.
func (a *currentFilesAuthority) Confirm(generation uint64, planID string) {
	a.mu.Lock()
	defer a.mu.Unlock()

	if !a.active || a.generation != generation || planID == "" || planID != a.pendingPlanID {
		return
	}
	a.confirmed = maps.Clone(a.accepted)
	a.hasConfirmed = true
	a.pendingPlanID = ""
}

// Rollback puts the baseline back to the last confirmed one after HAProxy
// refused the render that produced the provisional files.
//
// Without it, the refused render's auxiliary files would be what the next
// render reads as "what is deployed" — and the fleet was reverted away from
// exactly those, so the two would disagree for the rest of the term.
func (a *currentFilesAuthority) Rollback(generation uint64, planID string) {
	a.mu.Lock()
	defer a.mu.Unlock()

	if !a.active || a.generation != generation || planID == "" || planID != a.pendingPlanID {
		return
	}
	a.pendingPlanID = ""
	if !a.hasConfirmed {
		// Nothing was ever confirmed this term: fall back to the published
		// snapshot rather than keep a refused render's files.
		a.hasAccepted = false
		a.accepted = nil
		return
	}
	a.accepted = maps.Clone(a.confirmed)
}

func (a *currentFilesAuthority) publishedSnapshot() (map[string]string, error) {
	if a.published == nil {
		return map[string]string{}, nil
	}
	return a.published.get()
}
