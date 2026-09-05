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

// Package renderoutput binds one rendered configuration to its exact plan and
// auxiliary artifacts under an authenticated immutable root.
package renderoutput

import (
	"crypto/sha256"
	"errors"
	"strings"
	"sync"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/renderartifact"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/renderplan"
	"gitlab.com/haproxy-haptic/haptic/pkg/rendercontent"
)

var (
	errInvalidAuthority = errors.New("render output authority is invalid")
	errInvalidSnapshot  = errors.New("render output snapshot is invalid")
	errForeignSnapshot  = errors.New("render output snapshot has a foreign authority")
)

type authorityAuthentication struct {
	owner     *Authority
	plans     *renderplan.Authority
	artifacts *renderartifact.Authority
}

// Authority owns one output lineage and its exact plan and artifact lineages.
type Authority struct {
	plans     *renderplan.Authority
	artifacts *renderartifact.Authority
	seal      *Authority
	auth      authorityAuthentication
}

// NewAuthority binds exact plan and artifact lineages to a new output lineage.
func NewAuthority(plans *renderplan.Authority, artifacts *renderartifact.Authority) (*Authority, error) {
	if err := plans.ValidateAuthentication(); err != nil {
		return nil, errors.Join(errInvalidAuthority, err)
	}
	if err := artifacts.ValidateAuthentication(); err != nil {
		return nil, errors.Join(errInvalidAuthority, err)
	}
	authority := &Authority{plans: plans, artifacts: artifacts}
	authority.seal = authority
	authority.auth = authorityAuthentication{
		owner: authority, plans: authority.plans, artifacts: authority.artifacts,
	}
	return authority, nil
}

// ValidateAuthentication verifies the authority's exact lineage bindings.
func (a *Authority) ValidateAuthentication() error {
	if a == nil || a.seal != a || a.auth.owner != a || a.plans == nil ||
		a.auth.plans != a.plans || a.artifacts == nil || a.auth.artifacts != a.artifacts {
		return errInvalidAuthority
	}
	if err := a.plans.ValidateAuthentication(); err != nil {
		return errors.Join(errInvalidAuthority, err)
	}
	if err := a.artifacts.ValidateAuthentication(); err != nil {
		return errors.Join(errInvalidAuthority, err)
	}
	return nil
}

// ValidateSnapshot proves that snapshot belongs to this output lineage.
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

// Counts records the exact cardinality of each output collection.
type Counts struct {
	Sections  int
	Backends  int
	Profiles  int
	Maps      int
	CRTLists  int
	Files     int
	Artifacts int
}

type immutableConfigAuthentication struct {
	owner          *immutableConfig
	document       rendercontent.Document
	memo           *immutableConfigMemo
	bytes          int
	digest         [sha256.Size]byte
	deferredDigest bool
	digestMemo     *immutableConfigDigestMemo
	sectionAligned bool
}

type immutableConfig struct {
	document       rendercontent.Document
	memo           *immutableConfigMemo
	bytes          int
	digest         [sha256.Size]byte
	deferredDigest bool
	digestMemo     *immutableConfigDigestMemo
	sectionAligned bool
	seal           *immutableConfig
	auth           immutableConfigAuthentication
}

type immutableConfigMemo struct {
	once  sync.Once
	value string
	err   error
}

type immutableConfigDigestMemo struct {
	once   sync.Once
	digest [sha256.Size]byte
	err    error
}

func sealConfig(
	document rendercontent.Document,
	measurement configDocumentMeasurement,
	materialized *string,
) *immutableConfig {
	return sealConfigState(
		document, measurement.bytes, measurement.digest, false, nil,
		measurement.sectionAligned, materialized,
	)
}

func sealDeferredConfig(
	document rendercontent.Document,
	bytes int,
	sectionAligned bool,
) *immutableConfig {
	return sealConfigState(
		document, bytes, [sha256.Size]byte{}, true, &immutableConfigDigestMemo{},
		sectionAligned, nil,
	)
}

