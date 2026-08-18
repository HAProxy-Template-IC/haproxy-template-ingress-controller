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

// The manifest paths of the auxiliary file set this suite drives. Each string
// is at the same time the file's place in the pod's tree and the name HAProxy
// knows it by at runtime.
const (
	auxMapPath      = MapsDir + "/domains.map"
	auxCRTListPath  = SSLDir + "/crt-list.txt"
	auxCertPath     = SSLDir + "/example.com.pem"
	auxSecondCert   = SSLDir + "/test.com.pem"
	auxCAPath       = GeneralDir + "/trust.crt"
	auxErrorPath    = GeneralDir + "/503.http"
	auxCertFile     = "example.com.pem" // as a crt-list line names it, under crt-base
	auxSecondFile   = "test.com.pem"
	auxErrorSection = "aux-errors"
)

// auxConfig references one file of every kind the render can produce, so the
// pod's runtime inventory has to list all of them.
const auxConfig = `global
    log stdout format raw local0

defaults
    log     global
    mode    http
    timeout connect 5000ms
    timeout client  50000ms
    timeout server  50000ms

http-errors ` + auxErrorSection + `
    errorfile 503 ` + auxErrorPath + `

frontend http
    bind *:80
    use_backend %[req.hdr(host),lower,map(` + auxMapPath + `,web)]

frontend https
    bind *:443 ssl crt-list ` + auxCRTListPath + `
    default_backend web

backend web
    errorfiles ` + auxErrorSection + `
    default-server ssl ca-file ` + auxCAPath + ` verify required
    server srv1 192.0.2.1:443
`

// TestAuxiliaryFiles walks one pod through the life cycle of every auxiliary
// file kind: the initial apply loads them, a content change reaches the
// running worker where HAProxy has a command for it, and a file the manifest
// drops leaves the pod.
//
// The subtests share one pod and run in order: each is a step of the same
// life cycle, not an independent scenario.
func TestAuxiliaryFiles(t *testing.T) {
	t.Parallel()
	env := fixenv.New(t)
	ctx := context.Background()
	session := NewSession(t, env)

	session.SetConfig(withPodGlobals(t, auxConfig))
	session.Set(auxMapPath, LoadTestFileContent(t, "map-files/domains.map"))
	session.Set(auxCertPath, LoadTestFileContent(t, "ssl-certs/example.com.pem"))
	session.SetCRTList(auxCRTListPath, renderplan.CRTListEntry{Cert: auxCertFile})
	session.SetOfKind(auxCAPath, LoadTestFileContent(t, "ca-files/ca-a.crt"), renderplan.FileKindCA)
	session.Set(auxErrorPath, LoadTestFileContent(t, "error-files/503.http"))

	t.Run("initial-apply-loads-every-kind", func(t *testing.T) {
		require.Equal(t, deployplan.VerdictReload, session.MustApply(ctx).Verdict,
			"a pod with no baseline gets full state and a reload")

		for _, path := range session.Paths() {
			content, err := session.haproxy.ReadFile(ctx, path)
			require.NoError(t, err, "reading %s from the pod", path)
			assert.Equal(t, session.Content(path), content, "%s differs from the applied content", path)
		}

		inventory := session.State(ctx).Inventory
		assert.Contains(t, inventory.Maps, auxMapPath, "the worker must have loaded the map")
		assert.Contains(t, inventory.Certs, auxCertPath, "the worker must have loaded the certificate")
		assert.Contains(t, inventory.CAFiles, auxCAPath, "the worker must have loaded the CA file")
		assert.Contains(t, inventory.CRTLists, auxCRTListPath, "the worker must have loaded the crt-list")
	})

	t.Run("map-content-change-runs-on-the-worker", func(t *testing.T) {
		before := workerPID(t, ctx, session)
		session.Set(auxMapPath, LoadTestFileContent(t, "map-files/domains-updated.map"))

		assert.Equal(t, deployplan.VerdictRuntime, session.MustApply(ctx).Verdict)
		assert.Equal(t, before, workerPID(t, ctx, session), "a map change must not replace the worker")

		entries, err := session.haproxy.RuntimeMapEntries(ctx, auxMapPath)
		require.NoError(t, err)
		assert.Equal(t, mapEntriesOf(session.Content(auxMapPath)), entries,
			"the worker's in-memory map must match the file")
	})

	t.Run("certificate-content-change-runs-on-the-worker", func(t *testing.T) {
		before := workerPID(t, ctx, session)
		session.Set(auxCertPath, LoadTestFileContent(t, "ssl-certs/updated.com.pem"))

		assert.Equal(t, deployplan.VerdictRuntime, session.MustApply(ctx).Verdict)
		assert.Equal(t, before, workerPID(t, ctx, session), "a certificate rotation must not replace the worker")

		content, err := session.haproxy.ReadFile(ctx, auxCertPath)
		require.NoError(t, err)
		assert.Equal(t, session.Content(auxCertPath), content,
			"the rotated certificate must also be on disk, so a later reload keeps it")
	})

	t.Run("crt-list-entry-added-on-the-worker", func(t *testing.T) {
		before := workerPID(t, ctx, session)
		session.Set(auxSecondCert, LoadTestFileContent(t, "ssl-certs/test.com.pem"))
		session.SetCRTList(auxCRTListPath,
			renderplan.CRTListEntry{Cert: auxCertFile},
			renderplan.CRTListEntry{Cert: auxSecondFile, SNIFilters: []string{"test.com"}})

		assert.Equal(t, deployplan.VerdictRuntime, session.MustApply(ctx).Verdict)
		assert.Equal(t, before, workerPID(t, ctx, session), "adding a certificate must not replace the worker")

		inventory := session.State(ctx).Inventory
		assert.Contains(t, inventory.Certs, auxSecondCert,
			"the added certificate must be in the worker's store, not only on disk")
	})

	t.Run("general-file-change-reloads", func(t *testing.T) {
		before := workerPID(t, ctx, session)
		session.Set(auxErrorPath, LoadTestFileContent(t, "error-files/custom400.http"))

		decision := session.MustApply(ctx)
		assert.Equal(t, deployplan.VerdictReload, decision.Verdict,
			"HAProxy reads an error file while parsing, so only a reload picks up a change")
		assert.NotEqual(t, before, workerPID(t, ctx, session), "a reload must replace the worker")
	})

	t.Run("dropped-file-leaves-the-pod", func(t *testing.T) {
		// The certificate stops being referenced and stops being declared; the
		// manifest is the complete desired state, so absence deletes it.
		session.SetCRTList(auxCRTListPath, renderplan.CRTListEntry{Cert: auxCertFile})
		session.Remove(auxSecondCert)

		session.MustApply(ctx)
		assert.False(t, session.haproxy.FileExists(ctx, auxSecondCert),
			"a file the manifest no longer declares must be gone from the pod")

		entries, err := session.haproxy.ListDir(ctx, SSLDir)
		require.NoError(t, err)
		assert.NotContains(t, entries, auxSecondFile)
	})
}
