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
	"encoding/json"
	"fmt"
	"sync"

	"github.com/klauspost/compress/zstd"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/agent/api"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/renderplan"
)

// planCache holds the render plans the fleet still refers to: every pod's
// applied, running and worker-ops plan, plus the newest render. A plan it
// cannot produce costs that pod a full-state reload, so the cache is a
// latency optimisation with a correctness floor, never an authority.
type planCache struct {
	mu     sync.Mutex
	plans  map[string]*renderplan.Plan
	newest string
	// unusable are the applied plan ids whose blob this controller failed to
	// decode; retrying the decode every apply would burn CPU for the same answer.
	unusable map[string]struct{}
}

func newPlanCache() *planCache {
	return &planCache{
		plans:    map[string]*renderplan.Plan{},
		unusable: map[string]struct{}{},
	}
}

// Put records a plan and makes it the newest, which Retain always keeps.
func (c *planCache) Put(plan *renderplan.Plan) {
	if plan == nil || plan.ID == "" {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.plans[plan.ID] = plan
	c.newest = plan.ID
	delete(c.unusable, plan.ID)
}

// Plan returns the plan with this id, or nil.
func (c *planCache) Plan(id string) *renderplan.Plan {
	if id == "" {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.plans[id]
}

// Baseline resolves what a pod applied. A cache miss decodes the opaque blob
// the pod reports — the bytes this controller (or its predecessor) sent — so a
// leader change does not reload the fleet. Anything the decode cannot vouch
// for is no baseline at all: a partial plan would diff into ops for a pod that
// runs something else.
func (c *planCache) Baseline(state *api.State) *renderplan.Plan {
	if state == nil || state.AppliedPlanID == "" {
		return nil
	}
	if plan := c.Plan(state.AppliedPlanID); plan != nil {
		return plan
	}
	c.mu.Lock()
	_, known := c.unusable[state.AppliedPlanID]
	c.mu.Unlock()
	if known || len(state.AppliedPlan) == 0 {
		return nil
	}

	plan, err := decodePlan(state.AppliedPlan)
	if err != nil || plan.ID != state.AppliedPlanID || plan.SchemaVersion != renderplan.SchemaVersion {
		c.mu.Lock()
		c.unusable[state.AppliedPlanID] = struct{}{}
		c.mu.Unlock()
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.plans[plan.ID] = plan
	return plan
}

// Retain drops every plan no pod refers to any more. The newest render always
// survives, so the cache is bounded by three ids per pod plus one.
func (c *planCache) Retain(referenced []string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	keep := make(map[string]struct{}, len(referenced)+1)
	for _, id := range referenced {
		keep[id] = struct{}{}
	}
	if c.newest != "" {
		keep[c.newest] = struct{}{}
	}
	for id := range c.plans {
		if _, wanted := keep[id]; !wanted {
			delete(c.plans, id)
		}
	}
	for id := range c.unusable {
		if _, wanted := keep[id]; !wanted {
			delete(c.unusable, id)
		}
	}
}

// planCodec is the plan blob's encoding: zstd over the plan's JSON, raw bytes
// on the wire. pkg/compression base64s its output, which the multipart part
// has no use for.
var planCodec = struct {
	encoder *zstd.Encoder
	decoder *zstd.Decoder
}{}

func init() {
	encoder, err := zstd.NewWriter(nil,
		zstd.WithEncoderLevel(zstd.SpeedDefault),
		zstd.WithEncoderConcurrency(1))
	if err != nil {
		panic("deployer: creating the plan-blob encoder: " + err.Error())
	}
	decoder, err := zstd.NewReader(nil, zstd.WithDecoderMaxMemory(api.MaxApplyBodyBytes))
	if err != nil {
		panic("deployer: creating the plan-blob decoder: " + err.Error())
	}
	planCodec.encoder = encoder
	planCodec.decoder = decoder
}

// encodePlan produces the opaque blob a pod stores and hands back on
// /v1/state. It carries the plan id, so a decode can prove what it decoded.
func encodePlan(plan *renderplan.Plan) ([]byte, error) {
	encoded, err := json.Marshal(plan)
	if err != nil {
		return nil, fmt.Errorf("encoding plan %s: %w", plan.ID, err)
	}
	blob := planCodec.encoder.EncodeAll(encoded, nil)
	if len(blob) > api.MaxPlanBlobBytes {
		return nil, fmt.Errorf("plan %s compresses to %d bytes, over the %d-byte limit", plan.ID, len(blob), api.MaxPlanBlobBytes)
	}
	return blob, nil
}

func decodePlan(blob []byte) (*renderplan.Plan, error) {
	decoded, err := planCodec.decoder.DecodeAll(blob, nil)
	if err != nil {
		return nil, fmt.Errorf("decompressing plan blob: %w", err)
	}
	var plan renderplan.Plan
	if err := json.Unmarshal(decoded, &plan); err != nil {
		return nil, fmt.Errorf("decoding plan blob: %w", err)
	}
	return &plan, nil
}