func sealConfigState(
	document rendercontent.Document,
	bytes int,
	digest [sha256.Size]byte,
	deferredDigest bool,
	digestMemo *immutableConfigDigestMemo,
	sectionAligned bool,
	materialized *string,
) *immutableConfig {
	memo := &immutableConfigMemo{}
	if materialized != nil {
		owned := *materialized
		memo.once.Do(func() {
			memo.value = owned
		})
	}
	config := &immutableConfig{
		document: document, memo: memo, bytes: bytes, digest: digest,
		deferredDigest: deferredDigest, digestMemo: digestMemo,
		sectionAligned: sectionAligned,
	}
	config.seal = config
	config.auth = immutableConfigAuthentication{
		owner: config, document: config.document, memo: config.memo,
		bytes: config.bytes, digest: config.digest,
		deferredDigest: config.deferredDigest, digestMemo: config.digestMemo,
		sectionAligned: config.sectionAligned,
	}
	return config
}

func (c *immutableConfig) validate() error {
	if c == nil || c.seal != c || c.auth.owner != c || c.auth.document != c.document ||
		c.auth.memo != c.memo || c.memo == nil || c.auth.bytes != c.bytes ||
		c.auth.digest != c.digest || c.auth.deferredDigest != c.deferredDigest ||
		c.auth.digestMemo != c.digestMemo || c.auth.sectionAligned != c.sectionAligned ||
		c.bytes < 0 {
		return errInvalidSnapshot
	}
	if c.deferredDigest {
		if c.digest != ([sha256.Size]byte{}) || c.digestMemo == nil {
			return errInvalidSnapshot
		}
	} else if c.digestMemo != nil {
		return errInvalidSnapshot
	}
	return c.document.ValidateAuthentication()
}

func (c *immutableConfig) digestValue() ([sha256.Size]byte, error) {
	if err := c.validate(); err != nil {
		return [sha256.Size]byte{}, err
	}
	if !c.deferredDigest {
		return c.digest, nil
	}
	c.digestMemo.once.Do(func() {
		hasher := &boundedHashWriter{Hash: sha256.New()}
		written, err := c.document.WriteTo(hasher)
		if err != nil {
			c.digestMemo.err = err
			return
		}
		if written != int64(c.bytes) {
			c.digestMemo.err = errInvalidSnapshot
			return
		}
		copy(c.digestMemo.digest[:], hasher.Sum(nil))
	})
	return c.digestMemo.digest, c.digestMemo.err
}

func (c *immutableConfig) materialize() (string, error) {
	if err := c.validate(); err != nil {
		return "", err
	}
	c.memo.once.Do(func() {
		value, err := c.document.String()
		if err != nil {
			c.memo.err = err
			return
		}
		c.memo.value = value
	})
	if c.memo.err != nil {
		return "", c.memo.err
	}
	return c.memo.value, nil
}

type rootAuthentication struct {
	owner                 *root
	authority             *Authority
	config                *immutableConfig
	plan                  *renderplan.Snapshot
	artifacts             *renderartifact.Snapshot
	bindings              *outputBindingTree
	planID                string
	checksum              string
	deferredCompatibility bool
	compatibilityMemo     *rootCompatibilityMemo
	counts                Counts
}

type root struct {
	authority             *Authority
	config                *immutableConfig
	plan                  *renderplan.Snapshot
	artifacts             *renderartifact.Snapshot
	bindings              *outputBindingTree
	planID                string
	checksum              string
	deferredCompatibility bool
	compatibilityMemo     *rootCompatibilityMemo
	counts                Counts
	seal                  *root
	auth                  rootAuthentication
}

type rootCompatibilityMemo struct {
	planOnce     sync.Once
	planID       string
	planErr      error
	checksumOnce sync.Once
	checksum     string
	checksumErr  error
}

func sealRoot(
	authority *Authority,
	config *immutableConfig,
	plan *renderplan.Snapshot,
	artifacts *renderartifact.Snapshot,
	bindings *outputBindingTree,
	planID string,
	checksum string,
	counts Counts,
) *root {
	return sealRootState(
		authority, config, plan, artifacts, bindings, planID, checksum, false, nil, counts,
	)
}

func sealDeferredRoot(
	authority *Authority,
	config *immutableConfig,
	plan *renderplan.Snapshot,
	artifacts *renderartifact.Snapshot,
	bindings *outputBindingTree,
	counts Counts,
) *root {
	return sealRootState(
		authority, config, plan, artifacts, bindings, "", "", true, &rootCompatibilityMemo{}, counts,
	)
}

