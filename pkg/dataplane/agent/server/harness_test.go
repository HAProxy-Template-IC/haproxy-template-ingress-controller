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
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/agent/agenttest"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/agent/api"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/agent/server"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/renderplan"
)

const (
	testUser     = "haptic"
	testPassword = "s3cret"
	configPath   = "haproxy.cfg"
)

// harness runs a real agent against the HAProxy model over a loopback socket.
type harness struct {
	t        *testing.T
	agent    *server.Server
	model    *agenttest.HAProxy
	baseDir  string
	registry *prometheus.Registry
	url      string
	client   *http.Client
	stopOnce func()
}

type options struct {
	reloadIntervalMin time.Duration
	baseDir           string
	model             *agenttest.HAProxy
	statePrepare      func(baseDir string)
}

func newHarness(t *testing.T, opts ...func(*options)) *harness {
	t.Helper()
	settings := options{baseDir: t.TempDir()}
	for _, opt := range opts {
		opt(&settings)
	}
	if settings.statePrepare != nil {
		settings.statePrepare(settings.baseDir)
	}
	model := settings.model
	if model == nil {
		model = agenttest.Start(t)
	}
	registry := prometheus.NewRegistry()
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)

	agent, err := server.New(ctx, server.Config{
		BaseDir:           settings.baseDir,
		ConfigFile:        configPath,
		MasterSocket:      model.MasterSocket(),
		WorkerSocket:      model.WorkerSocket(),
		StateFile:         ".haptic-agent.json",
		Listen:            "127.0.0.1:0",
		ReloadIntervalMin: settings.reloadIntervalMin,
		Username:          testUser,
		Password:          testPassword,
		AgentVersion:      "test",
		Logger:            slog.New(slog.DiscardHandler),
		Registry:          registry,
	})
	require.NoError(t, err)

	done := make(chan error, 1)
	go func() { done <- agent.Start(ctx) }()
	stop := sync.OnceFunc(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			t.Error("the agent did not shut down")
		}
	})
	t.Cleanup(stop)

	h := &harness{
		t: t, agent: agent, model: model, baseDir: settings.baseDir,
		registry: registry, client: &http.Client{Timeout: 30 * time.Second},
		stopOnce: stop,
	}
	require.Eventually(t, func() bool { return agent.Ready() }, 10*time.Second, 10*time.Millisecond)
	h.url = "http://" + agent.Addr()
	return h
}

func withReloadInterval(d time.Duration) func(*options) {
	return func(o *options) { o.reloadIntervalMin = d }
}

func withBaseDir(dir string) func(*options) {
	return func(o *options) { o.baseDir = dir }
}

func withModel(model *agenttest.HAProxy) func(*options) {
	return func(o *options) { o.model = model }
}

// withState writes a state file before the agent starts, which is how the
// crash matrix puts the agent back on an interrupted apply.
func withState(prepare func(baseDir string)) func(*options) {
	return func(o *options) { o.statePrepare = prepare }
}

// stop shuts the agent down so a second one can take over the same tree.
func (h *harness) stop() {
	h.t.Helper()
	h.stopOnce()
}

// restart replaces the agent with a fresh one on the same tree and the same
// HAProxy, which is what a container restart of the agent alone looks like.
func (h *harness) restart() *harness {
	h.t.Helper()
	h.stop()
	return newHarness(h.t, withBaseDir(h.baseDir), withModel(h.model))
}

// file is one desired file of a test manifest, content included.
type file struct {
	Path    string
	Content string
	Kind    string
	Reload  bool
}

// buildManifest turns the test's files into the wire manifest.
func buildManifest(planID string, list []file) api.Manifest {
	m := api.Manifest{PlanID: planID, PlanSchemaVersion: 1, Mode: api.ModeAuto}
	for _, f := range list {
		kind := f.Kind
		if kind == "" {
			kind = api.FileKindMap
		}
		if f.Path == configPath {
			kind = api.FileKindConfig
		}
		m.Files = append(m.Files, api.File{
			Path:           f.Path,
			Digest:         renderplan.DigestString(f.Content),
			Size:           int64(len(f.Content)),
			Kind:           kind,
			ReloadOnChange: f.Reload,
		})
	}
	return m
}

