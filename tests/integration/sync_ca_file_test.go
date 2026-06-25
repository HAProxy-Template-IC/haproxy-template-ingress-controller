//go:build integration

package integration

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/rekby/fixenv"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/auxiliaryfiles"
)

// caFileBackendConfig references a CA file (mTLS trust bundle) from a backend
// server's `ssl ca-file … verify required` — i.e. HAProxy verifying the upstream
// server's certificate (backend mTLS, the SPIFFE/SPIRE case). The CA file lives
// in the general storage dir and is delivered as a flagged general file.
const caFileBackendConfig = `
global
    log stdout format raw local0
    stats socket /var/run/haproxy.sock mode 600 level admin

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
    default-server ssl ca-file /etc/haproxy/general/runtime-ca.crt verify required
    server srv1 192.0.2.1:443
`

// TestSyncSSLCaFileRuntimeNoReload is the empirical proof of reload-free CA-file
// (mTLS trust bundle) rotation: a CONTENT-only change to a ca-file referenced by
// a backend `ssl ca-file … verify` applies to the live worker via the runtime
// API with NO reload on DataPlane API v3.2+.
//
// It applies via the `add ssl ca-file` runtime endpoint (addCaEntry), NOT
// `set ssl ca-file`: the DataPlane API's set path returns 500 under the
// master-worker socket wrapping (the slower CA validation races the connection
// close). `add ssl ca-file` with no ongoing transaction replaces the file with
// the payload and applies reliably. It is the controller-side equivalent of what
// the SPIFFE cert-reloader sidecar does over the raw socket. The load-bearing
// behaviours it validates: GetAllSSLCaFiles lists the config-loaded ca-file so
// the resolver can map it, and addCaEntry replaces the live content without a
// reload — if either fails, the orchestrator falls back to a reload, failing the
// assertion.
func TestSyncSSLCaFileRuntimeNoReload(t *testing.T) {
	t.Parallel()
	env := fixenv.New(t)
	ctx := context.Background()

	lowLevel := TestDataplaneClient(env)
	if !lowLevel.Capabilities().SupportsSslCaFiles {
		t.Skipf("HAProxy %s lacks runtime SSL CA file support (v3.2+); reload fallback path is covered by the unit classifier test", lowLevel.DetectedVersion())
	}
	dpClient := TestDataplaneHighLevelClient(env)

	read := func(name string) string {
		content, err := os.ReadFile(filepath.Join("testdata", "ca-files", name))
		require.NoError(t, err, "read fixture %s", name)
		return string(content)
	}
	caFile := func(content string) *dataplane.AuxiliaryFiles {
		return &dataplane.AuxiliaryFiles{
			GeneralFiles: []auxiliaryfiles.GeneralFile{{
				Filename: "runtime-ca.crt",
				Path:     "/etc/haproxy/general/runtime-ca.crt",
				Content:  content,
				IsCaFile: true,
			}},
		}
	}

	// Initial deploy: uploads the CA bundle (general storage) + pushes the config
	// that references it. Structural → reload.
	initial, err := dpClient.Sync(ctx, caFileBackendConfig, caFile(read("ca-a.crt")), nil)
	require.NoError(t, err, "initial sync")
	require.True(t, initial.Success, "initial sync should succeed")

	// Diagnostic: what identifier does the DPA report for the config-loaded
	// ca-file? Drives the resolver; logged so a mismatch is debuggable.
	loaded, err := lowLevel.GetAllSSLCaFiles(ctx)
	require.NoError(t, err, "list loaded ca-files")
	t.Logf("loaded ca-files reported by DataPlane API: %v", loaded)
	assert.NotEmpty(t, loaded, "the config-loaded ca-file must be listed for the runtime path to address it")

	// Rotate the trust bundle (content-only; identical config). Must apply via
	// the runtime API with NO reload.
	rotated, err := dpClient.Sync(ctx, caFileBackendConfig, caFile(read("ca-b.crt")), nil)
	require.NoError(t, err, "ca-file rotation sync")
	require.True(t, rotated.Success, "rotation sync should succeed")

	assert.False(t, rotated.ReloadTriggered, "CA bundle rotation must NOT trigger a reload")
	assert.Empty(t, rotated.ReloadID, "no reload was triggered, so no reload ID")
	assert.Equal(t, dataplane.SyncModeRuntime, rotated.SyncMode, "rotation must run in runtime mode")
}
