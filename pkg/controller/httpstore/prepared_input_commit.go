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

package httpstore

import (
	"context"
	"errors"
	"slices"
	"sync"

	purehttpstore "gitlab.com/haproxy-haptic/haptic/pkg/httpstore"
)

// PrepareObservationCommitWithActiveLeases prepares verification and one
// persistent render cache lease transition without a fetch transaction.
func (c *Component) PrepareObservationCommitWithActiveLeases(
	ctx context.Context,
	observations []purehttpstore.ObservationToken,
	active *purehttpstore.ActiveLeaseCommit,
) (*PreparedInputCommit, error) {
	prepared, err := c.prepareStagedSourcesAndVerifyObservations(
		ctx, nil, nil, nil, observations, active, true, nil, nil,
	)
	if err != nil {
		return nil, err
	}
	activeToken, transition, hasActive := prepared.PlannedActiveLeases()
	plan, err := prepared.bindInputPlan(nil, nil, false)
	if err != nil {
		prepared.Abort()
		return nil, err
	}
	return &PreparedInputCommit{
		component:  prepared,
		plan:       plan,
		active:     activeToken,
		transition: transition,
		hasActive:  hasActive,
	}, nil
}

type preparedInputState uint8

const (
	preparedInputReady preparedInputState = iota
	preparedInputSealed
	preparedInputPublished
	preparedInputCommitted
	preparedInputReleased
)

// PreparedInputCommit retains HTTP input authority across coordinated publication.
type PreparedInputCommit struct {
	mu                 sync.Mutex
	transaction        *InputTransaction
	component          *preparedCandidateCommit
	plan               *preparedInputPublicationPlan
	committedSnapshots []purehttpstore.ContentSnapshot
	committedReplay    *purehttpstore.AcceptedReplayState
	cacheable          bool
	active             purehttpstore.ActiveLeaseToken
	transition         purehttpstore.ActiveLeaseTransition
	hasActive          bool
	transactionPlan    *preparedInputTransactionPlan
	state              preparedInputState
}

type preparedInputTransactionPlan struct {
	owner     *PreparedInputCommit
	snapshots []purehttpstore.ContentSnapshot
	replay    *purehttpstore.AcceptedReplayState
	cacheable bool
	auth      struct {
		owner     *PreparedInputCommit
		snapshots []purehttpstore.ContentSnapshot
		replay    *purehttpstore.AcceptedReplayState
		cacheable bool
	}
	seal *preparedInputTransactionPlan
}

func newPreparedInputTransactionPlan(c *PreparedInputCommit) *preparedInputTransactionPlan {
	if c.transaction == nil {
		return nil
	}
	plan := &preparedInputTransactionPlan{
		owner: c, snapshots: slices.Clone(c.plan.snapshots), replay: c.plan.replay,
		cacheable: c.plan.cacheable,
	}
	plan.auth.owner = c
	plan.auth.snapshots = slices.Clone(plan.snapshots)
	plan.auth.replay = plan.replay
	plan.auth.cacheable = plan.cacheable
	plan.seal = plan
	return plan
}

func (p *preparedInputTransactionPlan) validate(c *PreparedInputCommit) error {
	if c.transaction == nil {
		return p.validateTransactionLocked(c)
	}
	t := c.transaction
	t.mu.Lock()
	defer t.mu.Unlock()
	return p.validateTransactionLocked(c)
}

func (p *preparedInputTransactionPlan) validateTransactionLocked(c *PreparedInputCommit) error {
	if c.transaction == nil {
		if p != nil {
			return errors.New("prepared HTTP transaction plan has no transaction")
		}
		return nil
	}
	if p == nil || p.seal != p || p.owner != c || p.owner != p.auth.owner ||
		p.replay != p.auth.replay || p.cacheable != p.auth.cacheable ||
		!slices.Equal(p.snapshots, p.auth.snapshots) || p.replay != c.plan.replay ||
		p.cacheable != c.plan.cacheable || !slices.Equal(p.snapshots, c.plan.snapshots) {
		return errors.New("prepared HTTP transaction plan failed authentication")
	}
	t := c.transaction
	if t.state != transactionPrepared || t.prepared != c {
		return errors.New("prepared HTTP transaction plan lost its authority")
	}
	return nil
}

