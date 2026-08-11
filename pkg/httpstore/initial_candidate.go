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
	"time"
)

// InitialCandidate is content fetched for one unpopulated authoritative source.
type InitialCandidate struct {
	store            *HTTPStore
	entry            *CacheEntry
	url              string
	content          string
	contentChecksum  string
	etag             string
	lastModified     string
	sourceIdentity   string
	sourceGeneration uint64
	mutationRevision uint64
}

// Content returns the candidate bytes used by its render.
func (c *InitialCandidate) Content() string {
	return c.content
}

// URL returns the candidate's source URL.
func (c *InitialCandidate) URL() string {
	return c.url
}

type initialCandidateSnapshot struct {
	entry            *CacheEntry
	options          FetchOptions
	auth             *AuthConfig
	sourceIdentity   string
	sourceGeneration uint64
	mutationRevision uint64
}

// PrepareInitial fetches candidate bytes without exposing them through the cache.
func (s *HTTPStore) PrepareInitial(
	ctx context.Context,
	url string,
	state SourceState,
) (string, *InitialCandidate, error) {
	snapshot, content, cached, err := s.initialCandidateSnapshot(url, state)
	if err != nil || cached {
		return content, nil, err
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
	if err := s.validateInitialSnapshot(url, &snapshot); err != nil {
		return "", nil, err
	}
	if fetchErr != nil {
		if snapshot.options.Critical {
			return "", nil, fmt.Errorf("critical HTTP fetch failed for %s: %w", url, fetchErr)
		}
		s.logger.Warn("HTTP fetch failed, returning empty content", "url", url, "error", fetchErr)
		return "", nil, nil
	}

	candidate := &InitialCandidate{
		store:            s,
		entry:            snapshot.entry,
		url:              url,
		content:          content,
		contentChecksum:  checksum(content),
		etag:             etag,
		lastModified:     lastModified,
		sourceIdentity:   snapshot.sourceIdentity,
		sourceGeneration: snapshot.sourceGeneration,
		mutationRevision: snapshot.mutationRevision,
	}
	return content, candidate, nil
}

func (s *HTTPStore) initialCandidateSnapshot(
	url string,
	state SourceState,
) (snapshot initialCandidateSnapshot, content string, cached bool, err error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	entry, exists := s.cache[url]
	if !exists || entry.sourceIdentity != state.Identity || entry.sourceGeneration != state.Generation {
		return initialCandidateSnapshot{}, "", false,
			fmt.Errorf("HTTP source %s changed before it could be fetched; retry the render", url)
	}
	if entry.AcceptedChecksum != "" {
		return initialCandidateSnapshot{}, entry.AcceptedContent, true, nil
	}
	if entry.HasPending {
		return initialCandidateSnapshot{}, "", false,
			fmt.Errorf("HTTP source %s has content awaiting another validation; retry the render", url)
	}
	return initialCandidateSnapshot{
		entry:            entry,
		options:          entry.Options,
		auth:             entry.Auth,
		sourceIdentity:   entry.sourceIdentity,
		sourceGeneration: entry.sourceGeneration,
		mutationRevision: entry.mutationRevision,
	}, "", false, nil
}

func (s *HTTPStore) validateInitialSnapshot(url string, snapshot *initialCandidateSnapshot) error {
	s.mu.RLock()
	defer s.mu.RUnlock()

	entry, exists := s.cache[url]
	if !exists || entry != snapshot.entry || entry.sourceIdentity != snapshot.sourceIdentity ||
		entry.sourceGeneration != snapshot.sourceGeneration ||
		entry.mutationRevision != snapshot.mutationRevision || entry.AcceptedChecksum != "" || entry.HasPending {
		return fmt.Errorf("HTTP source %s changed while it was being fetched; retry the render", url)
	}
	return nil
}

// CommitInitialCandidates atomically accepts a complete validated candidate set.
func (s *HTTPStore) CommitInitialCandidates(ctx context.Context, candidates []*InitialCandidate) error {
	if len(candidates) == 0 {
		return nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if cause := context.Cause(ctx); cause != nil {
		return fmt.Errorf("committing initial HTTP candidates: %w", cause)
	}

	seen := make(map[string]struct{}, len(candidates))
	for _, candidate := range candidates {
		if candidate == nil || candidate.store != s {
			return errors.New("initial HTTP candidate does not belong to this store")
		}
		if _, exists := seen[candidate.url]; exists {
			return fmt.Errorf("initial HTTP candidate for %s appears more than once", candidate.url)
		}
		seen[candidate.url] = struct{}{}
		entry, exists := s.cache[candidate.url]
		if !exists || entry != candidate.entry || entry.sourceIdentity != candidate.sourceIdentity ||
			entry.sourceGeneration != candidate.sourceGeneration ||
			entry.mutationRevision != candidate.mutationRevision || entry.AcceptedChecksum != "" || entry.HasPending {
			return fmt.Errorf("HTTP source %s changed before its validated content could be accepted", candidate.url)
		}
	}

	now := time.Now()
	for _, candidate := range candidates {
		entry := candidate.entry
		entry.AcceptedContent = candidate.content
		entry.AcceptedChecksum = candidate.contentChecksum
		entry.AcceptedTime = now
		entry.LastAccessTime = now
		entry.ValidationState = StateAccepted
		entry.ETag = candidate.etag
		entry.LastModified = candidate.lastModified
		entry.mutationRevision++
	}
	return nil
}
