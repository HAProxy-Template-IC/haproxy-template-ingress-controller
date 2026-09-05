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
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"sync"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/rendercontext"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/renderartifact"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/renderoutput"
)

// currentFilesAuthority owns the accepted auxiliary output for one leader term.
type currentFilesAuthority struct {
	mu sync.RWMutex

	published        *publishedAuxFiles
	emptyRoot        *currentAuxFilesMapRoot
	generation       uint64
	active           bool
	hasAccepted      bool
	accepted         map[string]string
	acceptedRoot     *currentAuxFilesMapRoot
	acceptedSnapshot *renderartifact.Snapshot
	acceptedOutput   *renderoutput.Snapshot

	// confirmed is the last baseline HAProxy accepted, and pendingPlanID names
	// the render whose files are provisional. A refusal rolls `accepted` back
	// to `confirmed`, so the term's idea of "what is deployed" never keeps a
	// render the fleet was reverted away from.
	hasConfirmed      bool
	confirmed         map[string]string
	confirmedRoot     *currentAuxFilesMapRoot
	confirmedSnapshot *renderartifact.Snapshot
	confirmedOutput   *renderoutput.Snapshot
	pendingPlanID     string
}

type currentAuxFilesMapRoot struct {
	files     map[string]string
	canonical string
	seal      *currentAuxFilesMapRoot
}

type currentAuxFilesSource struct {
	authority  *currentFilesAuthority
	generation uint64
	root       *currentAuxFilesMapRoot
	seal       *currentAuxFilesSource
}

func newCurrentAuxFilesMapRoot(files map[string]string) (*currentAuxFilesMapRoot, error) {
	owned := maps.Clone(files)
	if owned == nil {
		owned = map[string]string{}
	}
	canonical, err := json.Marshal(owned)
	if err != nil {
		return nil, fmt.Errorf("encoding currentFiles root: %w", err)
	}
	root := &currentAuxFilesMapRoot{files: owned, canonical: string(canonical)}
	root.seal = root
	return root, nil
}

func retainCurrentAuxFilesMapRoot(
	previous *currentAuxFilesMapRoot,
	files map[string]string,
) (*currentAuxFilesMapRoot, error) {
	if previous != nil && previous.seal == previous && maps.Equal(previous.files, files) {
		return previous, nil
	}
	return newCurrentAuxFilesMapRoot(files)
}

func (a *currentFilesAuthority) ExactSource(
	generation uint64,
) (rendercontext.CurrentAuxFilesSource, error) {
	a.mu.RLock()
	if a.active && a.generation == generation && a.hasAccepted {
		source := &currentAuxFilesSource{
			authority: a, generation: generation, root: a.acceptedRoot,
		}
		a.mu.RUnlock()
		if source.root == nil {
			return nil, errors.New("accepted currentFiles has no exact root")
		}
		if err := a.publishedAvailabilityError(); err != nil {
			return nil, err
		}
		source.seal = source
		err := source.ValidateAuthentication()
		return source, err
	}
	a.mu.RUnlock()
	if a.published == nil {
		source := &currentAuxFilesSource{authority: a, generation: generation, root: a.emptyRoot}
		source.seal = source
		return source, nil
	}
	a.published.mu.RLock()
	defer a.published.mu.RUnlock()
	if a.published.unavailable != nil {
		return nil, a.published.unavailable
	}
	if a.published.currentRoot == nil || a.published.currentRoot.files == nil {
		return nil, errors.New("published currentFiles has no exact root")
	}
	source := &currentAuxFilesSource{
		authority: a, generation: generation, root: a.published.currentRoot,
	}
	source.seal = source
	return source, nil
}

func (s *currentAuxFilesSource) ValidateAuthentication() error {
	if s == nil || s.seal != s || s.authority == nil || s.generation == 0 {
		return errors.New("currentFiles source has invalid provenance")
	}
	if s.root == nil || s.root.seal != s.root || s.root.files == nil || s.root.canonical == "" {
		return errors.New("currentFiles source has an invalid exact root")
	}
	return nil
}

