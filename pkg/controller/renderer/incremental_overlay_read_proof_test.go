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

package renderer

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/incremental"
)

func overlayReadProofSession() *incrementalRenderSession {
	return &incrementalRenderSession{
		resourceProofs: map[incremental.InputKey]incremental.Input{},
	}
}

const overlayReadProofKey = "ingresses/default/api"

func overlayReadProof(value string) incremental.Input {
	return incremental.Input{
		Key:      incremental.NewInputKey(overlayReadProofKey),
		Revision: incremental.NewRevision("1"),
		Found:    true,
		Value:    []byte(value),
	}
}

// A resource must read the same way all render, so a second read that disagrees
// is a torn view and has to fail.
func TestARereadThatDisagreesWithTheRenderIsRefused(t *testing.T) {
	session := overlayReadProofSession()
	require.NoError(t, session.recordResourceProof(overlayReadProof("base")))

	err := session.recordResourceProof(overlayReadProof("changed"))

	require.Error(t, err)
	assert.ErrorIs(t, err, incremental.ErrRevisionConflict)
}

// Staging the admission overlay is the one read that is meant to disagree: the
// proposed object IS a different value for a key the base reads already
// recorded. Checking it there denied every update to an already-read resource --
// `kubectl annotate ingress` came back "denied ... incremental input revision
// conflict" while creates went through.
func TestStagingTheAdmissionOverlayReplacesTheBaseRead(t *testing.T) {
	session := overlayReadProofSession()
	require.NoError(t, session.recordResourceProof(overlayReadProof("base")))

	session.stagingOverlays.Store(true)
	require.NoError(t, session.recordResourceProof(overlayReadProof("proposed")))
	session.stagingOverlays.Store(false)

	assert.Equal(t, []byte("proposed"),
		session.resourceProofs[incremental.NewInputKey(overlayReadProofKey)].Value,
		"later reads must be held to the overlaid value, not the base one")
}

// The overlaid value becomes the value of record, so a read after staging that
// disagrees with it is still a torn view.
func TestARereadAfterStagingIsHeldToTheOverlaidValue(t *testing.T) {
	session := overlayReadProofSession()
	session.stagingOverlays.Store(true)
	require.NoError(t, session.recordResourceProof(overlayReadProof("proposed")))
	session.stagingOverlays.Store(false)

	require.NoError(t, session.recordResourceProof(overlayReadProof("proposed")))
	err := session.recordResourceProof(overlayReadProof("something else"))

	require.Error(t, err)
	assert.ErrorIs(t, err, incremental.ErrRevisionConflict)
}
