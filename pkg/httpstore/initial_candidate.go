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
	"fmt"
)

// InitialCandidate is content fetched for one unpopulated authoritative source.
type InitialCandidate struct {
	store             *HTTPStore
	source            *StagedSource
	entry             *CacheEntry
	url               string
	content           string
	contentChecksum   string
	etag              string
	lastModified      string
	sourceDescriptor  SourceDescriptor
	sourceGeneration  uint64
	mutationRevision  uint64
	candidateRevision Revision
	token             SnapshotToken
}

type stagedCandidateSnapshot struct {
	source           *StagedSource
	entry            *CacheEntry
	mutationRevision uint64
}

// PrepareStagedSnapshot fetches against render-local source authority.
func (s *HTTPStore) PrepareStagedSnapshot(
	ctx context.Context,
	source *StagedSource,
) (ContentSnapshot, *InitialCandidate, error) {
	prepared, accepted, err := s.stagedCandidateSnapshot(source)
	if err != nil {
		return ContentSnapshot{}, nil, err
	}
	if accepted.Found {
		return accepted, nil, nil
	}

	s.logger.Info("Performing initial HTTP fetch",
		"url", source.url,
		"timeout", source.spec.options.Timeout.String(),
		"retries", source.spec.options.Retries,
		"critical", source.spec.options.Critical)
	content, etag, lastModified, fetchErr := s.fetchWithRetry(
		ctx,
		source.url,
		source.spec.options,
		source.spec.auth,
		"",
		"",
	)

	s.mu.Lock()
	if err := s.validateStagedCandidateSnapshotLocked(&prepared); err != nil {
		s.mu.Unlock()
		return ContentSnapshot{}, nil, err
	}
	if fetchErr != nil {
		watermark := s.semanticRevision
		s.mu.Unlock()
		if source.spec.options.Critical {
			return ContentSnapshot{}, nil,
				fmt.Errorf("critical HTTP fetch failed for %s: %w", source.url, fetchErr)
		}
		s.logger.Warn("HTTP fetch failed, returning empty content", "url", source.url, "error", fetchErr)
		return ContentSnapshot{
			URL:         source.url,
			Descriptor:  source.spec.descriptor,
			StoreSource: s.revisionSource,
			Watermark:   watermark,
		}, nil, nil
	}
	if s.nextCandidateRevision == Revision(^uint64(0)) {
		s.mu.Unlock()
		panic("HTTP store candidate revision exhausted")
	}
	s.nextCandidateRevision++
	candidate := &InitialCandidate{
		store:             s,
		source:            source,
		entry:             prepared.entry,
		url:               source.url,
		content:           content,
		contentChecksum:   checksum(content),
		etag:              etag,
		lastModified:      lastModified,
		sourceDescriptor:  source.spec.descriptor,
		mutationRevision:  prepared.mutationRevision,
		candidateRevision: s.nextCandidateRevision,
	}
	if prepared.entry != nil {
		candidate.sourceGeneration = prepared.entry.sourceGeneration
	}
	candidate.token = s.candidateTokenLocked(candidate)
	result := ContentSnapshot{
		URL:         source.url,
		Descriptor:  source.spec.descriptor,
		Content:     content,
		Found:       true,
		Cacheable:   true,
		Token:       candidate.token,
		StoreSource: s.revisionSource,
		Watermark:   s.semanticRevision,
	}
	s.mu.Unlock()
	return result, candidate, nil
}

func (s *HTTPStore) stagedCandidateSnapshot(
	source *StagedSource,
) (stagedCandidateSnapshot, ContentSnapshot, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.publicationErrorLocked(); err != nil {
		return stagedCandidateSnapshot{}, ContentSnapshot{}, err
	}
	if err := s.validateStagedSourceLocked(source); err != nil {
		return stagedCandidateSnapshot{}, ContentSnapshot{}, err
	}
	prepared := stagedCandidateSnapshot{source: source}
	if source.Changed() {
		return prepared, ContentSnapshot{}, nil
	}
	entry := source.baseEntry
	if entry.AcceptedChecksum != "" {
		return prepared, s.acceptedSnapshotLocked(entry, s.semanticRevision), nil
	}
	if entry.HasPending {
		return stagedCandidateSnapshot{}, ContentSnapshot{},
			fmt.Errorf("HTTP source %s has content awaiting another validation; retry the render", source.url)
	}
	prepared.entry = entry
	prepared.mutationRevision = entry.mutationRevision
	return prepared, ContentSnapshot{}, nil
}

func (s *HTTPStore) validateStagedCandidateSnapshotLocked(snapshot *stagedCandidateSnapshot) error {
	if snapshot == nil || snapshot.source == nil {
		return errors.New("staged HTTP source is missing")
	}
	if err := s.validateStagedSourceLocked(snapshot.source); err != nil {
		return err
	}
	if snapshot.source.Changed() {
		return nil
	}
	entry := s.cache[snapshot.source.url]
	if entry != snapshot.entry || entry.mutationRevision != snapshot.mutationRevision ||
		entry.AcceptedChecksum != "" || entry.HasPending {
		return fmt.Errorf("HTTP source %s changed while it was being fetched; retry the render", snapshot.source.url)
	}
	return nil
}

// Content returns the candidate bytes used by its render.
func (c *InitialCandidate) Content() string {
	return c.content
}

// URL returns the candidate's source URL.
func (c *InitialCandidate) URL() string {
	return c.url
}

// SnapshotToken returns the render-local version of this candidate.
func (c *InitialCandidate) SnapshotToken() SnapshotToken {
	return c.token
}

