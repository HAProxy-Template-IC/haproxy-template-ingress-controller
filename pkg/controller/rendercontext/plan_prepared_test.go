// Copyright 2026 Philipp Hossner
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

package rendercontext

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPreparedPlanDeclarationsDetachTemplateRecords(t *testing.T) {
	profileLines := []string{" timeout connect 5s ", "# dropped"}
	keywordArgs := []string{"inter", "2s"}
	backendBody := []string{"    timeout server 5s"}
	backendComments := []string{"# route default/app"}
	profile, err := PreparePlanProfile(map[string]any{
		"mode":    "http",
		"profile": profileLines,
	})
	require.NoError(t, err)
	backend, err := PreparePlanBackend(map[string]any{
		"name": "be_app",
		"servers": []map[string]any{{
			"name":  "server-1",
			"extra": []map[string]any{{"name": "check", "args": keywordArgs}},
		}},
		"body":     backendBody,
		"comments": backendComments,
	}, "backend be_app\n")
	require.NoError(t, err)

	profileLines[0] = "poison"
	keywordArgs[0] = "poison"
	backendBody[0] = "poison"
	backendComments[0] = "poison"

	assert.Equal(t, []string{"mode http", "timeout connect 5s"}, profile.Body)
	assert.Equal(t, []string{"inter", "2s"}, backend.Backend.Servers[0].Extra[0].Args)
	assert.Equal(t, []string{"    timeout server 5s"}, backend.Body)
	assert.Equal(t, []string{"# route default/app"}, backend.Comments)
	require.NoError(t, profile.Validate())
	require.NoError(t, backend.Validate())
}

func TestPreparedPlanDeclarationsReplayWithFreshTokens(t *testing.T) {
	profile, err := PreparePlanProfile(map[string]any{"mode": "http"})
	require.NoError(t, err)
	backend, err := PreparePlanBackend(map[string]any{
		"name":    "be_app",
		"profile": profile.Name,
	}, "backend be_app from "+profile.Name+"\n")
	require.NoError(t, err)

	tokens := make([]string, 0, 2)
	configs := make([]string, 0, 2)
	for range 2 {
		registry := NewPlanRegistry(nil)
		name, registerErr := registry.RegisterPreparedProfile(profile)
		require.NoError(t, registerErr)
		assert.Equal(t, profile.Name, name)
		token, registerErr := registry.RegisterPreparedBackend(&backend)
		require.NoError(t, registerErr)
		config, _, assembleErr := registry.Assemble(
			context.Background(), registry.ProfileGroup()+token, nil,
		)
		require.NoError(t, assembleErr)
		tokens = append(tokens, token)
		configs = append(configs, config)
	}

	assert.NotEqual(t, tokens[0], tokens[1])
	assert.Equal(t, configs[0], configs[1])
	assert.NotContains(t, configs[0], "@haptic:")
}

func TestPreparedPlanDeclarationsRejectCorruption(t *testing.T) {
	profile, err := PreparePlanProfile(map[string]any{"mode": "http"})
	require.NoError(t, err)
	backend, err := PreparePlanBackend(map[string]any{
		"name": "be_app",
		"servers": []any{map[string]any{
			"name":  "server-1",
			"extra": []any{map[string]any{"name": "check", "args": []any{"inter", "2s"}}},
		}},
		"body": []any{"    timeout server 5s"},
	}, "backend be_app\n")
	require.NoError(t, err)

	profileTests := map[string]func(*PreparedPlanProfile){
		"name":   func(value *PreparedPlanProfile) { value.Name = "haptic-be-deadbeefdead" },
		"body":   func(value *PreparedPlanProfile) { value.Body[0] = "mode tcp" },
		"text":   func(value *PreparedPlanProfile) { value.Text += "    retries 2\n" },
		"digest": func(value *PreparedPlanProfile) { value.Digest = "0000000000000000" },
	}
	for name, poison := range profileTests {
		t.Run("profile "+name, func(t *testing.T) {
			value := profile.Clone()
			poison(&value)
			require.Error(t, value.Validate())
			_, registerErr := NewPlanRegistry(nil).RegisterPreparedProfile(value)
			require.Error(t, registerErr)
		})
	}

	backendTests := map[string]func(*PreparedPlanBackend){
		"identity": func(value *PreparedPlanBackend) { value.Backend.Name = "be_other" },
		"record":   func(value *PreparedPlanBackend) { value.Backend.Mode = "tcp" },
		"body":     func(value *PreparedPlanBackend) { value.Body[0] = "    timeout server 9s" },
		"text":     func(value *PreparedPlanBackend) { value.Text += "    retries 2\n" },
		"nested":   func(value *PreparedPlanBackend) { value.Backend.Servers[0].Extra[0].Args[0] = "poison" },
		"assembled": func(value *PreparedPlanBackend) {
			value.Backend.TextDigest = "0000000000000000"
		},
		"digest": func(value *PreparedPlanBackend) { value.Digest = "0000000000000000" },
	}
	for name, poison := range backendTests {
		t.Run("backend "+name, func(t *testing.T) {
			value := backend.Clone()
			poison(&value)
			require.Error(t, value.Validate())
			_, registerErr := NewPlanRegistry(nil).RegisterPreparedBackend(&value)
			require.Error(t, registerErr)
		})
	}
}

func TestRegisterPreparedBackendKeepsStrictDuplicateChecks(t *testing.T) {
	first, err := PreparePlanBackend(map[string]any{"name": "be_app", "mode": "http"}, "backend be_app\n")
	require.NoError(t, err)
	differentRecord, err := PreparePlanBackend(map[string]any{"name": "be_app", "mode": "tcp"}, "backend be_app\n")
	require.NoError(t, err)
	differentText, err := PreparePlanBackend(map[string]any{"name": "be_app", "mode": "http"}, "backend be_app\n    mode http\n")
	require.NoError(t, err)
	registry := NewPlanRegistry(nil)

	firstToken, err := registry.RegisterPreparedBackend(&first)
	require.NoError(t, err)
	repeatToken, err := registry.RegisterPreparedBackend(&first)
	require.NoError(t, err)
	assert.Equal(t, firstToken, repeatToken)
	_, err = registry.RegisterPreparedBackend(&differentRecord)
	require.ErrorContains(t, err, "different values")
	_, err = registry.RegisterPreparedBackend(&differentText)
	require.ErrorContains(t, err, "different text")
}
