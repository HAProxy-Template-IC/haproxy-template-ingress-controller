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

// Package rendercycle binds one rendered output to every effect produced by
// the same controller reconciliation under an authenticated immutable root.
package rendercycle

import (
	"errors"
	"strings"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/renderoutput"
	"gitlab.com/haproxy-haptic/haptic/pkg/templating"
)

var (
	errInvalidAuthority = errors.New("render cycle authority is invalid")
	errInvalidSnapshot  = errors.New("render cycle snapshot is invalid")
	errForeignSnapshot  = errors.New("render cycle snapshot has a foreign authority")
)

type authorityAuthentication struct {
	owner   *Authority
	outputs *renderoutput.Authority
}

// Authority owns one render-cycle lineage and its exact output lineage.
type Authority struct {
	outputs *renderoutput.Authority
	seal    *Authority
	auth    authorityAuthentication
}

// NewAuthority binds a render-output lineage to a new render-cycle lineage.
func NewAuthority(outputs *renderoutput.Authority) (*Authority, error) {
	if err := outputs.ValidateAuthentication(); err != nil {
		return nil, errors.Join(errInvalidAuthority, err)
	}
	authority := &Authority{outputs: outputs}
	authority.seal = authority
	authority.auth = authorityAuthentication{owner: authority, outputs: outputs}
	return authority, nil
}

// ValidateAuthentication verifies the authority's exact lineage binding.
func (a *Authority) ValidateAuthentication() error {
	if a == nil || a.seal != a || a.auth.owner != a || a.outputs == nil ||
		a.auth.outputs != a.outputs {
		return errInvalidAuthority
	}
	if err := a.outputs.ValidateAuthentication(); err != nil {
		return errors.Join(errInvalidAuthority, err)
	}
	return nil
}

// ValidateSnapshot proves that snapshot belongs to this render-cycle lineage.
func (a *Authority) ValidateSnapshot(snapshot *Snapshot) error {
	if err := a.ValidateAuthentication(); err != nil {
		return err
	}
	if err := snapshot.ValidateAuthentication(); err != nil {
		return err
	}
	if snapshot.authority != a {
		return errForeignSnapshot
	}
	return nil
}

type rootAuthentication struct {
	owner             *root
	authority         *Authority
	output            *renderoutput.Snapshot
	statusPatches     *templating.StatusPatchSnapshot
	events            *templating.RenderedEventSnapshot
	renderedResources *templating.RenderedResourceSnapshot
	contentChecksum   string
}

type root struct {
	authority         *Authority
	output            *renderoutput.Snapshot
	statusPatches     *templating.StatusPatchSnapshot
	events            *templating.RenderedEventSnapshot
	renderedResources *templating.RenderedResourceSnapshot
	contentChecksum   string
	seal              *root
	auth              rootAuthentication
}

func sealRoot(
	authority *Authority,
	output *renderoutput.Snapshot,
	statusPatches *templating.StatusPatchSnapshot,
	events *templating.RenderedEventSnapshot,
	renderedResources *templating.RenderedResourceSnapshot,
	contentChecksum string,
) *root {
	cycleRoot := &root{
		authority: authority, output: output, statusPatches: statusPatches,
		events: events, renderedResources: renderedResources,
		contentChecksum: strings.Clone(contentChecksum),
	}
	cycleRoot.seal = cycleRoot
	cycleRoot.auth = rootAuthentication{
		owner: cycleRoot, authority: cycleRoot.authority, output: cycleRoot.output,
		statusPatches: cycleRoot.statusPatches, events: cycleRoot.events,
		renderedResources: cycleRoot.renderedResources,
		contentChecksum:   cycleRoot.contentChecksum,
	}
	return cycleRoot
}

func (r *root) validate(authority *Authority) error {
	if err := r.validateShallow(authority); err != nil {
		return err
	}
	return r.validateChildren(authority)
}

func (r *root) validateShallow(authority *Authority) error {
	if r == nil || r.seal != r || r.auth.owner != r || r.authority != authority ||
		r.auth.authority != r.authority || r.output == nil || r.auth.output != r.output ||
		r.contentChecksum == "" ||
		r.auth.contentChecksum != r.contentChecksum {
		return errInvalidSnapshot
	}
	if r.statusPatches == nil || r.auth.statusPatches != r.statusPatches ||
		r.events == nil || r.auth.events != r.events || r.renderedResources == nil ||
		r.auth.renderedResources != r.renderedResources {
		return errInvalidSnapshot
	}
	return nil
}

func (r *root) validateChildren(authority *Authority) error {
	if err := authority.outputs.ValidateSnapshot(r.output); err != nil {
		return errors.Join(errInvalidSnapshot, err)
	}
	if err := r.statusPatches.ValidateAuthentication(); err != nil {
		return errors.Join(errInvalidSnapshot, err)
	}
	if err := r.events.ValidateAuthentication(); err != nil {
		return errors.Join(errInvalidSnapshot, err)
	}
	if err := r.renderedResources.ValidateAuthentication(); err != nil {
		return errors.Join(errInvalidSnapshot, err)
	}
	checksum, err := r.output.ContentChecksum()
	if err != nil {
		return errors.Join(errInvalidSnapshot, err)
	}
	if checksum != r.contentChecksum {
		return errInvalidSnapshot
	}
	return nil
}

type snapshotAuthentication struct {
	owner     *Snapshot
	authority *Authority
	root      *root
}

// Snapshot is one authenticated immutable render output and effect set.
type Snapshot struct {
	authority *Authority
	root      *root
	seal      *Snapshot
	auth      snapshotAuthentication
}

