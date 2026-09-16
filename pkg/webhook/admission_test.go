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

package webhook

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	admissionv1 "k8s.io/api/admission/v1"
)

func TestAdmissionRejectsInvalidEnvelope(t *testing.T) {
	server := newTestServer(t, &ServerConfig{})
	calls := 0
	server.RegisterValidator("v1.ConfigMap", func(*ValidationContext) (bool, string, []string, error) {
		calls++
		return true, "", nil, nil
	})
	for _, body := range []string{
		`{"apiVersion":"admission.k8s.io/v1","kind":"AdmissionReview"}`,
		`{"apiVersion":"admission.k8s.io/v1","kind":"AdmissionReview","request":null}`,
		`{"apiVersion":"admission.k8s.io/v1","kind":"AdmissionReview","request":{"kind":{"version":"v1","kind":"ConfigMap"}}}`,
		`{"apiVersion":"admission.k8s.io/v1","kind":"AdmissionReview","response":{"uid":"u","allowed":true}}`,
		`{"apiVersion":"admission.k8s.io/v1beta1","kind":"AdmissionReview","request":{"uid":"u","kind":{"version":"v1","kind":"ConfigMap"}}}`,
		`{"apiVersion":"admission.k8s.io/v1","kind":"AdmissionReviewList","request":{"uid":"u","kind":{"version":"v1","kind":"ConfigMap"}}}`,
		`null`,
	} {
		t.Run(body, func(t *testing.T) {
			w := httptest.NewRecorder()
			r := httptest.NewRequest(http.MethodPost, "/validate", strings.NewReader(body))
			require.NotPanics(t, func() { server.handleValidation(w, r) })
			assert.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
		})
	}
	assert.Zero(t, calls)
}

func TestAdmissionResponseOmitsRequest(t *testing.T) {
	server := newTestServer(t, &ServerConfig{})
	server.RegisterValidator("v1.ConfigMap", func(*ValidationContext) (bool, string, []string, error) {
		return true, "", []string{"warning"}, nil
	})
	w, r := configMapAdmissionRequest(t)
	server.handleValidation(w, r)

	require.Equal(t, http.StatusOK, w.Code)
	var response admissionv1.AdmissionReview
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))
	assert.Equal(t, "admission.k8s.io/v1", response.APIVersion)
	assert.Equal(t, "AdmissionReview", response.Kind)
	assert.Nil(t, response.Request)
	require.NotNil(t, response.Response)
	assert.Equal(t, "test-uid", string(response.Response.UID))
	assert.True(t, response.Response.Allowed)
	assert.Equal(t, []string{"warning"}, response.Response.Warnings)
}

func TestAdmissionSizeBoundary(t *testing.T) {
	server := newTestServer(t, &ServerConfig{})
	calls := 0
	server.RegisterValidator("v1.ConfigMap", func(*ValidationContext) (bool, string, []string, error) {
		calls++
		return true, "", nil, nil
	})
	const limit = 16 << 20
	for _, size := range []int{limit - 1, limit, limit + 1} {
		for _, knownLength := range []bool{false, true} {
			t.Run(fmt.Sprintf("size=%d/known=%t", size, knownLength), func(t *testing.T) {
				w, r := configMapAdmissionRequest(t)
				body, err := io.ReadAll(r.Body)
				require.NoError(t, err)
				r.Body = io.NopCloser(io.MultiReader(bytes.NewReader(body), strings.NewReader(strings.Repeat(" ", size-len(body)))))
				r.ContentLength = -1
				if knownLength {
					r.ContentLength = int64(size)
				}
				before := calls
				server.handleValidation(w, r)
				if size > limit {
					assert.Equal(t, http.StatusRequestEntityTooLarge, w.Code, w.Body.String())
					assert.Equal(t, before, calls)
					return
				}
				assert.Equal(t, http.StatusOK, w.Code, w.Body.String())
				assert.Equal(t, before+1, calls)
			})
		}
	}
}

func TestNewServerPreservesCallerConfig(t *testing.T) {
	cert, key, err := generateTestCertificates()
	require.NoError(t, err)
	config := ServerConfig{CertPEM: cert, KeyPEM: key}
	before := config
	server, err := NewServer(&config)
	require.NoError(t, err)
	assert.Equal(t, before, config)
	assert.Equal(t, 9443, server.config.Port)
}

func TestNewServerRejectsMissingConfig(t *testing.T) {
	server, err := NewServer(nil)
	require.ErrorContains(t, err, "configuration is required")
	assert.Nil(t, server)
}
