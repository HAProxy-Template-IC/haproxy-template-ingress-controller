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

package sections

import "fmt"

// sectionFrontend is the section name reported by frontend operations.
const sectionFrontend = "frontend"

// FrontendMaxconnUpdateOp is a specialized frontend update that carries ONLY a
// maxconn change, applied to the live worker via the runtime API
// (`set maxconn frontend`) without a reload. The comparator emits it only when
// maxconn is the sole differing frontend attribute and the desired value is
// set; any other attribute change keeps the whole frontend update structural.
//
// Like ServerUpdateOp, it is admitted to the runtime fast path by
// partitionByRuntimeEligibility and turned into an X-Runtime-Actions verb by
// buildRuntimeActions (both in pkg/dataplane/orchestrator.go). It is its own
// type rather than a generic op so those two functions can recognise it by
// type assertion, exactly as they do for ServerUpdateOp.
type FrontendMaxconnUpdateOp struct {
	frontendName string
	maxconn      int64
}

// NewFrontendMaxconnUpdate creates a runtime-eligible frontend maxconn update.
// maxconn is the desired value; the caller has already established that it is
// set and is the only changed (non-nested) frontend attribute.
func NewFrontendMaxconnUpdate(frontendName string, maxconn int64) *FrontendMaxconnUpdateOp {
	return &FrontendMaxconnUpdateOp{frontendName: frontendName, maxconn: maxconn}
}

func (op *FrontendMaxconnUpdateOp) Type() OperationType { return OperationUpdate }
func (op *FrontendMaxconnUpdateOp) Section() string     { return sectionFrontend }

func (op *FrontendMaxconnUpdateOp) Describe() string {
	return fmt.Sprintf("Update %s '%s' maxconn to %d (runtime)", sectionFrontend, op.frontendName, op.maxconn)
}

// FrontendName returns the frontend whose maxconn is being updated.
func (op *FrontendMaxconnUpdateOp) FrontendName() string { return op.frontendName }

// RuntimeAction returns the X-Runtime-Actions verb that applies this update via
// the dataplane API's skip_reload raw push (parsed by executeRuntimeActions:
// `SetFrontendMaxConn <name> <value>`).
func (op *FrontendMaxconnUpdateOp) RuntimeAction() string {
	return fmt.Sprintf("SetFrontendMaxConn %s %d", op.frontendName, op.maxconn)
}