func sealSnapshot(authority *Authority, cycleRoot *root) *Snapshot {
	snapshot := &Snapshot{authority: authority, root: cycleRoot}
	snapshot.seal = snapshot
	snapshot.auth = snapshotAuthentication{
		owner: snapshot, authority: snapshot.authority, root: snapshot.root,
	}
	return snapshot
}

// NewSnapshot validates and seals one complete render cycle. Previous is
// reused only when every supplied child has the same authenticated root.
func NewSnapshot(
	authority *Authority,
	output *renderoutput.Snapshot,
	statusPatches *templating.StatusPatchSnapshot,
	events *templating.RenderedEventSnapshot,
	renderedResources *templating.RenderedResourceSnapshot,
	previous *Snapshot,
) (*Snapshot, error) {
	if err := authority.ValidateAuthentication(); err != nil {
		return nil, err
	}
	if previous != nil {
		if err := authority.ValidateSnapshot(previous); err != nil {
			return nil, err
		}
	}
	if err := authority.outputs.ValidateSnapshot(output); err != nil {
		return nil, errors.Join(errInvalidSnapshot, err)
	}
	if err := statusPatches.ValidateAuthentication(); err != nil {
		return nil, errors.Join(errInvalidSnapshot, err)
	}
	if err := events.ValidateAuthentication(); err != nil {
		return nil, errors.Join(errInvalidSnapshot, err)
	}
	if err := renderedResources.ValidateAuthentication(); err != nil {
		return nil, errors.Join(errInvalidSnapshot, err)
	}
	checksum, err := output.ContentChecksum()
	if err != nil {
		return nil, errors.Join(errInvalidSnapshot, err)
	}
	if previous != nil {
		same, sameErr := previous.hasExactChildren(output, statusPatches, events, renderedResources)
		if sameErr != nil {
			return nil, sameErr
		}
		if same && previous.root.contentChecksum == checksum {
			return previous, nil
		}
	}
	return sealSnapshot(authority, sealRoot(
		authority, output, statusPatches, events, renderedResources, checksum,
	)), nil
}

func (s *Snapshot) hasExactChildren(
	output *renderoutput.Snapshot,
	statusPatches *templating.StatusPatchSnapshot,
	events *templating.RenderedEventSnapshot,
	renderedResources *templating.RenderedResourceSnapshot,
) (bool, error) {
	same, err := s.root.output.SameRoot(output)
	if err != nil || !same {
		return same, err
	}
	same, err = s.root.statusPatches.SameRoot(statusPatches)
	if err != nil || !same {
		return same, err
	}
	same, err = s.root.events.SameRoot(events)
	if err != nil || !same {
		return same, err
	}
	same, err = s.root.renderedResources.SameRoot(renderedResources)
	if err != nil || !same {
		return same, err
	}
	return true, nil
}

// ValidateAuthentication verifies the complete composite root in constant time.
func (s *Snapshot) ValidateAuthentication() error {
	if s == nil || s.seal != s || s.auth.owner != s || s.authority == nil ||
		s.auth.authority != s.authority || s.root == nil || s.auth.root != s.root {
		return errInvalidSnapshot
	}
	if err := s.authority.ValidateAuthentication(); err != nil {
		return errors.Join(errInvalidSnapshot, err)
	}
	return s.root.validate(s.authority)
}

// SameRoot reports exact authenticated render-cycle root identity.
func (s *Snapshot) SameRoot(other *Snapshot) (bool, error) {
	if err := s.ValidateAuthentication(); err != nil {
		return false, err
	}
	if err := other.ValidateAuthentication(); err != nil {
		return false, err
	}
	return s.authority == other.authority && s.root == other.root, nil
}

// ExactEqual compares every output byte, declaration, and effect. It is an
// explicit full fallback; checksums only reject unequal outputs.
func (s *Snapshot) ExactEqual(other *Snapshot) (bool, error) {
	same, err := s.SameRoot(other)
	if err != nil || same {
		return same, err
	}
	if s.root.contentChecksum != other.root.contentChecksum {
		return false, nil
	}
	equal, err := s.root.output.ExactEqual(other.root.output)
	if err != nil || !equal {
		return equal, err
	}
	equal, err = s.root.statusPatches.ExactEqual(other.root.statusPatches)
	if err != nil || !equal {
		return equal, err
	}
	equal, err = s.root.events.ExactEqual(other.root.events)
	if err != nil || !equal {
		return equal, err
	}
	return s.root.renderedResources.ExactEqual(other.root.renderedResources)
}

// OutputSnapshot returns the exact authenticated rendered-output root.
func (s *Snapshot) OutputSnapshot() (*renderoutput.Snapshot, error) {
	if err := s.ValidateAuthentication(); err != nil {
		return nil, err
	}
	return s.root.output, nil
}

// StatusPatchSnapshot returns the exact authenticated status-patch root.
func (s *Snapshot) StatusPatchSnapshot() (*templating.StatusPatchSnapshot, error) {
	if err := s.ValidateAuthentication(); err != nil {
		return nil, err
	}
	return s.root.statusPatches, nil
}

// RenderedEventSnapshot returns the exact authenticated rendered-event root.
func (s *Snapshot) RenderedEventSnapshot() (*templating.RenderedEventSnapshot, error) {
	if err := s.ValidateAuthentication(); err != nil {
		return nil, err
	}
	return s.root.events, nil
}

// RenderedResourceSnapshot returns the exact authenticated desired-resource root.
func (s *Snapshot) RenderedResourceSnapshot() (*templating.RenderedResourceSnapshot, error) {
	if err := s.ValidateAuthentication(); err != nil {
		return nil, err
	}
	return s.root.renderedResources, nil
}

// ContentChecksum returns the checksum authenticated by the bound output root.
func (s *Snapshot) ContentChecksum() (string, error) {
	if err := s.ValidateAuthentication(); err != nil {
		return "", err
	}
	return s.root.contentChecksum, nil
}
