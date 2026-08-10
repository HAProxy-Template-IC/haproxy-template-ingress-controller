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
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/auxiliaryfiles"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/parser"
)

func mustParse(t *testing.T, cfg string) *parser.StructuredConfig {
	t.Helper()
	p, err := parser.New()
	require.NoError(t, err)
	sc, err := p.ParseFromString(cfg)
	require.NoError(t, err)
	return sc
}

// TestComputeReloadImpact covers the config/aux verdict combinations that matter
// for the offline preview: the auxiliary-file cases (map/cert content updates are
// runtime-eligible; map create and general-file changes force a reload) that the
// config comparator alone cannot see, plus a structural config change.
func TestComputeReloadImpact(t *testing.T) {
	const cfg = "global\n  daemon\ndefaults\n  mode http\nfrontend fe\n  bind :80\n  default_backend be\nbackend be\n  server s1 10.0.0.1:80 check\n"
	sc := mustParse(t, cfg)
	// Two servers so the address-only edit is a runtime field update, not add/delete.
	const cfgIP = "global\n  daemon\ndefaults\n  mode http\nfrontend fe\n  bind :80\n  default_backend be\nbackend be\n  server s1 10.0.0.9:80 check\n"
	scIP := mustParse(t, cfgIP)
	const cfgMixedBase = "global\n  daemon\ndefaults\n  mode http\nfrontend fe\n  bind :80\n  default_backend be\nbackend be\n  server s1 10.0.0.1:80 check\n  server s2 10.0.0.2:80 check\n"
	scMixedBase := mustParse(t, cfgMixedBase)
	const cfgMixedDesired = "global\n  daemon\ndefaults\n  mode http\nfrontend fe\n  bind :80\n  default_backend be\nbackend be\n  server s1 10.0.0.9:80 check\n  server s2 10.0.0.2:80 check ssl verify none\n"
	scMixedDesired := mustParse(t, cfgMixedDesired)
	const cfgBackend = cfg + "backend be2\n  server s1 10.0.0.2:80 check\n"
	scBackend := mustParse(t, cfgBackend)

	baseMap := &AuxiliaryFiles{MapFiles: []auxiliaryfiles.MapFile{{Path: "host.map", Content: "a b\n"}}}
	updMap := &AuxiliaryFiles{MapFiles: []auxiliaryfiles.MapFile{{Path: "host.map", Content: "a c\n"}}}
	newMap := &AuxiliaryFiles{MapFiles: []auxiliaryfiles.MapFile{{Path: "host.map", Content: "a b\n"}, {Path: "n.map", Content: "x y\n"}}}
	baseFile := &AuxiliaryFiles{GeneralFiles: []auxiliaryfiles.GeneralFile{{Filename: "500.http", Content: "a"}}}
	updFile := &AuxiliaryFiles{GeneralFiles: []auxiliaryfiles.GeneralFile{{Filename: "500.http", Content: "b"}}}
	noReload := false
	baseSidecar := &AuxiliaryFiles{GeneralFiles: []auxiliaryfiles.GeneralFile{{Filename: "vector.yaml", Content: "a", ReloadOnPush: &noReload}}}
	updSidecar := &AuxiliaryFiles{GeneralFiles: []auxiliaryfiles.GeneralFile{{Filename: "vector.yaml", Content: "b", ReloadOnPush: &noReload}}}

	caps32, _ := ParseVersionString("3.2")
	caps30, _ := ParseVersionString("3.0")

	tests := []struct {
		name                    string
		baseCfg, desiredCfg     *parser.StructuredConfig
		baseAux, desiredAux     *AuxiliaryFiles
		desiredCfgText          string
		caps                    Capabilities
		wantChanged, wantReload bool
		wantRuntimeMaps         int
		wantReloadFreeFiles     int
	}{
		{name: "no change", baseCfg: sc, desiredCfg: sc, caps: CapabilitiesFromVersion(caps32)},
		{name: "map content update -> runtime", baseCfg: sc, desiredCfg: sc, baseAux: baseMap, desiredAux: updMap, caps: CapabilitiesFromVersion(caps32), wantChanged: true, wantReload: false, wantRuntimeMaps: 1},
		{name: "map create -> reload", baseCfg: sc, desiredCfg: sc, baseAux: baseMap, desiredAux: newMap, caps: CapabilitiesFromVersion(caps32), wantChanged: true, wantReload: true},
		{name: "general file update -> reload", baseCfg: sc, desiredCfg: sc, baseAux: baseFile, desiredAux: updFile, caps: CapabilitiesFromVersion(caps32), wantChanged: true, wantReload: true},
		{name: "sidecar general file update -> no reload, still reported as changed", baseCfg: sc, desiredCfg: sc, baseAux: baseSidecar, desiredAux: updSidecar, caps: CapabilitiesFromVersion(caps32), wantChanged: true, wantReload: false, wantReloadFreeFiles: 1},
		{name: "sidecar general file create -> no reload", baseCfg: sc, desiredCfg: sc, desiredAux: updSidecar, caps: CapabilitiesFromVersion(caps32), wantChanged: true, wantReload: false, wantReloadFreeFiles: 1},
		// `cfg` names no auxiliary file, so removing one dangles nothing. The
		// preview must agree with the deployer here or a pinned baseline would
		// promise a reload the deploy doesn't take.
		{name: "sidecar general file delete -> no reload", baseCfg: sc, desiredCfg: sc, baseAux: baseSidecar, desiredCfgText: cfg, caps: CapabilitiesFromVersion(caps32), wantChanged: false, wantReload: false},
		{name: "general file delete with no config text -> reload", baseCfg: sc, desiredCfg: sc, baseAux: baseFile, caps: CapabilitiesFromVersion(caps32), wantChanged: true, wantReload: true},
		{name: "server address change -> runtime", baseCfg: sc, desiredCfg: scIP, caps: CapabilitiesFromVersion(caps32), wantChanged: true, wantReload: false},
		{name: "runtime and structural server changes -> reload", baseCfg: scMixedBase, desiredCfg: scMixedDesired, caps: CapabilitiesFromVersion(caps32), wantChanged: true, wantReload: true},
		{name: "new backend -> reload", baseCfg: sc, desiredCfg: scBackend, caps: CapabilitiesFromVersion(caps32), wantChanged: true, wantReload: true},
		{name: "map update on v3.0 is still runtime (maps are v3.0+)", baseCfg: sc, desiredCfg: sc, baseAux: baseMap, desiredAux: updMap, caps: CapabilitiesFromVersion(caps30), wantChanged: true, wantReload: false, wantRuntimeMaps: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			imp, err := ComputeReloadImpact(tt.baseCfg, tt.desiredCfg, tt.baseAux, tt.desiredAux, tt.desiredCfgText, tt.caps)
			require.NoError(t, err)
			// Mirrors the playground's own "changed" expression
			// (cmd/playground/main.go): a change the preview cannot express
			// here is a change it renders as "no change" to the user.
			assert.Equal(t, tt.wantChanged, imp.ConfigChanged || len(imp.MapUpdates) > 0 || len(imp.CertUpdates) > 0 || len(imp.ReloadFreeFileUpdates) > 0 || imp.AuxForcesReload, "changed")
			assert.Equal(t, tt.wantReload, imp.WouldReload, "wouldReload")
			assert.Len(t, imp.MapUpdates, tt.wantRuntimeMaps, "runtime map updates")
			assert.Len(t, imp.ReloadFreeFileUpdates, tt.wantReloadFreeFiles, "reload-free file updates")
		})
	}
}