// ValidatePublication verifies that the retained commit can enter its terminal publication.
func (c *PreparedInputCommit) ValidatePublication() error {
	if c == nil {
		return errors.New("prepared HTTP input publication is missing")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.validatePublicationLocked()
}

func (c *PreparedInputCommit) validatePublicationLocked() error {
	if c.state != preparedInputReady && c.state != preparedInputSealed {
		return errors.New("prepared HTTP input publication is not ready")
	}
	if c.component != nil {
		if err := c.component.validateInputPlan(
			c.plan, c.committedReplay, c.committedSnapshots, c.cacheable,
		); err != nil {
			return err
		}
		if err := c.component.validatePublication(); err != nil {
			return err
		}
	} else if c.plan != nil {
		if err := c.plan.validate(nil); err != nil {
			return err
		}
		if c.plan.replay != c.committedReplay || c.plan.cacheable != c.cacheable ||
			!slices.Equal(c.plan.snapshots, c.committedSnapshots) {
			return errors.New("prepared HTTP input publication does not match its plan")
		}
	} else if c.transaction != nil || c.committedReplay != nil || len(c.committedSnapshots) != 0 || c.cacheable {
		return errors.New("prepared HTTP input publication has no authenticated plan")
	}
	if c.state == preparedInputSealed {
		return c.transactionPlan.validate(c)
	}
	if c.transaction == nil {
		return nil
	}
	c.transaction.mu.Lock()
	defer c.transaction.mu.Unlock()
	if c.transaction.state != transactionPrepared || c.transaction.prepared != c {
		return errors.New("prepared HTTP input publication lost its transaction authority")
	}
	return nil
}

// SealPublication retains an authenticated terminal publication with no remaining fallible work.
func (c *PreparedInputCommit) SealPublication() error {
	if c == nil {
		return errors.New("prepared HTTP input publication is missing")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.sealPublicationLocked()
}

func (c *PreparedInputCommit) sealPublicationLocked() error {
	if c.state == preparedInputSealed {
		return nil
	}
	if c.state != preparedInputReady {
		if c.transaction != nil {
			c.transaction.mu.Lock()
			aborted := c.transaction.state == transactionAborted
			c.transaction.mu.Unlock()
			if aborted {
				return errInputTransactionAborted
			}
		}
		return errors.New("prepared HTTP input publication is not ready")
	}
	if err := c.validatePublicationLocked(); err != nil {
		return err
	}
	if c.component != nil {
		if err := c.component.sealPublication(); err != nil {
			return err
		}
	}
	c.transactionPlan = newPreparedInputTransactionPlan(c)
	if err := c.transactionPlan.validate(c); err != nil {
		return err
	}
	c.state = preparedInputSealed
	return nil
}

// PlannedActiveLeases returns the lease token published with this commit.
func (c *PreparedInputCommit) PlannedActiveLeases() (
	purehttpstore.ActiveLeaseToken,
	purehttpstore.ActiveLeaseTransition,
	bool,
) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.state == preparedInputReleased || !c.hasActive {
		return purehttpstore.ActiveLeaseToken{}, purehttpstore.ActiveLeaseTransition{}, false
	}
	return c.active, purehttpstore.ActiveLeaseTransition{
		Activated: append([]string(nil), c.transition.Activated...),
		Retired:   append([]string(nil), c.transition.Retired...),
	}, true
}

// Publish reports whether publication won over Abort.
func (c *PreparedInputCommit) Publish() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	switch c.state {
	case preparedInputPublished:
		return true
	case preparedInputReleased:
		return false
	}
	if c.state == preparedInputReady {
		if err := c.sealPublicationLocked(); err != nil {
			return false
		}
	}
	if c.state != preparedInputSealed {
		return false
	}
	if err := c.validatePublicationLocked(); err != nil {
		return false
	}
	c.publishSealedLocked()
	return true
}

// PublishSealed completes a publication returned successfully from SealPublication.
func (c *PreparedInputCommit) PublishSealed() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.state == preparedInputPublished {
		return
	}
	if c.state != preparedInputSealed {
		panic("prepared HTTP input publication is not sealed")
	}
	if err := c.validatePublicationLocked(); err != nil {
		panic("prepared HTTP input publication failed authentication: " + err.Error())
	}
	c.publishSealedLocked()
}

func (c *PreparedInputCommit) publishSealedLocked() {
	if c.component != nil {
		c.component.publishSealed()
	}
	c.state = preparedInputPublished
}

