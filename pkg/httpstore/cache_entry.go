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

// Immutable publication copies may update access time without changing any input (ADR-0030).
func sameCacheEntryVersion(left, right *CacheEntry) bool {
	if left == right {
		return true
	}
	if left == nil || right == nil {
		return false
	}
	leftCopy := *left
	rightCopy := *right
	leftCopy.LastAccessTime = rightCopy.LastAccessTime
	return sameCacheEntrySource(&leftCopy, &rightCopy) &&
		sameCacheEntryContent(&leftCopy, &rightCopy)
}

func sameCacheEntry(left, right *CacheEntry) bool {
	if left == nil || right == nil {
		return left == right
	}
	leftCopy := *left
	rightCopy := *right
	return sameCacheEntrySource(&leftCopy, &rightCopy) &&
		sameCacheEntryContent(&leftCopy, &rightCopy)
}

func sameCacheEntrySource(left, right *CacheEntry) bool {
	return left.mutationRevision == right.mutationRevision &&
		left.replayRevision == right.replayRevision &&
		left.acceptedRevision == right.acceptedRevision &&
		left.sourceIdentity == right.sourceIdentity &&
		left.sourceDescriptor == right.sourceDescriptor &&
		left.sourceGeneration == right.sourceGeneration && left.fixture == right.fixture &&
		left.URL == right.URL && left.Options == right.Options &&
		sameAuthConfig(left.Auth, right.Auth)
}

func sameCacheEntryContent(left, right *CacheEntry) bool {
	return left.AcceptedContent == right.AcceptedContent &&
		left.AcceptedChecksum == right.AcceptedChecksum && left.AcceptedTime.Equal(right.AcceptedTime) &&
		left.LastAccessTime.Equal(right.LastAccessTime) && left.PendingContent == right.PendingContent &&
		left.PendingChecksum == right.PendingChecksum && left.PendingRevision == right.PendingRevision &&
		left.HasPending == right.HasPending && left.ValidationState == right.ValidationState &&
		left.ValidationStartedAt.Equal(right.ValidationStartedAt) && left.ETag == right.ETag &&
		left.LastModified == right.LastModified
}

func sameAuthConfig(left, right *AuthConfig) bool {
	if left == nil || right == nil {
		return left == right
	}
	if left.Type != right.Type || left.Username != right.Username || left.Password != right.Password ||
		left.Token != right.Token || len(left.Headers) != len(right.Headers) {
		return false
	}
	for name, value := range left.Headers {
		if other, exists := right.Headers[name]; !exists || other != value {
			return false
		}
	}
	return true
}
