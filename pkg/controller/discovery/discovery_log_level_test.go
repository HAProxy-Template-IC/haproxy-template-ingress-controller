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

package discovery

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/testutil"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane"
)

const discoveredMessage = "Discovered HAProxy pods"

// loggedLevel is the level discoveredMessage was written at. The logger is at
// LevelDebug so the quiet path is captured too — at the default level a Debug
// line is simply absent, which no assertion could tell apart from a regression
// that stopped logging altogether.
func loggedLevel(t *testing.T, logs *bytes.Buffer) string {
	t.Helper()
	for _, line := range bytes.Split(bytes.TrimSpace(logs.Bytes()), []byte("\n")) {
		var record struct {
			Level string `json:"level"`
			Msg   string `json:"msg"`
		}
		require.NoError(t, json.Unmarshal(line, &record))
		if record.Msg == discoveredMessage {
			return record.Level
		}
	}
	require.FailNowf(t, "message never logged", "%q", discoveredMessage)
	return ""
}

func testEndpoint(podName string) *dataplane.Endpoint {
	return &dataplane.Endpoint{
		PodNamespace: "default",
		PodName:      podName,
		PodUID:       podName + "-uid",
		URL:          "http://127.0.0.1:5555",
	}
}

// componentWithCapturedLog builds a Component logging into the returned
// buffer, seeded with the endpoints a previous pass already published.
func componentWithCapturedLog(t *testing.T, previous []*dataplane.Endpoint) (*Component, *bytes.Buffer) {
	t.Helper()
	logs := &bytes.Buffer{}
	logger := slog.New(slog.NewJSONHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	c := New(testutil.NewTestBus(), logger)
	for _, endpoint := range previous {
		identity := podIdentity{podNamespace: endpoint.PodNamespace, podName: endpoint.PodName}
		c.lastEndpoints[identity] = endpointAuthorityOf(endpoint)
	}
	return c, logs
}

// An unchanged fleet re-reports the same numbers on every drift-prevention
// tick. At Info that buries every other line the leader writes.
func TestSteadyFleetDiscoveryLogsAtDebug(t *testing.T) {
	fleet := []*dataplane.Endpoint{testEndpoint("haproxy-a"), testEndpoint("haproxy-b")}
	c, logs := componentWithCapturedLog(t, fleet)

	c.publishDiscoveryResult("drift_prevention", len(fleet), fleet, nil)

	assert.Equal(t, "DEBUG", loggedLevel(t, logs))
}

// A count that moved is the event the message exists to report.
func TestChangedFleetDiscoveryLogsAtInfo(t *testing.T) {
	c, logs := componentWithCapturedLog(t, []*dataplane.Endpoint{testEndpoint("haproxy-a")})

	grown := []*dataplane.Endpoint{testEndpoint("haproxy-a"), testEndpoint("haproxy-b")}
	c.publishDiscoveryResult("resource_index_updated", len(grown), grown, nil)

	assert.Equal(t, "INFO", loggedLevel(t, logs))
}

// Admitting nothing means there is no pod left to deploy to. The condition
// used to read len(admitted) > 0, which put exactly this case — and only this
// case — on the quiet path.
func TestEmptyFleetDiscoveryLogsAtInfo(t *testing.T) {
	c, logs := componentWithCapturedLog(t, nil)

	c.publishDiscoveryResult("drift_prevention", 2, nil, nil)

	assert.Equal(t, "INFO", loggedLevel(t, logs))
}
