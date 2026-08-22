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

package templating

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestGoRenderIndexLimit_SurfacesInHaptic proves the scriggo fork's guard
// against parallel-render index truncation (issue #169) reaches HAPTIC. The
// "go render" operand is a single byte; a template referencing more than 256
// distinct render targets in one function must fail to compile with a limit
// error instead of silently wrapping the index and rendering the wrong body.
func TestGoRenderIndexLimit_SurfacesInHaptic(t *testing.T) {
	const overLimit = 300 // > scriggo's maxScriggoFunctionsCount (256)

	templates := make(map[string]string, overLimit+1)
	var body strings.Builder
	for i := 0; i < overLimit; i++ {
		name := fmt.Sprintf("p%d.txt", i)
		templates[name] = fmt.Sprintf("<%d>", i)
		fmt.Fprintf(&body, `{{ go render %q }}`, name)
	}
	templates["index.txt"] = body.String()

	_, err := New(templates, &Options{EntryPoints: []string{"index.txt"}})
	require.Error(t, err, "expected a build-time limit error for >256 distinct go-render targets, not silent index truncation")
	require.Contains(t, err.Error(), "Scriggo functions count exceeded",
		"expected the scriggo functions-count limit error to surface")
}

// TestGoRenderBelowLimit_SurfacesInHaptic confirms the guard leaves the normal
// parallel-render path intact: distinct targets below the limit compile and each
// site renders its own target's body.
func TestGoRenderBelowLimit_SurfacesInHaptic(t *testing.T) {
	const n = 10

	templates := make(map[string]string, n+1)
	var body strings.Builder
	for i := 0; i < n; i++ {
		name := fmt.Sprintf("p%d.txt", i)
		templates[name] = fmt.Sprintf("<%d>", i)
		fmt.Fprintf(&body, `{{ go render %q }}`, name)
	}
	templates["index.txt"] = body.String()

	engine, err := New(templates, &Options{EntryPoints: []string{"index.txt"}})
	require.NoError(t, err)

	out, err := engine.Render(context.Background(), "index.txt", nil)
	require.NoError(t, err)
	for i := 0; i < n; i++ {
		token := fmt.Sprintf("<%d>", i)
		require.Equal(t, 1, strings.Count(out, token),
			"target %s should render exactly once", token)
	}
}
