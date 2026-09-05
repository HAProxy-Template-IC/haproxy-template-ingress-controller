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
	"errors"
	"fmt"
)

// StagedSource is one render-local authoritative source declaration.
type StagedSource struct {
	store           *HTTPStore
	url             string
	spec            sourceSpec
	baseEntry       *CacheEntry
	baseDescriptor  SourceDescriptor
	baseGeneration  uint64
	baseReplay      uint64
	baseHasAccepted bool
	baseExists      bool
	watermark       Revision
}

// URL returns the declared URL.
func (s *StagedSource) URL() string {
	if s == nil {
		return ""
	}
	return s.url
}

// Descriptor returns the exact effective fetch declaration.
func (s *StagedSource) Descriptor() SourceDescriptor {
	if s == nil {
		return SourceDescriptor{}
	}
	return s.spec.descriptor
}

// Changed reports whether publication replaces the current source authority.
func (s *StagedSource) Changed() bool {
	return s != nil && (!s.baseExists || s.baseDescriptor != s.spec.descriptor)
}

// State returns the render-local source state.
func (s *StagedSource) State() SourceState {
	if s == nil {
		return SourceState{}
	}
	generation := uint64(0)
	hasAccepted := false
	if !s.Changed() {
		generation = s.baseGeneration
		hasAccepted = s.baseHasAccepted
	}
	return SourceState{
		Identity:    s.spec.descriptor.Identity(),
		Descriptor:  s.spec.descriptor,
		Generation:  generation,
		Delay:       s.spec.options.Delay,
		HasAccepted: hasAccepted,
	}
}

// StageSource captures a declaration without changing shared authority.
func (s *HTTPStore) StageSource(
	url string,
	opts FetchOptions,
	auth *AuthConfig,
) (*StagedSource, error) {
	spec, err := normalizeSource(opts, auth)
	if err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.publicationErrorLocked(); err != nil {
		return nil, err
	}
	return s.stageSourceLocked(url, &spec), nil
}

func (s *HTTPStore) stageSourceLocked(url string, spec *sourceSpec) *StagedSource {
	entry, exists := s.cache[url]
	staged := &StagedSource{
		store:      s,
		url:        url,
		spec:       *spec,
		baseEntry:  entry,
		baseExists: exists,
		watermark:  s.semanticRevision,
	}
	if exists {
		staged.baseDescriptor = entry.sourceDescriptor
		staged.baseGeneration = entry.sourceGeneration
		staged.baseReplay = entry.replayRevision
		staged.baseHasAccepted = entry.AcceptedChecksum != ""
	}
	return staged
}

// StageAcceptedSnapshot pins accepted bytes without changing source or timer state.
func (s *HTTPStore) StageAcceptedSnapshot(
	token SnapshotToken,
) (ContentSnapshot, *StagedSource, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.publicationErrorLocked() != nil {
		return ContentSnapshot{}, nil, false
	}
	if !s.verifySnapshotLocked(&token) {
		return ContentSnapshot{}, nil, false
	}
	entry := s.cache[token.url]
	spec, err := normalizeSource(entry.Options, entry.Auth)
	if err != nil || spec.descriptor != token.descriptor {
		return ContentSnapshot{}, nil, false
	}
	staged := s.stageSourceLocked(token.url, &spec)
	return s.acceptedSnapshotLocked(entry, s.semanticRevision), staged, true
}

// StageCurrentAcceptedSnapshot pins the current accepted bytes and source authority.
func (s *HTTPStore) StageCurrentAcceptedSnapshot(
	url string,
	descriptor SourceDescriptor,
) (ContentSnapshot, *StagedSource, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.publicationErrorLocked() != nil {
		return ContentSnapshot{}, nil, false
	}
	entry, exists := s.cache[url]
	if !exists || entry.sourceDescriptor != descriptor || entry.AcceptedChecksum == "" ||
		entry.acceptedRevision == 0 {
		return ContentSnapshot{}, nil, false
	}
	spec, err := normalizeSource(entry.Options, entry.Auth)
	if err != nil || spec.descriptor != descriptor {
		return ContentSnapshot{}, nil, false
	}
	staged := s.stageSourceLocked(url, &spec)
	return s.acceptedSnapshotLocked(entry, s.semanticRevision), staged, true
}

func (s *HTTPStore) stagedSourceCurrent(source *StagedSource) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.publicationErrorLocked() != nil {
		return false
	}
	return s.stagedSourceCurrentLocked(source)
}

// VerifyStagedSource reports whether the captured source authority is current.
func (s *HTTPStore) VerifyStagedSource(source *StagedSource) bool {
	return s.stagedSourceCurrent(source)
}

func (s *HTTPStore) stagedSourceCurrentLocked(source *StagedSource) bool {
	if source == nil || source.store != s || source.watermark > s.semanticRevision {
		return false
	}
	entry, exists := s.cache[source.url]
	if source.baseExists {
		return exists && entry == source.baseEntry &&
			entry.sourceDescriptor == source.baseDescriptor &&
			entry.sourceGeneration == source.baseGeneration &&
			entry.replayRevision == source.baseReplay
	}
	if exists {
		return false
	}
	_, changes, complete := s.changesSinceLocked(source.watermark)
	if !complete {
		return false
	}
	for index := range changes {
		if changes[index].URL == source.url {
			return false
		}
	}
	return true
}

func (s *HTTPStore) validateStagedSourceLocked(source *StagedSource) error {
	if source == nil || source.store != s {
		return errors.New("staged HTTP source does not belong to this store")
	}
	if !s.stagedSourceCurrentLocked(source) {
		return fmt.Errorf("HTTP source %s changed while the render was running", source.url)
	}
	return nil
}

func (s *HTTPStore) planStagedSourcesLocked(
	sources []*StagedSource,
) ([]preparedSourcePlan, map[string]*StagedSource, error) {
	plans := make([]preparedSourcePlan, 0, len(sources))
	byURL := make(map[string]*StagedSource, len(sources))
	for _, source := range sources {
		if err := s.validateStagedSourceLocked(source); err != nil {
			return nil, nil, err
		}
		if _, exists := byURL[source.url]; exists {
			return nil, nil, fmt.Errorf("staged HTTP source for %s appears more than once", source.url)
		}
		byURL[source.url] = source
		plans = append(plans, preparedSourcePlan{source: source})
	}
	return plans, byURL, nil
}