func sealRootState(
	authority *Authority,
	config *immutableConfig,
	plan *renderplan.Snapshot,
	artifacts *renderartifact.Snapshot,
	bindings *outputBindingTree,
	planID string,
	checksum string,
	deferredCompatibility bool,
	compatibilityMemo *rootCompatibilityMemo,
	counts Counts,
) *root {
	result := &root{
		authority: authority, config: config, plan: plan, artifacts: artifacts,
		bindings: bindings,
		planID:   strings.Clone(planID), checksum: strings.Clone(checksum),
		deferredCompatibility: deferredCompatibility,
		compatibilityMemo:     compatibilityMemo, counts: counts,
	}
	result.seal = result
	result.auth = rootAuthentication{
		owner: result, authority: result.authority, config: result.config,
		plan: result.plan, artifacts: result.artifacts, bindings: result.bindings,
		planID:   result.planID,
		checksum: result.checksum, deferredCompatibility: result.deferredCompatibility,
		compatibilityMemo: result.compatibilityMemo, counts: result.counts,
	}
	return result
}

func (r *root) validate(authority *Authority) error {
	if err := r.validateShallow(authority); err != nil {
		return err
	}
	if err := r.config.validate(); err != nil {
		return err
	}
	return r.validateChildren(authority)
}

func (r *root) validateShallow(authority *Authority) error {
	if r == nil || r.seal != r || r.authority != authority {
		return errInvalidSnapshot
	}
	if r.config == nil || r.plan == nil || r.artifacts == nil || r.bindings == nil ||
		!r.counts.valid() {
		return errInvalidSnapshot
	}
	expected := rootAuthentication{
		owner: r, authority: r.authority, config: r.config,
		plan: r.plan, artifacts: r.artifacts, bindings: r.bindings, planID: r.planID,
		checksum: r.checksum, deferredCompatibility: r.deferredCompatibility,
		compatibilityMemo: r.compatibilityMemo, counts: r.counts,
	}
	if r.auth != expected {
		return errInvalidSnapshot
	}
	if r.deferredCompatibility {
		if r.planID != "" || r.checksum != "" || r.compatibilityMemo == nil {
			return errInvalidSnapshot
		}
	} else if r.checksum == "" || r.compatibilityMemo != nil {
		return errInvalidSnapshot
	}
	return nil
}

func (r *root) validateChildren(authority *Authority) error {
	if err := authority.plans.ValidateSnapshot(r.plan); err != nil {
		return errors.Join(errInvalidSnapshot, err)
	}
	if err := authority.artifacts.ValidateSnapshot(r.artifacts); err != nil {
		return errors.Join(errInvalidSnapshot, err)
	}
	artifactCount, err := r.artifacts.Len()
	if err != nil {
		return errors.Join(errInvalidSnapshot, err)
	}
	if artifactCount != r.counts.Artifacts {
		return errInvalidSnapshot
	}
	if err := r.bindings.validate(); err != nil {
		return err
	}
	if r.bindings.files != r.counts.Files || r.bindings.artifacts != r.counts.Artifacts {
		return errInvalidSnapshot
	}
	planEntries, err := r.plan.Len()
	if err != nil {
		return errors.Join(errInvalidSnapshot, err)
	}
	if planEntries != r.counts.planEntries() {
		return errInvalidSnapshot
	}
	return nil
}

func (c Counts) valid() bool {
	return c.Sections >= 0 && c.Backends >= 0 && c.Profiles >= 0 && c.Maps >= 0 &&
		c.CRTLists >= 0 && c.Files >= 0 && c.Artifacts >= 0
}

func (c Counts) planEntries() int {
	return c.Sections + c.Backends + c.Profiles + c.Maps + c.CRTLists + c.Files
}

type snapshotAuthentication struct {
	owner     *Snapshot
	authority *Authority
	root      *root
}

// Snapshot is one authenticated immutable render output.
type Snapshot struct {
	authority *Authority
	root      *root
	seal      *Snapshot
	auth      snapshotAuthentication
}

