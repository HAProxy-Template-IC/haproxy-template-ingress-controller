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
	caps32 := Capabilities{SupportsRuntimeSSLCerts: true}
	caps31 := Capabilities{} // < v3.2: no runtime ssl certs

	tests := []struct {
		name            string
		in              *auxiliaryFileDiffs
		caps            Capabilities
		wantMaps        int
		wantCerts       int
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
			name:            "ca file change forces reload",
			in:              &auxiliaryFileDiffs{caFileDiff: &auxiliaryfiles.SSLCaFileDiff{ToDelete: []string{"ca.pem"}}},
			caps:            caps32,
			wantNeedsReload: true,
		},
		{
			name:            "general file change forces reload",
			in:              &auxiliaryFileDiffs{fileDiff: &auxiliaryfiles.FileDiff{ToCreate: []auxiliaryfiles.GeneralFile{{Filename: "x"}}}},
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
			maps, certs, needsReload := tt.in.runtimeEligibleAuxUpdates(tt.caps)
			assert.Len(t, maps, tt.wantMaps, "map updates")
			assert.Len(t, certs, tt.wantCerts, "cert updates")
			assert.Equal(t, tt.wantNeedsReload, needsReload, "needs reload")
		})
	}
}
