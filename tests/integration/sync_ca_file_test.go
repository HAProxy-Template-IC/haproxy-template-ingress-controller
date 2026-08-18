//go:build integration

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

package integration

import (
	"context"
	"testing"

	"github.com/rekby/fixenv"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/deployplan"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/renderplan"
)

// caFilePath is where the trust bundle lives in the pod's tree; HAProxy names
// it by the same string at runtime, which is what `set ssl ca-file` addresses.
const caFilePath = GeneralDir + "/runtime-ca.crt"

// caFileBackendConfig references a CA file (mTLS trust bundle) from a backend
// server's `ssl ca-file … verify required` — HAProxy verifying the upstream
// server's certificate, the SPIFFE/SPIRE case.
const caFileBackendConfig = `global
    log stdout format raw local0

defaults
    log     global
    mode    http
    timeout connect 5000ms
    timeout client  50000ms
    timeout server  50000ms

frontend f
    bind *:80
    default_backend b

backend b
    default-server ssl ca-file ` + caFilePath + ` verify required
    server srv1 192.0.2.1:443
`

// TestSyncSSLCaFileRuntimeNoReload proves reload-free rotation of an mTLS
// trust bundle: a content-only change to a CA file the configuration
// references runs on the live worker, and the pod keeps the worker it had.
func TestSyncSSLCaFileRuntimeNoReload(t *testing.T) {
	t.Parallel()
	env := fixenv.New(t)
	ctx := context.Background()
	session := NewSession(t, env)

	// Initial deploy: the bundle plus the configuration that references it.
	// A pod with no baseline gets full state and a reload.
	session.SetConfig(withPodGlobals(t, caFileBackendConfig))
	session.SetOfKind(caFilePath, LoadTestFileContent(t, "ca-files/ca-a.crt"), renderplan.FileKindCA)
	require.Equal(t, deployplan.VerdictReload, session.MustApply(ctx).Verdict)

	// The running worker must hold the CA file for the rotation to address it.
	assert.Contains(t, session.State(ctx).Inventory.CAFiles, caFilePath,
		"the configuration's CA file must be in the runtime store for a rotation to reach it")

	// Rotate the trust bundle: content only, identical configuration.
	before, err := session.haproxy.WorkerPID(ctx)
	require.NoError(t, err)

	session.SetOfKind(caFilePath, LoadTestFileContent(t, "ca-files/ca-b.crt"), renderplan.FileKindCA)
	rotation := session.MustApply(ctx)

	assert.Equal(t, deployplan.VerdictRuntime, rotation.Verdict, "a CA bundle rotation must not reload")
	after, err := session.haproxy.WorkerPID(ctx)
	require.NoError(t, err)
	assert.Equal(t, before, after, "the rotation must reach the running worker, not replace it")

	onDisk, err := session.haproxy.ReadFile(ctx, caFilePath)
	require.NoError(t, err)
	assert.Equal(t, LoadTestFileContent(t, "ca-files/ca-b.crt"), onDisk,
		"the rotated bundle must also be on disk, so a later reload keeps it")
}