func sealSnapshot(authority *Authority, outputRoot *root) *Snapshot {
	snapshot := &Snapshot{authority: authority, root: outputRoot}
	snapshot.seal = snapshot
	snapshot.auth = snapshotAuthentication{
		owner: snapshot, authority: snapshot.authority, root: snapshot.root,
	}
	return snapshot
}

// NewSnapshot validates and seals one complete render output.
func NewSnapshot(
	authority *Authority,
	config string,
	plan *renderplan.Plan,
	artifacts *renderartifact.Snapshot,
	previous *Snapshot,
) (*Snapshot, error) {
	owned := strings.Clone(config)
	document, err := configDocumentFromString(owned)
	if err != nil {
		return nil, err
	}
	return newSnapshot(authority, document, &owned, plan, artifacts, previous)
}

// NewSnapshotFromDocument validates and seals one authenticated document output.
func NewSnapshotFromDocument(
	authority *Authority,
	document rendercontent.Document,
	plan *renderplan.Plan,
	artifacts *renderartifact.Snapshot,
	previous *Snapshot,
) (*Snapshot, error) {
	return newSnapshot(authority, document, nil, plan, artifacts, previous)
}

func newSnapshot(
	authority *Authority,
	document rendercontent.Document,
	materialized *string,
	plan *renderplan.Plan,
	artifacts *renderartifact.Snapshot,
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
	if err := authority.artifacts.ValidateSnapshot(artifacts); err != nil {
		return nil, errors.Join(errInvalidSnapshot, err)
	}
	files, counts, err := validateExactPlan(plan)
	if err != nil {
		return nil, err
	}
	artifactCount, measurement, checksumItems, err := validateOutputDocumentBindings(
		document, plan.Sections, files, artifacts,
	)
	if err != nil {
		return nil, err
	}
	counts.Artifacts = artifactCount

	var previousPlan *renderplan.Snapshot
	if previous != nil {
		previousPlan = previous.root.plan
	}
	planSnapshot, err := renderplan.NewSnapshotWithConfigDocument(
		authority.plans, plan, document, previousPlan,
	)
	if err != nil {
		return nil, errors.Join(errInvalidSnapshot, err)
	}

	artifactSnapshot, err := canonicalArtifactSnapshot(artifacts, previous)
	if err != nil {
		return nil, err
	}

	configRoot, err := ownConfig(document, measurement, materialized, previous)
	if err != nil {
		return nil, err
	}
	checksum, err := computeDocumentSnapshotContentChecksum(measurement.contentHash, checksumItems)
	if err != nil {
		return nil, errors.Join(errInvalidSnapshot, err)
	}
	var bindings *outputBindingTree
	if previous != nil && configRoot == previous.root.config &&
		planSnapshot == previous.root.plan && artifactSnapshot == previous.root.artifacts {
		bindings = previous.root.bindings
	} else {
		bindings, err = buildOutputBindingTree(document, files, artifactSnapshot)
		if err != nil {
			return nil, errors.Join(errInvalidSnapshot, err)
		}
	}
	if exactPrevious(previous, configRoot, planSnapshot, artifactSnapshot, plan.ID, checksum, counts) {
		return previous, nil
	}
	return sealSnapshot(authority, sealRoot(
		authority, configRoot, planSnapshot, artifactSnapshot, bindings,
		plan.ID, checksum, counts,
	)), nil
}

func canonicalArtifactSnapshot(
	artifacts *renderartifact.Snapshot,
	previous *Snapshot,
) (*renderartifact.Snapshot, error) {
	if previous == nil {
		return artifacts, nil
	}
	same, err := artifacts.SameRoot(previous.root.artifacts)
	if err != nil {
		return nil, errors.Join(errInvalidSnapshot, err)
	}
	if same {
		return previous.root.artifacts, nil
	}
	exact, err := artifacts.ExactEqual(previous.root.artifacts)
	if err != nil {
		return nil, errors.Join(errInvalidSnapshot, err)
	}
	if exact {
		return previous.root.artifacts, nil
	}
	return artifacts, nil
}

func ownConfig(
	document rendercontent.Document,
	measurement configDocumentMeasurement,
	materialized *string,
	previous *Snapshot,
) (*immutableConfig, error) {
	if previous == nil {
		return sealConfig(document, measurement, materialized), nil
	}
	return ownConfigFromPrevious(document, measurement, materialized, previous)
}

