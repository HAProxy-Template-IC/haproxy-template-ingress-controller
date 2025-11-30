// Copyright 2025 Philipp Hossner
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

// Package httpstore provides HTTP resource fetching with caching and validation.
//
// This package implements a store that fetches resources from HTTP(S) URLs,
// caches them, and supports periodic refresh with a two-version cache for
// safe validation before accepting new content.
package httpstore

import (
	"crypto/sha256"
	"encoding/hex"
	"time"
)

// DefaultTimeout is the default HTTP request timeout.
const DefaultTimeout = 30 * time.Second

// DefaultRetries is the default number of retry attempts.
const DefaultRetries = 3

// DefaultRetryDelay is the default delay between retry attempts.
const DefaultRetryDelay = time.Second

// MaxContentSize is the maximum allowed content size (10MB).
const MaxContentSize = 10 * 1024 * 1024

// FetchOptions configures HTTP fetching behavior.
type FetchOptions struct {
	// Delay is the refresh interval (how often to re-fetch).
	// Zero means no automatic refresh (fetch once).
	Delay time.Duration

	// Timeout is the HTTP request timeout.
	// Default: 30s
	Timeout time.Duration

	// Retries is the number of retry attempts on failure.
	// Default: 3
	Retries int

	// RetryDelay is the wait time between retries.
	// Default: 1s
	RetryDelay time.Duration

	// Critical indicates whether fetch failure should fail the template render.
	// If true, a failed fetch returns an error.
	// If false, a failed fetch returns empty string and logs a warning.
	Critical bool
}

// WithDefaults returns a copy of the options with default values applied.
func (o FetchOptions) WithDefaults() FetchOptions {
	if o.Timeout == 0 {
		o.Timeout = DefaultTimeout
	}
	if o.Retries == 0 {
		o.Retries = DefaultRetries
	}
	if o.RetryDelay == 0 {
		o.RetryDelay = DefaultRetryDelay
	}
	return o
}

// AuthConfig configures HTTP authentication.
type AuthConfig struct {
	// Type is the authentication type: "basic", "bearer", or "header".
	Type string

	// Username for basic auth.
	Username string

	// Password for basic auth.
	Password string

	// Token for bearer auth.
	Token string

	// Headers for custom header auth (e.g., API keys).
	// These headers are added to every request.
	Headers map[string]string
}

// ValidationState represents the current validation state of a cached entry.
type ValidationState int

const (
	// StateAccepted means the accepted content is in use, no pending content.
	StateAccepted ValidationState = iota

	// StateValidating means pending content exists and is being validated.
	StateValidating

	// StateRejected means the last pending content was rejected, using accepted.
	StateRejected
)

// String returns a string representation of the validation state.
func (s ValidationState) String() string {
	switch s {
	case StateAccepted:
		return "accepted"
	case StateValidating:
		return "validating"
	case StateRejected:
		return "rejected"
	default:
		return "unknown"
	}
}

// CacheEntry holds cached content with two-version support for safe validation.
//
// The two-version design ensures that new content is only accepted after
// successful validation. This is critical for resources like IP blocklists
// where we must not discard the old blocklist before knowing the new one is valid.
type CacheEntry struct {
	// URL is the source URL for this entry.
	URL string

	// Accepted version (validated, in production use)
	AcceptedContent  string
	AcceptedChecksum string
	AcceptedTime     time.Time

	// Pending version (fetched, awaiting validation)
	PendingContent  string
	PendingChecksum string
	HasPending      bool

	// ValidationState tracks the current state of this entry.
	ValidationState ValidationState

	// HTTP caching headers for conditional requests
	ETag         string
	LastModified string

	// Configuration for this URL
	Options FetchOptions
	Auth    *AuthConfig
}

// Checksum computes SHA256 checksum of content.
func Checksum(content string) string {
	hash := sha256.Sum256([]byte(content))
	return hex.EncodeToString(hash[:])
}
