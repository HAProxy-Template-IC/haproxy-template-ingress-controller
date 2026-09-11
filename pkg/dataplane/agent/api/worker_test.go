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

package api_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/agent/api"
)

func TestWorkerIdentity(t *testing.T) {
	worker := api.HAProxyInfo{WorkerPID: 1000, WorkerStartTimeUnixMicros: 1_700_000_000_000_000}
	tests := []struct {
		name     string
		info     api.HAProxyInfo
		complete bool
		same     bool
	}{
		{name: "same worker", info: worker, complete: true, same: true},
		{name: "missing identity"},
		{name: "missing process", info: api.HAProxyInfo{WorkerStartTimeUnixMicros: worker.WorkerStartTimeUnixMicros}},
		{name: "missing start", info: api.HAProxyInfo{WorkerPID: worker.WorkerPID}},
		{name: "negative process", info: api.HAProxyInfo{WorkerPID: -1, WorkerStartTimeUnixMicros: worker.WorkerStartTimeUnixMicros}},
		{name: "negative start", info: api.HAProxyInfo{WorkerPID: worker.WorkerPID, WorkerStartTimeUnixMicros: -1}},
		{name: "reused process", info: api.HAProxyInfo{WorkerPID: worker.WorkerPID, WorkerStartTimeUnixMicros: worker.WorkerStartTimeUnixMicros + 1}, complete: true},
		{name: "different process", info: api.HAProxyInfo{WorkerPID: worker.WorkerPID + 1, WorkerStartTimeUnixMicros: worker.WorkerStartTimeUnixMicros}, complete: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.complete, tt.info.HasWorkerIdentity())
			assert.Equal(t, tt.complete, tt.info.SameWorker(tt.info))
			assert.Equal(t, tt.same, worker.SameWorker(tt.info))
			assert.Equal(t, tt.same, tt.info.SameWorker(worker))
		})
	}
}