func ownConfigFromPrevious(
	document rendercontent.Document,
	measurement configDocumentMeasurement,
	materialized *string,
	previous *Snapshot,
) (*immutableConfig, error) {
	same, err := document.SameRoot(previous.root.config.document)
	if err != nil {
		return nil, errors.Join(errInvalidSnapshot, err)
	}
	if same && previous.root.config.sectionAligned == measurement.sectionAligned {
		return previous.root.config, nil
	}
	previousDigest, err := previous.root.config.digestValue()
	if err != nil {
		return nil, err
	}
	if measurement.bytes != previous.root.config.bytes || measurement.digest != previousDigest ||
		measurement.sectionAligned != previous.root.config.sectionAligned {
		return sealConfig(document, measurement, materialized), nil
	}
	if materialized != nil {
		previousValue, err := previous.root.config.materialize()
		if err != nil {
			return nil, err
		}
		if *materialized == previousValue {
			return previous.root.config, nil
		}
		return sealConfig(document, measurement, materialized), nil
	}
	equal, err := exactDocumentEqual(document, previous.root.config.document)
	if err != nil {
		return nil, errors.Join(errInvalidSnapshot, err)
	}
	if equal {
		return previous.root.config, nil
	}
	return sealConfig(document, measurement, materialized), nil
}

func exactPrevious(
	previous *Snapshot,
	config *immutableConfig,
	plan *renderplan.Snapshot,
	artifacts *renderartifact.Snapshot,
	planID string,
	checksum string,
	counts Counts,
) bool {
	return previous != nil && previous.root.config == config && previous.root.plan == plan &&
		previous.root.artifacts == artifacts && previous.root.planID == planID &&
		previous.root.checksum == checksum && previous.root.counts == counts
}

// ReusePrevious returns previous only when both supplied child roots are the
// exact roots it already binds. A false result requires full NewSnapshot validation.
func ReusePrevious(
	authority *Authority,
	previous *Snapshot,
	plan *renderplan.Snapshot,
	artifacts *renderartifact.Snapshot,
) (*Snapshot, bool, error) {
	return reusePrevious(authority, previous, nil, plan, artifacts)
}

// ReusePreviousDocument returns previous only when the supplied document and
// both child snapshots are its exact authenticated roots.
func ReusePreviousDocument(
	authority *Authority,
	previous *Snapshot,
	document rendercontent.Document,
	plan *renderplan.Snapshot,
	artifacts *renderartifact.Snapshot,
) (*Snapshot, bool, error) {
	return reusePrevious(authority, previous, &document, plan, artifacts)
}

func reusePrevious(
	authority *Authority,
	previous *Snapshot,
	document *rendercontent.Document,
	plan *renderplan.Snapshot,
	artifacts *renderartifact.Snapshot,
) (*Snapshot, bool, error) {
	if err := authority.ValidateSnapshot(previous); err != nil {
		return nil, false, err
	}
	if err := authority.plans.ValidateSnapshot(plan); err != nil {
		return nil, false, errors.Join(errInvalidSnapshot, err)
	}
	if err := authority.artifacts.ValidateSnapshot(artifacts); err != nil {
		return nil, false, errors.Join(errInvalidSnapshot, err)
	}
	if document != nil {
		documentSame, err := document.SameRoot(previous.root.config.document)
		if err != nil {
			return nil, false, errors.Join(errInvalidSnapshot, err)
		}
		if !documentSame {
			return nil, false, nil
		}
	}
	planSame, err := plan.SameRoot(previous.root.plan)
	if err != nil {
		return nil, false, errors.Join(errInvalidSnapshot, err)
	}
	if !planSame {
		return nil, false, nil
	}
	artifactsSame, err := artifacts.SameRoot(previous.root.artifacts)
	if err != nil {
		return nil, false, errors.Join(errInvalidSnapshot, err)
	}
	if !artifactsSame {
		return nil, false, nil
	}
	return previous, true, nil
}

// ValidateAuthentication verifies the composite root in constant time.
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

