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

package executors

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/client"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/client/testutil"
)

// findRepoFile walks up from the test's working directory until it finds the
// named file. Fails the test if not found. Used to locate /versions.env from
// any package's test run.
func findRepoFile(t *testing.T, name string) string {
	t.Helper()
	dir, err := os.Getwd()
	require.NoError(t, err)
	for {
		p := filepath.Join(dir, name)
		if _, err := os.Stat(p); err == nil {
			return p
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	t.Fatalf("could not find %s walking up from CWD", name)
	return ""
}

var (
	supportedVersionsOnce sync.Once
	supportedVersionsList []string
)

// supportedVersionStrings returns one mock /v3/info version string per HAProxy
// version declared in /versions.env (4 community + 3 enterprise today). The
// list is parsed once and cached for the test binary's lifetime.
//
// The list is sourced from /versions.env so the test matrix stays in sync as
// new HAProxy versions are added or enterprise builds are released. The file
// format is a shell-sourceable env: `HAPROXY_VERSIONS="3.0 3.1 3.2 3.3"` and
// one `HAPROXY_ENTERPRISE_XX="3.Xr1"` line per enterprise version.
func supportedVersionStrings(t *testing.T) []string {
	t.Helper()
	supportedVersionsOnce.Do(func() {
		data, err := os.ReadFile(findRepoFile(t, "versions.env"))
		require.NoError(t, err)
		community, enterprise := parseVersionsEnv(string(data))
		supportedVersionsList = buildMockVersionStrings(community, enterprise)
		if len(supportedVersionsList) == 0 {
			t.Fatal("no versions parsed from versions.env")
		}
	})
	return supportedVersionsList
}

// parseVersionsEnv extracts the community and enterprise version lists from the
// shell-sourceable versions.env content.
func parseVersionsEnv(content string) (community, enterprise []string) {
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, val, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		val = strings.Trim(val, `"`)
		switch {
		case key == "HAPROXY_VERSIONS":
			community = strings.Fields(val)
		case strings.HasPrefix(key, "HAPROXY_ENTERPRISE_"):
			enterprise = append(enterprise, val)
		}
	}
	return community, enterprise
}

// buildMockVersionStrings turns parsed version lists into mock /v3/info strings.
// Community "3.2" -> "v3.2.0 testhash"; enterprise "3.0r1" -> "v3.0.0-ee1".
// IsEnterpriseVersion accepts the -ee form, keeping ParseVersion on the decimal path.
func buildMockVersionStrings(community, enterprise []string) []string {
	out := make([]string, 0, len(community)+len(enterprise))
	for _, v := range community {
		out = append(out, "v"+v+".0 testhash")
	}
	for _, v := range enterprise {
		before, _, _ := strings.Cut(v, "r")
		out = append(out, fmt.Sprintf("v%s.0-ee1", before))
	}
	return out
}

// runAcrossVersions executes body as a subtest for each supported API version.
// Each subtest spins up its own mock server reporting that version from /v3/info
// so the DataPlane API client detects and dispatches to the corresponding branch.
//
// Uses testutil.NewMockEnterpriseServer for all versions — the enterprise mock is a
// superset of the community mock that additionally matches requests regardless of
// whether handler keys include the "/v3" prefix (the actual path the generated clients
// emit is "/services/..." with no /v3 prefix; the handler maps in existing tests use
// the "/v3/services/..." form for readability, and this fallback keeps them working).
// The detected edition is still driven by APIVersion, not the mock helper choice.
func runAcrossVersions(t *testing.T, handlers map[string]http.HandlerFunc,
	body func(t *testing.T, c *client.DataplaneClient)) {
	t.Helper()
	for _, version := range supportedVersionStrings(t) {
		t.Run(version, func(t *testing.T) {
			t.Helper()
			server := testutil.NewMockEnterpriseServer(t, testutil.MockServerConfig{
				APIVersion: version,
				Handlers:   handlers,
			})
			defer server.Close()
			body(t, testutil.NewTestClient(t, server))
		})
	}
}
