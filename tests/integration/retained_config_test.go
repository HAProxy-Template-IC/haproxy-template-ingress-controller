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

//go:build integration

package integration

import (
	"testing"

	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/rendergate"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/validation"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/planblob"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/renderartifact"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/renderoutput"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/renderplan"
)

func TestRetainedConfigurationChecksCurrentHAProxy(t *testing.T) {
	for _, tc := range []struct {
		name, directive string
		rejected        bool
	}{
		{"current", "maxconn 100", false},
		{"removed nbproc directive", "nbproc 2", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			config := "global\n  " + tc.directive + "\ndefaults\n  mode http\n  timeout connect 1s\n  timeout client 1s\n  timeout server 1s\nfrontend health\n  bind 127.0.0.1:18080\n  http-request return status 200\n"
			plan := &renderplan.Plan{SchemaVersion: renderplan.SchemaVersion,
				Sections: []renderplan.Section{{Kind: renderplan.SectionKindCore, Name: "core#0", Text: config, TextKnown: true, TextDigest: renderplan.DigestString(config), Length: len(config)}},
				Files:    []renderplan.File{{Path: "haproxy.cfg", Kind: renderplan.FileKindConfig, Content: config, ContentKnown: true, Digest: renderplan.DigestString(config), Size: int64(len(config)), ReloadOnChange: true}},
			}
			plan.ComputeID()
			encoded, err := planblob.EncodeCheckpoint(plan)
			require.NoError(t, err)
			restored, err := planblob.DecodeCheckpoint(encoded, config, map[string]string{"haproxy.cfg": config})
			require.NoError(t, err)
			artifactAuthority := renderartifact.NewAuthority()
			builder, err := renderartifact.NewBuilder(artifactAuthority, nil)
			require.NoError(t, err)
			artifacts, err := builder.Build()
			require.NoError(t, err)
			authority, err := renderoutput.NewAuthority(renderplan.NewAuthority(), artifactAuthority)
			require.NoError(t, err)
			output, err := renderoutput.NewSnapshot(authority, config, restored, artifacts, nil)
			require.NoError(t, err)
			checksum, err := output.ContentChecksum()
			require.NoError(t, err)
			checker := rendergate.ServiceChecker{Service: validation.NewValidationService(&validation.ValidationServiceConfig{SkipDNSValidation: true})}
			err = checker.CheckOutput(t.Context(), output, checksum)
			if tc.rejected {
				require.ErrorContains(t, err, "nbproc")
			} else {
				require.NoError(t, err)
			}
		})
	}
}