// post sends an apply. Every file of list travels as a part unless omit names
// it, which is how the 409-missing path is exercised.
func (h *harness) post(m api.Manifest, list []file, omit ...string) (*http.Response, []byte) {
	h.t.Helper()
	skip := map[string]struct{}{}
	for _, path := range omit {
		skip[path] = struct{}{}
	}
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	manifestPart, err := writer.CreateFormField(api.PartManifest)
	require.NoError(h.t, err)
	require.NoError(h.t, json.NewEncoder(manifestPart).Encode(m))
	for _, f := range list {
		if _, omitted := skip[f.Path]; omitted {
			continue
		}
		part, err := writer.CreateFormFile(f.Path, f.Path)
		require.NoError(h.t, err)
		_, err = part.Write([]byte(f.Content))
		require.NoError(h.t, err)
	}
	require.NoError(h.t, writer.Close())

	request, err := http.NewRequestWithContext(h.t.Context(), http.MethodPost, h.url+api.PathApply, body)
	require.NoError(h.t, err)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	request.SetBasicAuth(testUser, testPassword)
	response, err := h.client.Do(request)
	require.NoError(h.t, err)
	defer func() { _ = response.Body.Close() }()
	raw, err := io.ReadAll(response.Body)
	require.NoError(h.t, err)
	return response, raw
}

// apply posts and requires a 200, returning the ACK.
func (h *harness) apply(m api.Manifest, list []file) api.ApplyResult {
	h.t.Helper()
	response, raw := h.post(m, list)
	require.Equal(h.t, http.StatusOK, response.StatusCode, string(raw))
	result := api.ApplyResult{}
	require.NoError(h.t, json.Unmarshal(raw, &result))
	return result
}

func (h *harness) state(verify bool) api.State {
	h.t.Helper()
	url := h.url + api.PathState
	if verify {
		url += "?verify=1"
	}
	request, err := http.NewRequestWithContext(h.t.Context(), http.MethodGet, url, nil)
	require.NoError(h.t, err)
	request.SetBasicAuth(testUser, testPassword)
	response, err := h.client.Do(request)
	require.NoError(h.t, err)
	defer func() { _ = response.Body.Close() }()
	require.Equal(h.t, http.StatusOK, response.StatusCode)
	state := api.State{}
	require.NoError(h.t, json.NewDecoder(response.Body).Decode(&state))
	return state
}

func (h *harness) read(path string) string {
	h.t.Helper()
	raw, err := os.ReadFile(filepath.Join(h.baseDir, path))
	require.NoError(h.t, err)
	return string(raw)
}

func (h *harness) exists(path string) bool {
	h.t.Helper()
	_, err := os.Lstat(filepath.Join(h.baseDir, path))
	return err == nil
}

// tree lists the manifest-owned files on disk, so a test can assert that the
// set is the desired one and not a mix.
func (h *harness) tree() map[string]string {
	h.t.Helper()
	out := map[string]string{}
	entries, err := os.ReadDir(h.baseDir)
	require.NoError(h.t, err)
	for _, entry := range entries {
		if entry.IsDir() || entry.Name()[0] == '.' {
			continue
		}
		out[entry.Name()] = h.read(entry.Name())
	}
	return out
}

func (h *harness) metric(name string, labels ...string) float64 {
	h.t.Helper()
	families, err := h.registry.Gather()
	require.NoError(h.t, err)
	total := 0.0
	for _, family := range families {
		if family.GetName() != name {
			continue
		}
		for _, metric := range family.GetMetric() {
			if !matchesLabels(metric, labels) {
				continue
			}
			total += metric.GetCounter().GetValue() + metric.GetGauge().GetValue()
		}
	}
	return total
}

func matchesLabels(metric *dto.Metric, wanted []string) bool {
	if len(wanted) == 0 {
		return true
	}
	got := make([]string, 0, len(metric.GetLabel()))
	for _, pair := range metric.GetLabel() {
		got = append(got, pair.GetValue())
	}
	sort.Strings(got)
	sort.Strings(wanted)
	if len(got) != len(wanted) {
		return false
	}
	for i := range got {
		if got[i] != wanted[i] {
			return false
		}
	}
	return true
}

// writeFile changes the tree behind the agent's back, which is how the tests
// stand in for a container restart that copied the bootstrap files.
func writeFile(h *harness, path, content string) error {
	return os.WriteFile(filepath.Join(h.baseDir, path), []byte(content), 0o600)
}