// ValidatePublishedPublication authenticates tentative live state without releasing authority.
func (c *PreparedInputCommit) ValidatePublishedPublication() error {
	if c == nil {
		return errors.New("prepared HTTP input publication is missing")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.validatePublishedPublicationLocked()
}

func (c *PreparedInputCommit) validatePublishedPublicationLocked() error {
	if c.state != preparedInputPublished {
		return errors.New("prepared HTTP input publication is not published")
	}
	if c.component != nil {
		if err := c.component.validateInputPlan(
			c.plan, c.committedReplay, c.committedSnapshots, c.cacheable,
		); err != nil {
			return err
		}
		if err := c.component.validatePublishedPublication(); err != nil {
			return err
		}
	} else if c.plan != nil {
		if err := c.plan.validate(nil); err != nil {
			return err
		}
		if c.plan.replay != c.committedReplay || c.plan.cacheable != c.cacheable ||
			!slices.Equal(c.plan.snapshots, c.committedSnapshots) {
			return errors.New("published HTTP input publication does not match its plan")
		}
	} else if c.transaction != nil || c.committedReplay != nil || len(c.committedSnapshots) != 0 || c.cacheable {
		return errors.New("published HTTP input publication has no authenticated plan")
	}
	return c.transactionPlan.validate(c)
}

// CommitPublishedPublication records a rollback-capable publication decision.
func (c *PreparedInputCommit) CommitPublishedPublication() error {
	if c == nil {
		return errors.New("prepared HTTP input publication is missing")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.commitPublishedPublicationLocked()
}

func (c *PreparedInputCommit) commitPublishedPublicationLocked() error {
	if c.state == preparedInputCommitted {
		return nil
	}
	if err := c.validatePublishedPublicationLocked(); err != nil {
		return err
	}
	if c.component != nil {
		if err := c.component.commitPublishedPublication(); err != nil {
			return err
		}
	}
	c.state = preparedInputCommitted
	return nil
}

// ReleaseCommittedPublication exposes committed state and releases retained authority.
func (c *PreparedInputCommit) ReleaseCommittedPublication() {
	if c == nil {
		panic("prepared HTTP input publication is missing")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.state == preparedInputReleased {
		return
	}
	if c.state != preparedInputCommitted {
		panic("prepared HTTP input publication is not committed")
	}
	c.releaseCommittedPublicationLocked()
}

func (c *PreparedInputCommit) releaseCommittedPublicationLocked() {
	t := c.transaction
	if t != nil {
		t.mu.Lock()
	}
	if c.component != nil {
		c.component.releaseCommittedPublication()
	}
	if t != nil {
		c.commitTransactionLocked()
		if t.prepared == c {
			t.prepared = nil
		}
		t.mu.Unlock()
	}
	c.state = preparedInputReleased
}

// Release exposes the coordinated publication and releases retained authority.
func (c *PreparedInputCommit) Release() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.state == preparedInputReleased {
		return
	}
	if c.state == preparedInputPublished {
		if err := c.commitPublishedPublicationLocked(); err != nil {
			finishErr := c.abortPublicationLocked()
			panic("prepared HTTP input publication failed final authentication: " +
				errors.Join(err, finishErr).Error())
		}
	}
	if c.state == preparedInputCommitted {
		c.releaseCommittedPublicationLocked()
		return
	}
	if err := c.abortPublicationLocked(); err != nil {
		panic("prepared HTTP input publication failed rollback authentication: " + err.Error())
	}
}

func (c *PreparedInputCommit) commitTransactionLocked() {
	t := c.transaction
	if t == nil {
		return
	}
	if t.state == transactionPrepared && t.prepared == c {
		t.cacheable = c.transactionPlan.cacheable
		t.committedSnapshots = c.transactionPlan.snapshots
		t.committedReplay = c.transactionPlan.replay
		t.state = transactionCommitted
		t.sources = nil
		t.results = nil
		t.candidates = nil
		t.retrySeed = nil
	}
}

// Abort rolls back a tentative publication and releases retained authority.
func (c *PreparedInputCommit) Abort() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.state == preparedInputReleased {
		return
	}
	finishErr := c.abortPublicationLocked()
	if finishErr != nil {
		panic("prepared HTTP input publication failed rollback authentication: " + finishErr.Error())
	}
}

func (c *PreparedInputCommit) abortPublicationLocked() error {
	var finishErr error
	t := c.transaction
	if t != nil {
		t.mu.Lock()
		c.abortTransactionLocked()
	}
	if c.component != nil {
		finishErr = c.component.finish(false)
	}
	if t != nil {
		if t.prepared == c {
			t.prepared = nil
		}
		t.mu.Unlock()
	}
	c.state = preparedInputReleased
	return finishErr
}

func (c *PreparedInputCommit) abortTransactionLocked() {
	t := c.transaction
	if t == nil {
		return
	}
	if t.prepared == c && (t.state == transactionPrepared || t.state == transactionCommitted) {
		t.state = transactionAborted
		t.cacheable = false
		t.sources = nil
		t.results = nil
		t.candidates = nil
		t.retrySeed = nil
		t.committedSnapshots = nil
		t.committedReplay = nil
		t.replayEpoch = nil
		t.replayState = nil
		t.prepared = nil
	}
}
