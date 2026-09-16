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

package server_test

import (
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/agent/api"
)

func TestApplyManifestSizeBoundary(t *testing.T) {
	for _, suffix := range []string{"", " ", "{}"} {
		t.Run("suffix="+suffix, func(t *testing.T) {
			h := newHarness(t)
			firstApply(t, h)
			before := h.state(false)
			manifest := buildManifest("plan-2", baseFiles("global\n"))
			manifest.Mode = api.ModeReload
			manifest.ExpectedPrevPlanID = before.AppliedPlanID
			manifest.ExpectedPrevToken = before.AppliedToken
			h.prepareExactManifest(&manifest)
			raw, err := json.Marshal(manifest)
			require.NoError(t, err)
			raw = append(raw, bytes.Repeat([]byte(" "), api.MaxPlanBlobBytes-len(raw))...)
			raw = append(raw, suffix...)

			status, answer := postManifestBytes(t, h, raw)
			if suffix == "" {
				require.Equal(t, http.StatusOK, status, string(answer))
				result := api.ApplyResult{}
				require.NoError(t, json.Unmarshal(answer, &result))
				assert.True(t, result.OK, "%+v", result.Error)
				return
			}
			require.Equal(t, http.StatusBadRequest, status, string(answer))
			assert.Contains(t, string(answer), "manifest exceeds")
			after := h.state(false)
			assert.Equal(t, before.Generation, after.Generation)
			assert.Equal(t, before.AppliedPlanID, after.AppliedPlanID)
			assert.Equal(t, before.LKGPlanID, after.LKGPlanID)
			assert.Equal(t, "global\n", h.read(configPath))
		})
	}
}

func postManifestBytes(t *testing.T, h *harness, manifest []byte) (status int, answer []byte) {
	t.Helper()
	var payload bytes.Buffer
	writer := multipart.NewWriter(&payload)
	part, err := writer.CreateFormField(api.PartManifest)
	require.NoError(t, err)
	_, err = part.Write(manifest)
	require.NoError(t, err)
	require.NoError(t, writer.Close())

	request, err := http.NewRequestWithContext(t.Context(), http.MethodPost, h.url+api.PathApply, &payload)
	require.NoError(t, err)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	request.SetBasicAuth(testUser, testPassword)
	response, err := h.client.Do(request)
	require.NoError(t, err)
	defer response.Body.Close()
	answer, err = io.ReadAll(response.Body)
	require.NoError(t, err)
	return response.StatusCode, answer
}
