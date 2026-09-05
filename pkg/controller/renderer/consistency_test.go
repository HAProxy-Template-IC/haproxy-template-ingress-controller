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
	"fmt"
	"path"
	"regexp"
	"strings"
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

func TestExtractMapReferencesPreservesConverterBoundaries(t *testing.T) {
	config := `
map(maps/plain.map)
map_end(maps/end.map)
map_int(maps/int.map)
map_ip(maps/ip.map)
map_reg(maps/reg.map)
map_sub(maps/sub.map)
wordmap_str(maps/ignored-word.map)
word_map_str(maps/ignored-underscore.map)
map_unknown(map_str(maps/nested.map))
map_str()
map_str(maps/not-a-map.txt)
`
	require.Equal(t, map[string]struct{}{
		"plain.map": {}, "end.map": {}, "int.map": {}, "ip.map": {},
		"reg.map": {}, "sub.map": {}, "nested.map": {},
	}, extractMapReferences(config))
}

func FuzzExtractMapReferencesMatchesLegacyPattern(f *testing.F) {
	for _, seed := range []string{
		"", "map_str(maps/a.map)", "wordmap_str(maps/a.map)",
		"map_unknown(map_str(maps/nested.map))", "map_str()",
		"map_str(maps/a.map,default)", "map_str(unclosed\nmap_beg(maps/b.map)",
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, config string) {
		if len(config) > 1<<20 {
			t.Skip()
		}
		want := extractMapReferencesWithLegacyPattern(config)
		got := extractMapReferences(config)
		require.Len(t, got, len(want))
		for name := range want {
			require.Contains(t, got, name)
		}
	})
}

var legacyMapReferencePattern = regexp.MustCompile(`\bmap(?:_str|_beg|_dir|_dom|_end|_int|_ip|_reg|_sub)?\(([^)]+)\)`)

func extractMapReferencesWithLegacyPattern(config string) map[string]struct{} {
	references := map[string]struct{}{}
	for _, match := range legacyMapReferencePattern.FindAllStringSubmatch(config, -1) {
		argument := strings.TrimSpace(match[1])
		if index := strings.Index(argument, ","); index >= 0 {
			argument = strings.TrimSpace(argument[:index])
		}
		name := path.Base(argument)
		if strings.HasSuffix(name, ".map") {
			references[name] = struct{}{}
		}
	}
	return references
}

func BenchmarkExtractMapReferences(b *testing.B) {
	for _, size := range []int{5_000, 50_000, 500_000} {
		b.Run(fmt.Sprintf("bytes=%d/no-references", size), func(b *testing.B) {
			config := strings.Repeat("backend route\n", size/len("backend route\n"))
			b.ReportAllocs()
			b.SetBytes(int64(len(config)))
			b.ResetTimer()
			for range b.N {
				if references := extractMapReferences(config); len(references) != 0 {
					b.Fatal(references)
				}
			}
		})
		b.Run(fmt.Sprintf("bytes=%d/one-reference", size), func(b *testing.B) {
			config := strings.Repeat("backend route\n", size/len("backend route\n")) +
				"map_str(maps/routes.map)\n"
			b.ReportAllocs()
			b.SetBytes(int64(len(config)))
			b.ResetTimer()
			for range b.N {
				references := extractMapReferences(config)
				if _, found := references["routes.map"]; !found || len(references) != 1 {
					b.Fatal(references)
				}
			}
		})
	}
}
