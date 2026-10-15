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

import "sync/atomic"

// backgroundWorkerGate keeps a CPU free for every admission render: a
// background render's batches run their first worker unconditionally and
// take an extra one only while cpus-1-admissions are not all in use. It
// caps rather than pauses background renders, so a stream of admissions
// slows reconciles instead of stalling them (#304).
type backgroundWorkerGate struct {
	cpus       int
	admissions atomic.Int32
	extra      atomic.Int32
}

func newBackgroundWorkerGate(cpus int) *backgroundWorkerGate {
	return &backgroundWorkerGate{cpus: cpus}
}

// TryAcquire implements templating.WorkerGate.
func (g *backgroundWorkerGate) TryAcquire() bool {
	for {
		extra := g.extra.Load()
		if int(extra) >= g.cpus-1-int(g.admissions.Load()) {
			return false
		}
		if g.extra.CompareAndSwap(extra, extra+1) {
			return true
		}
	}
}

// Release implements templating.WorkerGate.
func (g *backgroundWorkerGate) Release() {
	g.extra.Add(-1)
}

// admissionRendering reserves a CPU until the returned func is called.
func (g *backgroundWorkerGate) admissionRendering() func() {
	g.admissions.Add(1)
	return func() { g.admissions.Add(-1) }
}
