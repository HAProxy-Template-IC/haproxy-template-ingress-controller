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
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/auxiliaryfiles"
)

func TestValidateAuxiliaryFilesConsistency(t *testing.T) {
	cases := []struct {
		name        string
		config      string
		mapFiles    []auxiliaryfiles.MapFile
		wantErr     bool
		wantMissing []string
	}{
		{
			name:     "no references is fine",
			config:   "global\n  daemon\nfrontend f\n  bind *:80\n",
			mapFiles: nil,
			wantErr:  false,
		},
		{
			name: "rule and map both present",
			config: `frontend http_frontend
  http-request redirect scheme https code 301 if !{ ssl_fc } { var(txn.host),map_str(maps/ssl-redirect-301.map) -m found }
`,
			mapFiles: []auxiliaryfiles.MapFile{
				{Path: "ssl-redirect-301.map", Content: "foo.com 1\n"},
			},
			wantErr: false,
		},
		{
			name: "rule references missing map — the production failure mode",
			config: `frontend http_frontend
  http-request redirect scheme https code 301 if !{ ssl_fc } { var(txn.host),map_str(maps/ssl-redirect-301.map) -m found }
`,
			mapFiles:    nil,
			wantErr:     true,
			wantMissing: []string{"ssl-redirect-301.map"},
		},
		{
			name: "absolute path map reference is also matched",
			config: `frontend http_frontend
  use_backend %[var(txn.host),map_str(/etc/haproxy/maps/host.map)]
`,
			mapFiles:    nil,
			wantErr:     true,
			wantMissing: []string{"host.map"},
		},
		{
			name: "multiple missing maps are all reported",
			config: `frontend http_frontend
  use_backend %[var(txn.host),map_str(maps/host.map)]
  http-request redirect scheme https code 301 if !{ ssl_fc } { var(txn.host),map_str(maps/ssl-redirect-301.map) -m found }
`,
			mapFiles:    []auxiliaryfiles.MapFile{{Path: "path-prefix.map"}},
			wantErr:     true,
			wantMissing: []string{"host.map", "ssl-redirect-301.map"},
		},
		{
			name: "map_beg / map_dir / map_dom variants are all matched",
			config: `frontend http_frontend
  use_backend %[var(txn.host),map_dom(maps/dom.map)]
  use_backend %[path,map_beg(maps/beg.map)]
  use_backend %[path,map_dir(maps/dir.map)]
`,
			mapFiles:    nil,
			wantErr:     true,
			wantMissing: []string{"beg.map", "dir.map", "dom.map"},
		},
		{
			name: "comma-delimited default value is parsed correctly",
			config: `frontend http_frontend
  use_backend %[var(txn.host),map_str(maps/host.map,default-backend)]
`,
			mapFiles:    nil,
			wantErr:     true,
			wantMissing: []string{"host.map"},
		},
		{
			name: "non-.map argument is ignored (avoid false positives on map_str over txn vars)",
			config: `frontend http_frontend
  http-request set-var(txn.x) str(prefix),map_str(some-arg-that-isnt-a-file)
`,
			mapFiles: nil,
			wantErr:  false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			aux := &dataplane.AuxiliaryFiles{MapFiles: tc.mapFiles}
			err := validateAuxiliaryFilesConsistency(tc.config, aux)
			if !tc.wantErr {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)

			var consErr *auxiliaryFilesConsistencyError
			require.True(t, errors.As(err, &consErr), "expected auxiliaryFilesConsistencyError, got %T", err)
			assert.Equal(t, tc.wantMissing, consErr.missingMaps)
		})
	}
}