func (s *currentAuxFilesSource) SameRoot(
	other rendercontext.CurrentAuxFilesSource,
) (bool, error) {
	if err := s.ValidateAuthentication(); err != nil {
		return false, err
	}
	typed, ok := other.(*currentAuxFilesSource)
	if !ok {
		return false, nil
	}
	if err := typed.ValidateAuthentication(); err != nil {
		return false, err
	}
	if s.authority != typed.authority || s.generation != typed.generation {
		return false, nil
	}
	if s.root == typed.root {
		return true, nil
	}
	return s.root.canonical == typed.root.canonical, nil
}

func (s *currentAuxFilesSource) MaterializeCurrentAuxFiles() (map[string]string, error) {
	if err := s.ValidateAuthentication(); err != nil {
		return nil, err
	}
	return maps.Clone(s.root.files), nil
}

func newCurrentFilesAuthority(published *publishedAuxFiles) *currentFilesAuthority {
	empty, err := newCurrentAuxFilesMapRoot(nil)
	if err != nil {
		panic(err)
	}
	return &currentFilesAuthority{published: published, emptyRoot: empty}
}

func (a *currentFilesAuthority) BeginTerm() uint64 {
	a.mu.Lock()
	defer a.mu.Unlock()

	a.generation++
	a.active = true
	a.hasAccepted = false
	a.accepted = nil
	a.acceptedRoot = nil
	a.acceptedSnapshot = nil
	a.acceptedOutput = nil
	a.hasConfirmed = false
	a.confirmed = nil
	a.confirmedRoot = nil
	a.confirmedSnapshot = nil
	a.confirmedOutput = nil
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
	a.acceptedRoot = nil
	a.acceptedSnapshot = nil
	a.acceptedOutput = nil
	a.hasConfirmed = false
	a.confirmed = nil
	a.confirmedRoot = nil
	a.confirmedSnapshot = nil
	a.confirmedOutput = nil
	a.pendingPlanID = ""
	if a.published != nil {
		a.published.endLeaderTerm()
	}
}

