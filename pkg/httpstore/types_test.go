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

package httpstore

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestFetchOptions_WithDefaults(t *testing.T) {
	tests := []struct {
		name string
		in   FetchOptions
		want FetchOptions
	}{
		{
			name: "all defaults",
			in:   FetchOptions{},
			want: FetchOptions{
				Timeout:    DefaultTimeout,
				Retries:    DefaultRetries,
				RetryDelay: DefaultRetryDelay,
			},
		},
		{
			name: "explicit Timeout retained",
			in:   FetchOptions{Timeout: 5 * time.Second},
			want: FetchOptions{
				Timeout:    5 * time.Second,
				Retries:    DefaultRetries,
				RetryDelay: DefaultRetryDelay,
			},
		},
		{
			name: "explicit Retries retained",
			in:   FetchOptions{Retries: 7},
			want: FetchOptions{
				Timeout:    DefaultTimeout,
				Retries:    7,
				RetryDelay: DefaultRetryDelay,
			},
		},
		{
			name: "explicit RetryDelay retained",
			in:   FetchOptions{RetryDelay: 250 * time.Millisecond},
			want: FetchOptions{
				Timeout:    DefaultTimeout,
				Retries:    DefaultRetries,
				RetryDelay: 250 * time.Millisecond,
			},
		},
		{
			name: "delay and critical preserved unchanged",
			in:   FetchOptions{Delay: time.Minute, Critical: true},
			want: FetchOptions{
				Timeout:    DefaultTimeout,
				Retries:    DefaultRetries,
				RetryDelay: DefaultRetryDelay,
				Delay:      time.Minute,
				Critical:   true,
			},
		},
		{
			name: "all explicit values retained",
			in: FetchOptions{
				Timeout:    1 * time.Second,
				Retries:    1,
				RetryDelay: 100 * time.Millisecond,
			},
			want: FetchOptions{
				Timeout:    1 * time.Second,
				Retries:    1,
				RetryDelay: 100 * time.Millisecond,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.in.WithDefaults()
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestFetchOptions_WithDefaults_DoesNotMutateReceiver(t *testing.T) {
	original := FetchOptions{}
	_ = original.WithDefaults()
	// Receiver value is unchanged: WithDefaults returns a copy with defaults
	// applied; the original must still hold zero values.
	assert.Equal(t, time.Duration(0), original.Timeout)
	assert.Equal(t, 0, original.Retries)
	assert.Equal(t, time.Duration(0), original.RetryDelay)
}

func TestChecksum(t *testing.T) {
	tests := []struct {
		name    string
		content string
	}{
		{name: "empty content", content: ""},
		{name: "ascii content", content: "hello world"},
		{name: "binary-ish content", content: "\x00\x01\x02\xff"},
		{name: "large content", content: string(make([]byte, 10_000))},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := checksum(tt.content)
			// SHA-256 hex encoded is 64 characters.
			assert.Len(t, got, 64)

			// Verify it matches the standard library output.
			h := sha256.Sum256([]byte(tt.content))
			assert.Equal(t, hex.EncodeToString(h[:]), got)

			// Determinism: same input → same checksum.
			assert.Equal(t, got, checksum(tt.content))
		})
	}
}

func TestChecksum_DifferentInputsDifferOutputs(t *testing.T) {
	a := checksum("hello")
	b := checksum("hello!")
	assert.NotEqual(t, a, b)
}