// SameRoot reports exact authenticated output-root identity.
func (s *Snapshot) SameRoot(other *Snapshot) (bool, error) {
	if err := s.ValidateAuthentication(); err != nil {
		return false, err
	}
	if err := other.ValidateAuthentication(); err != nil {
		return false, err
	}
	return s.authority == other.authority && s.root == other.root, nil
}

// ExactEqual compares every final byte and declaration; hashes and IDs only reject.
func (s *Snapshot) ExactEqual(other *Snapshot) (bool, error) {
	same, err := s.SameRoot(other)
	if err != nil || same {
		return same, err
	}
	if s.root.counts != other.root.counts || s.root.config.bytes != other.root.config.bytes {
		return false, nil
	}
	leftPlanID, err := s.PlanID()
	if err != nil {
		return false, err
	}
	rightPlanID, err := other.PlanID()
	if err != nil || leftPlanID != rightPlanID {
		return false, err
	}
	leftChecksum, err := s.ContentChecksum()
	if err != nil {
		return false, err
	}
	rightChecksum, err := other.ContentChecksum()
	if err != nil || leftChecksum != rightChecksum {
		return false, err
	}
	leftDigest, err := s.root.config.digestValue()
	if err != nil {
		return false, err
	}
	rightDigest, err := other.root.config.digestValue()
	if err != nil || leftDigest != rightDigest {
		return false, err
	}
	configsEqual, err := exactDocumentEqual(
		s.root.config.document, other.root.config.document,
	)
	if err != nil || !configsEqual {
		return configsEqual, err
	}
	plansEqual, err := s.root.plan.ExactEqual(other.root.plan)
	if err != nil || !plansEqual {
		return plansEqual, err
	}
	return s.root.artifacts.ExactEqual(other.root.artifacts)
}

// Config returns the exact immutable haproxy.cfg bytes.
func (s *Snapshot) Config() (string, error) {
	if err := s.ValidateAuthentication(); err != nil {
		return "", err
	}
	return s.root.config.materialize()
}

// ConfigDocument returns the authenticated haproxy.cfg document root.
func (s *Snapshot) ConfigDocument() (rendercontent.Document, error) {
	if err := s.ValidateAuthentication(); err != nil {
		return rendercontent.Document{}, err
	}
	return s.root.config.document, nil
}

// PlanSnapshot returns the authenticated render-plan root.
func (s *Snapshot) PlanSnapshot() (*renderplan.Snapshot, error) {
	if err := s.ValidateAuthentication(); err != nil {
		return nil, err
	}
	return s.root.plan, nil
}

// ArtifactSnapshot returns the authenticated auxiliary-artifact root.
func (s *Snapshot) ArtifactSnapshot() (*renderartifact.Snapshot, error) {
	if err := s.ValidateAuthentication(); err != nil {
		return nil, err
	}
	return s.root.artifacts, nil
}

// PlanID returns the plan's verified identifier.
func (s *Snapshot) PlanID() (string, error) {
	if err := s.ValidateAuthentication(); err != nil {
		return "", err
	}
	if !s.root.deferredCompatibility {
		return s.root.planID, nil
	}
	s.root.compatibilityMemo.planOnce.Do(func() {
		s.root.compatibilityMemo.planID, s.root.compatibilityMemo.planErr = s.root.plan.ID()
	})
	return s.root.compatibilityMemo.planID, s.root.compatibilityMemo.planErr
}

// ContentChecksum returns the checksum authenticated by this output root.
func (s *Snapshot) ContentChecksum() (string, error) {
	if err := s.ValidateAuthentication(); err != nil {
		return "", err
	}
	if !s.root.deferredCompatibility {
		return s.root.checksum, nil
	}
	s.root.compatibilityMemo.checksumOnce.Do(func() {
		s.root.compatibilityMemo.checksum, s.root.compatibilityMemo.checksumErr =
			computeSnapshotContentChecksum(s.root.config.document, s.root.artifacts)
	})
	return s.root.compatibilityMemo.checksum, s.root.compatibilityMemo.checksumErr
}

// Counts returns the authenticated collection cardinalities.
func (s *Snapshot) Counts() (Counts, error) {
	if err := s.ValidateAuthentication(); err != nil {
		return Counts{}, err
	}
	return s.root.counts, nil
}