func (a *currentFilesAuthority) Snapshot(generation uint64) (map[string]string, error) {
	a.mu.RLock()
	if a.active && a.generation == generation && a.hasAccepted {
		accepted := a.accepted
		acceptedSnapshot := a.acceptedSnapshot
		a.mu.RUnlock()
		var (
			files map[string]string
			err   error
		)
		if acceptedSnapshot != nil {
			files, err = dataplane.SnapshotCurrentFiles(acceptedSnapshot)
		} else {
			files = maps.Clone(accepted)
		}
		if err != nil {
			return nil, fmt.Errorf("projecting accepted auxiliary files: %w", err)
		}
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
	root, err := retainCurrentAuxFilesMapRoot(a.acceptedRoot, files)
	if err != nil {
		return
	}
	a.acceptedRoot = root
	a.accepted = root.files
	a.acceptedSnapshot = nil
	a.acceptedOutput = nil
	a.pendingPlanID = planID
}

func (a *currentFilesAuthority) AcceptSnapshot(
	generation uint64, planID string, snapshot *renderartifact.Snapshot,
) error {
	if snapshot == nil {
		return errors.New("accepting auxiliary snapshot: snapshot is nil")
	}
	if err := snapshot.ValidateAuthentication(); err != nil {
		return fmt.Errorf("accepting auxiliary snapshot: %w", err)
	}
	files, err := dataplane.SnapshotCurrentFiles(snapshot)
	if err != nil {
		return fmt.Errorf("accepting auxiliary snapshot: %w", err)
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	if !a.active || a.generation != generation {
		return fmt.Errorf("accepting auxiliary snapshot: leader term %d is not active", generation)
	}
	root, err := retainCurrentAuxFilesMapRoot(a.acceptedRoot, files)
	if err != nil {
		return fmt.Errorf("accepting auxiliary snapshot: %w", err)
	}
	a.hasAccepted = true
	a.accepted = root.files
	a.acceptedRoot = root
	a.acceptedSnapshot = snapshot
	a.acceptedOutput = nil
	a.pendingPlanID = planID
	return nil
}

func (a *currentFilesAuthority) AcceptOutput(
	generation uint64, output *renderoutput.Snapshot,
) error {
	if err := output.ValidateAuthentication(); err != nil {
		return fmt.Errorf("accepting render output: %w", err)
	}
	snapshot, err := output.ArtifactSnapshot()
	if err != nil {
		return fmt.Errorf("accepting render output: %w", err)
	}
	planID, err := output.PlanID()
	if err != nil {
		return fmt.Errorf("accepting render output: %w", err)
	}
	files, err := dataplane.SnapshotCurrentFiles(snapshot)
	if err != nil {
		return fmt.Errorf("accepting render output: %w", err)
	}

	a.mu.Lock()
	defer a.mu.Unlock()
	if !a.active || a.generation != generation {
		return fmt.Errorf("accepting render output: leader term %d is not active", generation)
	}
	root, err := retainCurrentAuxFilesMapRoot(a.acceptedRoot, files)
	if err != nil {
		return fmt.Errorf("accepting render output: %w", err)
	}
	a.hasAccepted = true
	a.accepted = root.files
	a.acceptedRoot = root
	a.acceptedSnapshot = snapshot
	a.acceptedOutput = output
	a.pendingPlanID = planID
	return nil
}

// Confirm settles the provisional baseline once the render gate passed the
// render it came from.
func (a *currentFilesAuthority) Confirm(generation uint64, planID string) {
	a.mu.Lock()
	defer a.mu.Unlock()

	if !a.active || a.generation != generation || a.acceptedOutput != nil ||
		planID == "" || planID != a.pendingPlanID {
		return
	}
	a.confirmed = a.accepted
	a.confirmedRoot = a.acceptedRoot
	a.confirmedSnapshot = a.acceptedSnapshot
	a.confirmedOutput = nil
	a.hasConfirmed = true
	a.pendingPlanID = ""
}

func (a *currentFilesAuthority) ConfirmOutput(
	generation uint64, output *renderoutput.Snapshot,
) error {
	if err := output.ValidateAuthentication(); err != nil {
		return fmt.Errorf("confirming render output: %w", err)
	}

	a.mu.Lock()
	defer a.mu.Unlock()
	if !a.active || a.generation != generation || a.acceptedOutput == nil {
		return nil
	}
	same, err := a.acceptedOutput.SameRoot(output)
	if err != nil {
		return fmt.Errorf("confirming render output: %w", err)
	}
	if !same {
		return nil
	}
	a.confirmed = a.accepted
	a.confirmedRoot = a.acceptedRoot
	a.confirmedSnapshot = a.acceptedSnapshot
	a.confirmedOutput = a.acceptedOutput
	a.hasConfirmed = true
	a.pendingPlanID = ""
	return nil
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

	if !a.active || a.generation != generation || a.acceptedOutput != nil ||
		planID == "" || planID != a.pendingPlanID {
		return
	}
	a.pendingPlanID = ""
	if !a.hasConfirmed {
		// Nothing was ever confirmed this term: fall back to the published
		// snapshot rather than keep a refused render's files.
		a.hasAccepted = false
		a.accepted = nil
		a.acceptedRoot = nil
		a.acceptedSnapshot = nil
		a.acceptedOutput = nil
		return
	}
	a.accepted = a.confirmed
	a.acceptedRoot = a.confirmedRoot
	a.acceptedSnapshot = a.confirmedSnapshot
	a.acceptedOutput = a.confirmedOutput
}

func (a *currentFilesAuthority) RollbackOutput(
	generation uint64, output *renderoutput.Snapshot,
) error {
	if err := output.ValidateAuthentication(); err != nil {
		return fmt.Errorf("rolling back render output: %w", err)
	}

	a.mu.Lock()
	defer a.mu.Unlock()
	if !a.active || a.generation != generation || a.acceptedOutput == nil {
		return nil
	}
	same, err := a.acceptedOutput.SameRoot(output)
	if err != nil {
		return fmt.Errorf("rolling back render output: %w", err)
	}
	if !same {
		return nil
	}
	a.pendingPlanID = ""
	if !a.hasConfirmed {
		a.hasAccepted = false
		a.accepted = nil
		a.acceptedRoot = nil
		a.acceptedSnapshot = nil
		a.acceptedOutput = nil
		return nil
	}
	a.accepted = a.confirmed
	a.acceptedRoot = a.confirmedRoot
	a.acceptedSnapshot = a.confirmedSnapshot
	a.acceptedOutput = a.confirmedOutput
	return nil
}

func (a *currentFilesAuthority) publishedSnapshot() (map[string]string, error) {
	if a.published == nil {
		return map[string]string{}, nil
	}
	return a.published.get()
}