type initialCandidateSnapshot struct {
	entry            *CacheEntry
	options          FetchOptions
	auth             *AuthConfig
	sourceDescriptor SourceDescriptor
	sourceGeneration uint64
	mutationRevision uint64
}

// PrepareInitial fetches candidate bytes without exposing them through the cache.
func (s *HTTPStore) PrepareInitial(
	ctx context.Context,
	url string,
	state SourceState,
) (string, *InitialCandidate, error) {
	snapshot, candidate, err := s.PrepareInitialSnapshot(ctx, url, state)
	return snapshot.Content, candidate, err
}

// PrepareInitialSnapshot fetches candidate bytes without exposing them through
// shared accepted state and returns their exact lifecycle version.
func (s *HTTPStore) PrepareInitialSnapshot(
	ctx context.Context,
	url string,
	state SourceState,
) (ContentSnapshot, *InitialCandidate, error) {
	snapshot, cached, err := s.initialCandidateSnapshot(url, state)
	if err != nil {
		return ContentSnapshot{}, nil, err
	}
	if cached {
		accepted := s.AcceptedSnapshot(url, state.Descriptor)
		if !accepted.Found {
			return ContentSnapshot{}, nil,
				fmt.Errorf("HTTP source %s changed before its cached content could be read; retry the render", url)
		}
		return accepted, nil, nil
	}

	s.logger.Info("Performing initial HTTP fetch",
		"url", url,
		"timeout", snapshot.options.Timeout.String(),
		"retries", snapshot.options.Retries,
		"critical", snapshot.options.Critical)

	content, etag, lastModified, fetchErr := s.fetchWithRetry(
		ctx,
		url,
		snapshot.options,
		snapshot.auth,
		"",
		"",
	)

	s.mu.Lock()
	if err := s.validateInitialSnapshotLocked(url, &snapshot); err != nil {
		s.mu.Unlock()
		return ContentSnapshot{}, nil, err
	}
	if fetchErr != nil {
		watermark := s.semanticRevision
		s.mu.Unlock()
		if snapshot.options.Critical {
			return ContentSnapshot{}, nil, fmt.Errorf("critical HTTP fetch failed for %s: %w", url, fetchErr)
		}
		s.logger.Warn("HTTP fetch failed, returning empty content", "url", url, "error", fetchErr)
		return ContentSnapshot{
			URL:         url,
			Descriptor:  snapshot.sourceDescriptor,
			Content:     "",
			Found:       false,
			Cacheable:   false,
			StoreSource: s.revisionSource,
			Watermark:   watermark,
		}, nil, nil
	}

	if s.nextCandidateRevision == Revision(^uint64(0)) {
		s.mu.Unlock()
		panic("HTTP store candidate revision exhausted")
	}
	s.nextCandidateRevision++
	candidate := &InitialCandidate{
		store:             s,
		entry:             snapshot.entry,
		url:               url,
		content:           content,
		contentChecksum:   checksum(content),
		etag:              etag,
		lastModified:      lastModified,
		sourceDescriptor:  snapshot.sourceDescriptor,
		sourceGeneration:  snapshot.sourceGeneration,
		mutationRevision:  snapshot.mutationRevision,
		candidateRevision: s.nextCandidateRevision,
	}
	candidate.token = s.candidateTokenLocked(candidate)
	result := ContentSnapshot{
		URL:         url,
		Descriptor:  snapshot.sourceDescriptor,
		Content:     content,
		Found:       true,
		Cacheable:   true,
		Token:       candidate.token,
		StoreSource: s.revisionSource,
		Watermark:   s.semanticRevision,
	}
	s.mu.Unlock()
	return result, candidate, nil
}

func (s *HTTPStore) initialCandidateSnapshot(
	url string,
	state SourceState,
) (snapshot initialCandidateSnapshot, cached bool, err error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.publicationErrorLocked(); err != nil {
		return initialCandidateSnapshot{}, false, err
	}

	entry, exists := s.cache[url]
	if !exists || entry.sourceDescriptor != state.Descriptor || entry.sourceGeneration != state.Generation {
		return initialCandidateSnapshot{}, false,
			fmt.Errorf("HTTP source %s changed before it could be fetched; retry the render", url)
	}
	if entry.AcceptedChecksum != "" {
		return initialCandidateSnapshot{}, true, nil
	}
	if entry.HasPending {
		return initialCandidateSnapshot{}, false,
			fmt.Errorf("HTTP source %s has content awaiting another validation; retry the render", url)
	}
	return initialCandidateSnapshot{
		entry:            entry,
		options:          entry.Options,
		auth:             entry.Auth,
		sourceDescriptor: entry.sourceDescriptor,
		sourceGeneration: entry.sourceGeneration,
		mutationRevision: entry.mutationRevision,
	}, false, nil
}

func (s *HTTPStore) validateInitialSnapshotLocked(url string, snapshot *initialCandidateSnapshot) error {
	entry, exists := s.cache[url]
	if !exists || entry != snapshot.entry || entry.sourceDescriptor != snapshot.sourceDescriptor ||
		entry.sourceGeneration != snapshot.sourceGeneration ||
		entry.mutationRevision != snapshot.mutationRevision || entry.AcceptedChecksum != "" || entry.HasPending {
		return fmt.Errorf("HTTP source %s changed while it was being fetched; retry the render", url)
	}
	return nil
}

// CommitInitialCandidates atomically accepts a complete validated candidate set.
func (s *HTTPStore) CommitInitialCandidates(ctx context.Context, candidates []*InitialCandidate) error {
	_, _, err := s.CommitInitialCandidatesAndVerify(ctx, candidates, nil)
	return err
}
