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

package dataplane

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/parser/parserconfig"
)

// TestPopulatePostSyncParsedConfig_Success verifies that on a successful
// fetch + parse, the helper writes the parser's output into the caller's
// result struct. This is the path the deployer's version-cache relies on
// to store the pod's ACTUAL post-sync state rather than the caller's
// desired intent — the architectural invariant the cross-pod drift fix
// hinges on.
func TestPopulatePostSyncParsedConfig_Success(t *testing.T) {
	const postSyncRaw = "global\n  daemon\n\ndefaults\n  mode http\n"

	sentinel := &parserconfig.StructuredConfig{}
	parser := &mockConfigParser{
		parseFunc: func(config string) (*parserconfig.StructuredConfig, error) {
			assert.Equal(t, postSyncRaw, config,
				"helper must pass the bytes it just fetched into the parser")
			return sentinel, nil
		},
	}

	orch, cleanup := createTestOrchestratorWithParser(t, func(w http.ResponseWriter, r *http.Request) {
		if v3InfoResponse(w, r) {
			return
		}
		if r.URL.Path == "/services/haproxy/configuration/raw" {
			w.WriteHeader(http.StatusOK)
			fmt.Fprint(w, postSyncRaw)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}, parser)
	defer cleanup()

	result := &SyncResult{}
	orch.populatePostSyncParsedConfig(context.Background(), result)

	assert.Same(t, sentinel, result.PostSyncParsedConfig,
		"helper must store the parser's output in result.PostSyncParsedConfig "+
			"so the deployer caches the actual post-sync state, not the caller's desired intent")
}

// TestPopulatePostSyncParsedConfig_FetchError verifies that a failed
// GetRawConfiguration call is non-fatal: the field stays nil and callers
// fall back to the input desired (status quo). We don't fail the sync
// over a post-sync read error — the sync already committed.
func TestPopulatePostSyncParsedConfig_FetchError(t *testing.T) {
	var parseCalls atomic.Int32

	parser := &mockConfigParser{
		parseFunc: func(string) (*parserconfig.StructuredConfig, error) {
			parseCalls.Add(1)
			return &parserconfig.StructuredConfig{}, nil
		},
	}

	orch, cleanup := createTestOrchestratorWithParser(t, func(w http.ResponseWriter, r *http.Request) {
		if v3InfoResponse(w, r) {
			return
		}
		if r.URL.Path == "/services/haproxy/configuration/raw" {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}, parser)
	defer cleanup()

	result := &SyncResult{}
	orch.populatePostSyncParsedConfig(context.Background(), result)

	assert.Nil(t, result.PostSyncParsedConfig,
		"failed GetRawConfiguration must leave PostSyncParsedConfig nil (caller falls back)")
	assert.Equal(t, int32(0), parseCalls.Load(),
		"parser must not run when fetch failed")
}

// TestPopulatePostSyncParsedConfig_ParseError verifies that a failed
// parse is also non-fatal: field stays nil, no panic, no error returned.
func TestPopulatePostSyncParsedConfig_ParseError(t *testing.T) {
	parser := &mockConfigParser{
		parseFunc: func(string) (*parserconfig.StructuredConfig, error) {
			return nil, errors.New("simulated parse failure")
		},
	}

	orch, cleanup := createTestOrchestratorWithParser(t, func(w http.ResponseWriter, r *http.Request) {
		if v3InfoResponse(w, r) {
			return
		}
		if r.URL.Path == "/services/haproxy/configuration/raw" {
			w.WriteHeader(http.StatusOK)
			fmt.Fprint(w, "anything")
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}, parser)
	defer cleanup()

	result := &SyncResult{}
	orch.populatePostSyncParsedConfig(context.Background(), result)

	assert.Nil(t, result.PostSyncParsedConfig,
		"failed parse must leave PostSyncParsedConfig nil (caller falls back)")
}
