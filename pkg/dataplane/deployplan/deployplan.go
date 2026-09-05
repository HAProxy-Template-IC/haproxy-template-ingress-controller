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

// Package deployplan decides what one pod has to do to reach a render: run
// runtime ops, write files only, or reload. It compares two renderplan.Plans
// and composes typed agent ops; it is pure data in, data out — no I/O, no
// controller imports and no HAProxy configuration parsing — so every rule is
// table tested and the playground runs the same code in a browser.
package deployplan

import (
	"slices"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/agent/api"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/renderplan"
)

// Verdict is what an apply built from a Decision does to the pod.
type Verdict string

// The three outcomes of a diff.
const (
	VerdictRuntime  Verdict = "runtime"
	VerdictFileOnly Verdict = "file_only"
	VerdictReload   Verdict = "reload"
)

const (
	// MaxChunks is the number of fenced applies one decision may be split into.
	MaxChunks = 8
	// MaxReasons caps the reason list, which ships in status and logs.
	MaxReasons = 32
	// removableTimeoutMs is the wait budget for one server or backend removal.
	removableTimeoutMs = 2000
)

// Baseline is what the controller knows about one pod, every field sourced
// from that pod's ACK or /v1/state.
type Baseline struct {
	Applied   *renderplan.Plan // the plan the pod ACKed; nil means unknown
	Running   *renderplan.Plan // what the worker runs; equals Applied without a pending reload
	WorkerOps *renderplan.Plan // Running plus the in-place ops accepted since
	Inventory api.Inventory    // maps, certs, CA and crt-list files the worker loaded
	Caps      Caps

	PendingServerDeletes  int
	PendingBackendDeletes int
	ReloadPending         bool
}

// Decision is the apply one pod gets. Ops is empty unless Verdict is runtime;
// Files is always the complete desired set. Reasons name every change that was
// not applied at runtime, whether it forced a reload or only a file write.
type Decision struct {
	Verdict Verdict  `json:"verdict"`
	Ops     []api.Op `json:"ops,omitempty"`
	InPlace []api.Op `json:"in_place_ops,omitempty"` // executable while a reload is pending; empty otherwise
	// WorkerPlan is what the worker holds once InPlace ran; its ID is the
	// pod's next worker-ops baseline. Set exactly when InPlace is.
	WorkerPlan *renderplan.Plan `json:"-"`
	Chunks     int              `json:"chunks,omitempty"` // applies Ops is split into, >1 only past api.MaxOpsPerApply
	Reasons    []string         `json:"reasons,omitempty"`
	Files      []api.File       `json:"files"`
	Mode       string           `json:"mode"` // api.ModeAuto or api.ModeReload
}

// composedOps are every op kind Diff can put in a Decision. shutdown_sessions
// is absent on purpose: the agent issues it from its own deferred-delete retry,
// never from a composed batch.
var composedOps = []string{
	api.OpBackendAdd,
	api.OpBackendPublish,
	api.OpBackendUnpublish,
	api.OpBackendDel,
	api.OpBackendWaitRemovable,
	api.OpServerAdd,
	api.OpServerEnable,
	api.OpServerDisable,
	api.OpServerSetAddr,
	api.OpServerSetWeight,
	api.OpServerSetState,
	api.OpServerWaitRemovable,
	api.OpServerDel,
	api.OpMapAdd,
	api.OpMapSet,
	api.OpMapDel,
	api.OpMapReplace,
	api.OpCertSet,
	api.OpCertNew,
	api.OpCASet,
	api.OpCANew,
	api.OpCRTListAdd,
	api.OpCRTListDel,
}

// ComposedOps returns the op kinds this controller composes — the set an agent
// is measured against before it is sent anything.
func ComposedOps() []string {
	return slices.Clone(composedOps)
}

// Chunk splits Ops into the applies the deployer sends, each an ordered prefix
// of the remaining ops. The first apply carries the in-place batch as well and
// the cap is on their sum, so that batch comes out of its budget — an apply
// over the cap is refused before it is sent, and reaches no pod at all.
func (d *Decision) Chunk() [][]api.Op {
	if len(d.Ops) == 0 {
		return nil
	}
	chunks := make([][]api.Op, 0, chunkCount(len(d.Ops), len(d.InPlace)))
	budget := max(api.MaxOpsPerApply-len(d.InPlace), 0)
	for start := 0; start < len(d.Ops); {
		end := min(start+budget, len(d.Ops))
		chunks = append(chunks, d.Ops[start:end])
		start = end
		budget = api.MaxOpsPerApply
	}
	return chunks
}

// Files projects a plan's file set onto the wire type.
func Files(p *renderplan.Plan) []api.File {
	if p == nil {
		return nil
	}
	files := make([]api.File, 0, len(p.Files))
	for i := range p.Files {
		f := &p.Files[i]
		files = append(files, api.File{
			Path:   f.Path,
			Digest: f.Digest,
			// The witness the agent stores and compares on the next apply. It
			// is the content digest, but it answers a different question than
			// Digest does: Digest asks whether the bytes the agent holds match
			// what this render asserts, Proof asks whether this render asserts
			// what the last one did. A file with no proof always ships bytes.
			Proof:          f.Digest,
			Size:           f.Size,
			Kind:           f.Kind,
			ReloadOnChange: f.ReloadOnChange,
		})
	}
	return files
}

func chunkCount(ops, inPlace int) int {
	if ops == 0 {
		return 0
	}
	first := min(max(api.MaxOpsPerApply-inPlace, 0), ops)
	return 1 + (ops-first+api.MaxOpsPerApply-1)/api.MaxOpsPerApply
}
