// Copyright 2026 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package renderer

import (
	"runtime"
	"strings"
	"testing"
	"weak"

	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/rendercontext"
)

func TestIncrementalSourceTransactionReleasesSessionAfterNativeClosure(t *testing.T) {
	for _, commit := range []bool{true, false} {
		name := "abort"
		if commit {
			name = "commit"
		}
		t.Run(name, func(t *testing.T) {
			cfg := incrementalSourceTransactionSharedConfig()
			snippet := cfg.TemplateSnippets["200-consumer"]
			snippet.Template = `{%%
var matches = []any{item} | filter(func(value any) bool { return (value | dig_string("", "metadata", "name")) != "" })
show len(matches)
%%}`
			cfg.TemplateSnippets["200-consumer"] = snippet
			service, engine := newIncrementalSourceTransactionTestService(t, cfg, true)
			provider := incrementalSourceTransactionTestProvider(t)
			result, session := func() (*RenderResult, weak.Pointer[incrementalRenderSession]) {
				result, err := service.Render(t.Context(), provider, rendercontext.RenderModeReconcile)
				require.NoError(t, err)
				transaction, ok := result.InputTransaction.(*combinedRenderInputTransaction)
				require.True(t, ok)
				require.NotNil(t, transaction.incremental)
				session := weak.Make(transaction.incremental)
				if commit {
					require.NoError(t, result.InputTransaction.Commit(t.Context()))
					waitForIncrementalCache(t, service)
				} else {
					result.InputTransaction.Abort()
				}
				service.incremental.cache.wg.Wait()
				return result, session
			}()
			require.Equal(t, "1", strings.TrimSpace(result.HAProxyConfig))
			require.Positive(t, engine.sourceCalls.Load())
			runtime.GC()
			require.True(t, session.Value() == nil, "finished render session is still retained")
			runtime.KeepAlive(service)
			runtime.KeepAlive(provider)
			runtime.KeepAlive(result)
		})
	}
}
