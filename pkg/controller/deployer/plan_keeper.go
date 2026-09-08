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
	"context"
	"errors"
	"log/slog"
	"sync"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane"
	agentclient "gitlab.com/haproxy-haptic/haptic/pkg/dataplane/agent/client"
)

// planKeeper hands each pod the blob of the plan it applied, after the apply
// and off its critical path. The blob is what a cold leader reads its baseline
// from; nothing in the apply needs it, and encoding it cost 17 ms of every
// deployment at 1,800 routes before any pod was contacted. One upload is in
// flight per pod and only the newest offer waits behind it: under churn a plan
// the pod has already moved past is never encoded at all.
//
// A pod whose agent predates PUT /v1/plan is remembered; its blob rides the
// next apply instead, as it always did.
type planKeeper struct {
	clients *agentClients
	logger  *slog.Logger

	mu         sync.Mutex
	ctx        context.Context       // the term's; nil before the first term
	pending    map[string]*planOffer // pod key → the newest offer not yet uploaded
	running    map[string]bool       // pod key → an upload loop is draining offers
	noEndpoint map[string]bool       // pod key → the agent has no plan endpoint
	wg         sync.WaitGroup
}

// planOffer is one applied plan whose blob a pod should hold.
type planOffer struct {
	endpoint dataplane.Endpoint
	planID   string
	proof    string
	blob     *planBlob
}

func newPlanKeeper(clients *agentClients, logger *slog.Logger) *planKeeper {
	return &planKeeper{
		clients:    clients,
		logger:     logger,
		pending:    map[string]*planOffer{},
		running:    map[string]bool{},
		noEndpoint: map[string]bool{},
	}
}

// Delivers reports whether the keeper hands this pod its blobs; false once
// the pod's agent answered that it has no plan endpoint.
func (k *planKeeper) Delivers(endpoint *dataplane.Endpoint) bool {
	k.mu.Lock()
	defer k.mu.Unlock()
	return !k.noEndpoint[podKey(endpoint)]
}

// Begin binds the uploads to the term: they outlive the deployment that
// offered them and end with the term, where Wait reaps them.
func (k *planKeeper) Begin(ctx context.Context) {
	k.mu.Lock()
	k.ctx = ctx
	k.mu.Unlock()
}

// Offer schedules the blob of the plan a pod just applied. A newer offer for
// the same pod replaces one still waiting.
func (k *planKeeper) Offer(endpoint *dataplane.Endpoint, planID, proof string, blob *planBlob) {
	if blob == nil || planID == "" || proof == "" {
		return
	}
	key := podKey(endpoint)
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.noEndpoint[key] {
		return
	}
	k.pending[key] = &planOffer{endpoint: *endpoint, planID: planID, proof: proof, blob: blob}
	if k.running[key] {
		return
	}
	ctx := k.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	k.running[key] = true
	k.wg.Add(1)
	go k.drain(ctx, key)
}

// drain uploads the newest offer for one pod until none is left.
func (k *planKeeper) drain(ctx context.Context, key string) {
	defer k.wg.Done()
	for {
		k.mu.Lock()
		offer := k.pending[key]
		delete(k.pending, key)
		if offer == nil || ctx.Err() != nil {
			k.running[key] = false
			k.mu.Unlock()
			return
		}
		k.mu.Unlock()
		k.upload(ctx, key, offer)
	}
}

func (k *planKeeper) upload(ctx context.Context, key string, offer *planOffer) {
	blob := offer.blob.bytes()
	if len(blob) == 0 {
		return
	}
	client, err := k.clients.For(&offer.endpoint)
	if err != nil {
		k.logger.Debug("Plan blob not delivered: no agent client", "pod", offer.endpoint.PodName, "error", err)
		return
	}
	err = client.PutPlan(ctx, offer.planID, offer.proof, blob)
	switch {
	case err == nil:
	case errors.Is(err, agentclient.ErrPlanMoved):
		// The pod applied a newer plan; that apply's offer follows.
	case errors.Is(err, agentclient.ErrNoPlanEndpoint):
		k.mu.Lock()
		k.noEndpoint[key] = true
		delete(k.pending, key)
		k.mu.Unlock()
		k.logger.Info("Agent has no plan endpoint; its plan blob rides the next apply",
			"pod", offer.endpoint.PodName)
	case ctx.Err() != nil:
	default:
		// The next apply's offer tries again; until then a cold leader adopts
		// no baseline from this pod, which costs it one reload, never correctness.
		k.logger.Debug("Plan blob not delivered", "pod", offer.endpoint.PodName, "plan", offer.planID, "error", err)
	}
}

// Wait blocks until every upload loop has drained, for a term's end.
func (k *planKeeper) Wait() {
	k.wg.Wait()
}
