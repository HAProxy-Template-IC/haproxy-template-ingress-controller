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

package server

import (
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/agent/api"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/renderplan"
)

func TestCachedNACKRequiresExactWorkDespiteDigestCollision(t *testing.T) {
	first := []byte("maps/11c714b2cc3f873f.map")
	second := []byte("maps/71b06949baa8f2ff.map")
	digest := renderplan.Digest(first)
	require.Equal(t, digest, renderplan.Digest(second))
	result := &api.ApplyResult{PlanID: "bad", OK: false}
	server := &Server{
		logger: slog.New(slog.DiscardHandler),
		state: &persistentState{NACK: &nackRecord{
			ManifestDigest: digest,
			ManifestWork:   first,
			Until:          time.Now().Add(time.Minute),
			Result:         result,
		}},
	}

	assert.Same(t, result, server.cachedNACK(digest, first))
	assert.Nil(t, server.cachedNACK(digest, second))
	assert.Nil(t, server.cachedNACK(digest, nil))
}
