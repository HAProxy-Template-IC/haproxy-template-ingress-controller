package dataplane

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/auxiliaryfiles"
)

// runtimeEligibleAuxUpdates is the gate that lets a content-only change skip the
// reload. Map-content updates are runtime-eligible (v3.0+); SSL-cert content
// updates only on v3.2+ (per caps). Everything else — file create/delete, a cert
// update on <v3.2, and any other aux change — forces a reload. Pin every branch
// so the all-or-nothing guarantee can't regress.
func TestAuxiliaryFileDiffs_RuntimeEligibleAuxUpdates(t *testing.T) {
	mapUpd := []auxiliaryfiles.MapFile{{Path: "host.map", Content: "a b\n"}}
	certUpd := []auxiliaryfiles.SSLCertificate{{Path: "tls.pem", Content: "PEM"}}
	caUpd := []auxiliaryfiles.GeneralFile{{Path: "general/ca.crt", Content: "CABUNDLE", IsCaFile: true}}
	nonCaUpd := []auxiliaryfiles.GeneralFile{{Path: "general/500.http", Content: "body", IsCaFile: false}}
	noReload := false
	sidecarUpd := []auxiliaryfiles.GeneralFile{{Filename: "spoa-hub-config.toml", Path: "general/spoa-hub-config.toml", Content: "toml", ReloadOnPush: &noReload}}
	caps32 := Capabilities{SupportsRuntimeSSLCerts: true, SupportsSslCaFiles: true}
	caps31 := Capabilities{} // < v3.2: no runtime ssl certs / ca-files

	tests := []struct {
		name            string
		in              *auxiliaryFileDiffs
		caps            Capabilities
		wantMaps        int
		wantCerts       int
		wantCa          int
		wantNeedsReload bool
	}{
		{name: "nil diff", in: nil, caps: caps32},
		{name: "empty diff", in: &auxiliaryFileDiffs{}, caps: caps32},
		{
			name:     "map content update only: runtime, no reload",
			in:       &auxiliaryFileDiffs{mapDiff: &auxiliaryfiles.MapFileDiff{ToUpdate: mapUpd}},
			caps:     caps32,
			wantMaps: 1,
		},
		{
			name:            "map create forces reload",
			in:              &auxiliaryFileDiffs{mapDiff: &auxiliaryfiles.MapFileDiff{ToCreate: []auxiliaryfiles.MapFile{{Path: "n.map"}}}},
			caps:            caps32,
			wantNeedsReload: true,
		},
		{
			name:            "map delete forces reload",
			in:              &auxiliaryFileDiffs{mapDiff: &auxiliaryfiles.MapFileDiff{ToDelete: []string{"o.map"}}},
			caps:            caps32,
			wantNeedsReload: true,
		},
		{
			name:      "cert content update on v3.2+: runtime, no reload",
			in:        &auxiliaryFileDiffs{sslDiff: &auxiliaryfiles.SSLCertificateDiff{ToUpdate: certUpd}},
			caps:      caps32,
			wantCerts: 1,
		},
		{
			name:            "cert content update on <v3.2: forces reload",
			in:              &auxiliaryFileDiffs{sslDiff: &auxiliaryfiles.SSLCertificateDiff{ToUpdate: certUpd}},
			caps:            caps31,
			wantCerts:       0,
			wantNeedsReload: true,
		},
		{
			name:            "cert create forces reload",
			in:              &auxiliaryFileDiffs{sslDiff: &auxiliaryfiles.SSLCertificateDiff{ToCreate: certUpd}},
			caps:            caps32,
			wantNeedsReload: true,
		},
		{
			name:            "cert delete forces reload",
			in:              &auxiliaryFileDiffs{sslDiff: &auxiliaryfiles.SSLCertificateDiff{ToDelete: []string{"tls.pem"}}},
			caps:            caps32,
			wantNeedsReload: true,
		},
		{
			name: "map + cert content update on v3.2+: both runtime, no reload",
			in: &auxiliaryFileDiffs{
				mapDiff: &auxiliaryfiles.MapFileDiff{ToUpdate: mapUpd},
				sslDiff: &auxiliaryfiles.SSLCertificateDiff{ToUpdate: certUpd},
			},
			caps:      caps32,
			wantMaps:  1,
			wantCerts: 1,
		},
		{
			name: "map + cert content update on <v3.2: cert forces reload",
			in: &auxiliaryFileDiffs{
				mapDiff: &auxiliaryfiles.MapFileDiff{ToUpdate: mapUpd},
				sslDiff: &auxiliaryfiles.SSLCertificateDiff{ToUpdate: certUpd},
			},
			caps:            caps31,
			wantMaps:        1,
			wantNeedsReload: true,
		},
		{
			name:            "ca file change (dead SSLCaFiles slot) forces reload",
			in:              &auxiliaryFileDiffs{caFileDiff: &auxiliaryfiles.SSLCaFileDiff{ToDelete: []string{"ca.pem"}}},
			caps:            caps32,
			wantNeedsReload: true,
		},
		{
			name:   "ca-file (general) content update on v3.2+: runtime, no reload",
			in:     &auxiliaryFileDiffs{fileDiff: &auxiliaryfiles.FileDiff{ToUpdate: caUpd}},
			caps:   caps32,
			wantCa: 1,
		},
		{
			name:            "ca-file content update on <v3.2: forces reload",
			in:              &auxiliaryFileDiffs{fileDiff: &auxiliaryfiles.FileDiff{ToUpdate: caUpd}},
			caps:            caps31,
			wantCa:          0,
			wantNeedsReload: true,
		},
		{
			name:            "ca-file create forces reload",
			in:              &auxiliaryFileDiffs{fileDiff: &auxiliaryfiles.FileDiff{ToCreate: caUpd}},
			caps:            caps32,
			wantNeedsReload: true,
		},
		{
			name:            "non-ca general file content update forces reload",
			in:              &auxiliaryFileDiffs{fileDiff: &auxiliaryfiles.FileDiff{ToUpdate: nonCaUpd}},
			caps:            caps32,
			wantNeedsReload: true,
		},
		{
			name: "ca-file + map update on v3.2+: both runtime, no reload",
			in: &auxiliaryFileDiffs{
				fileDiff: &auxiliaryfiles.FileDiff{ToUpdate: caUpd},
				mapDiff:  &auxiliaryfiles.MapFileDiff{ToUpdate: mapUpd},
			},
			caps:     caps32,
			wantCa:   1,
			wantMaps: 1,
		},
		{
			name:            "general file create forces reload",
			in:              &auxiliaryFileDiffs{fileDiff: &auxiliaryfiles.FileDiff{ToCreate: []auxiliaryfiles.GeneralFile{{Filename: "x"}}}},
			caps:            caps32,
			wantNeedsReload: true,
		},
		{
			// The spoa-hub TOML and the vector config: HAProxy never opens
			// them, their sidecar watches the file itself. A reload here would
			// respawn a worker for a file it doesn't read.
			name: "sidecar general file content update: no reload",
			in:   &auxiliaryFileDiffs{fileDiff: &auxiliaryfiles.FileDiff{ToUpdate: sidecarUpd}},
			caps: caps32,
		},
		{
			name: "sidecar general file create: no reload",
			in:   &auxiliaryFileDiffs{fileDiff: &auxiliaryfiles.FileDiff{ToCreate: sidecarUpd}},
			caps: caps32,
		},
		{
			// reloadOnPush is per file, not per batch: one HAProxy-read file in
			// the same push still reloads. The all-or-nothing runtime gate then
			// makes the sidecar file's exemption moot for this deploy.
			name: "sidecar file alongside an HAProxy-read file forces reload",
			in: &auxiliaryFileDiffs{fileDiff: &auxiliaryfiles.FileDiff{
				ToUpdate: append(append([]auxiliaryfiles.GeneralFile{}, sidecarUpd...), nonCaUpd...),
			}},
			caps:            caps32,
			wantNeedsReload: true,
		},
		{
			// A delete carries only the identifier, so reloadOnPush is gone by
			// then; the desired state is searched for the name instead. Nothing
			// names a sidecar's own config, so nothing dangles.
			name: "delete of a file the desired state does not name: no reload",
			in: &auxiliaryFileDiffs{
				fileDiff:   &auxiliaryfiles.FileDiff{ToDelete: []string{"spoa-hub-config.toml"}},
				references: newFileReferences("frontend fe\n  bind :80\n", nil),
			},
			caps: caps32,
		},
		{
			name: "delete of a file the config still names forces reload",
			in: &auxiliaryFileDiffs{
				fileDiff:   &auxiliaryfiles.FileDiff{ToDelete: []string{"503.http"}},
				references: newFileReferences("defaults\n  errorfile 503 general/503.http\n", nil),
			},
			caps:            caps32,
			wantNeedsReload: true,
		},
		{
			// `ca-file <path>` on a crt-list line is the one place HAPTIC names
			// a general file outside haproxy.cfg. Searching the config alone
			// would drop the reload and let the reference dangle.
			name: "delete of a ca-file named only by a crt-list forces reload",
			in: &auxiliaryFileDiffs{
				fileDiff: &auxiliaryfiles.FileDiff{ToDelete: []string{"client-ca.pem"}},
				references: newFileReferences("frontend fe\n  bind :80\n", &AuxiliaryFiles{
					CRTListFiles: []auxiliaryfiles.CRTListFile{{
						Path:    "certificate-list.txt",
						Content: "tls.pem [ocsp-update on ca-file general/client-ca.pem verify required] *\n",
					}},
				}),
			},
			caps:            caps32,
			wantNeedsReload: true,
		},
		{
			// Zero value: no desired state to search, so every delete reloads.
			// Keeps any path that forgets to populate it on the safe side.
			name:            "delete with no reference scan forces reload",
			in:              &auxiliaryFileDiffs{fileDiff: &auxiliaryfiles.FileDiff{ToDelete: []string{"spoa-hub-config.toml"}}},
			caps:            caps32,
			wantNeedsReload: true,
		},
		{
			name:            "crtlist change forces reload",
			in:              &auxiliaryFileDiffs{crtlistDiff: &auxiliaryfiles.CRTListDiff{ToUpdate: []auxiliaryfiles.CRTListFile{{Path: "l.txt"}}}},
			caps:            caps32,
			wantNeedsReload: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			maps, certs, ca, needsReload := tt.in.runtimeEligibleAuxUpdates(tt.caps)
			assert.Len(t, maps, tt.wantMaps, "map updates")
			assert.Len(t, certs, tt.wantCerts, "cert updates")
			assert.Len(t, ca, tt.wantCa, "ca-file updates")
			assert.Equal(t, tt.wantNeedsReload, needsReload, "needs reload")
		})
	}
}
