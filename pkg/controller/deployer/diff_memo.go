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

package deployer

import (
	"sync"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/deployplan"
)

// diffKey is everything a decision depends on besides the render: two pods
// reporting the same key get the same ops, so the diff runs once for them.
//
// Every field the diff branches on has to be in here. The pending-delete counts
// are: a pod at the deferral cap gets a planned reload where another composes
// the delete batch, and handing it the other pod's ops makes its agent refuse
// the batch and fall back to a reload it did not plan. The inventory is keyed
// by content for the same reason — its generation is a per-pod counter, so
// equal counters say nothing about equal content.
type diffKey struct {
	applied         string
	running         string
	workerOps       string
	caps            string
	inventory       string
	pendingServers  int
	pendingBackends int
	reloadPending   bool
}

// diffMemo shares one deployment's decisions across its pods. It lives for one
// deployment only — the next one diffs a different render.
type diffMemo struct {
	mu      sync.Mutex
	answers map[diffKey]deployplan.Decision
}

func newDiffMemo() *diffMemo {
	return &diffMemo{answers: map[diffKey]deployplan.Decision{}}
}

// get returns the decision for this key, computing it on first sight. The
// answer is read-only: every pod that shares the key shares the slices in it.
func (m *diffMemo) get(key *diffKey, compute func() deployplan.Decision) deployplan.Decision {
	m.mu.Lock()
	defer m.mu.Unlock()
	if decision, known := m.answers[*key]; known {
		return decision
	}
	decision := compute()
	m.answers[*key] = decision
	return decision
}
