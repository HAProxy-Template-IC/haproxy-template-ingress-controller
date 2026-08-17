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

package cli

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/agent/api"
)

// deferredWaitMs is how long one `wait …-removable` may block. `disable server`
// empties the idle pool, so a server with only keep-alive clients answers in
// milliseconds; the budget only covers in-flight requests.
const deferredWaitMs = 2000

// retryInterval paces the retry of a delete whose target still has clients.
const retryInterval = 2 * time.Second

// ErrDeferralOverflow means the queue is at its cap: the caller must reload
// instead of deferring more deletes.
var ErrDeferralOverflow = errors.New("deferred delete queue is full")

// ServerRef names one server of one backend.
type ServerRef struct {
	Backend string `json:"backend"`
	Server  string `json:"server"`
}

// String renders the reference the way HAProxy addresses it.
func (r ServerRef) String() string { return r.Backend + "/" + r.Server }

// Observer receives deferred-delete outcomes so the server can export them.
type Observer interface {
	DeferredDeleteDone(kind string)
	DeferredDeleteDeferred(kind string)
}

type noopObserver struct{}

func (noopObserver) DeferredDeleteDone(string)     {}
func (noopObserver) DeferredDeleteDeferred(string) {}

// Deferrals drains the delete tail of an apply off the apply path: `wait
// …-removable` blocks for as long as a client keeps a connection, and no apply
// may pay that.
type Deferrals struct {
	client   *Client
	logger   *slog.Logger
	observer Observer

	mu       sync.Mutex
	servers  []attempt[ServerRef]
	backends []attempt[string]
	wake     chan struct{}
}

type attempt[T any] struct {
	Target T
	Tries  int
}

// NewDeferrals builds the queue. observer may be nil.
func NewDeferrals(client *Client, logger *slog.Logger, observer Observer) *Deferrals {
	if observer == nil {
		observer = noopObserver{}
	}
	return &Deferrals{client: client, logger: logger, observer: observer, wake: make(chan struct{}, 1)}
}

// Split separates the ops an apply runs inline from the deletes that block on
// `wait`. The controller composes the full A4 sequence; the agent runs the
// traffic-stopping half now and the removal half later.
func Split(ops []api.Op) (inline []api.Op, servers []ServerRef, backends []string) {
	for _, op := range ops {
		switch op.Kind {
		case api.OpServerDel:
			servers = append(servers, ServerRef{Backend: op.Backend, Server: op.Server})
		case api.OpBackendDel:
			backends = append(backends, op.Backend)
		case api.OpWaitSrvRemovable, api.OpWaitBeRemovable, api.OpShutdownSessions:
			// The queue owns the wait and the session shutdown that follows it.
		default:
			inline = append(inline, op)
		}
	}
	return inline, servers, backends
}

// Enqueue adds a batch of deletes. Past the caps the caller must reload: an
// unbounded queue would hide a leak until the pod ran out of proxies.
func (d *Deferrals) Enqueue(servers []ServerRef, backends []string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if len(d.servers)+len(servers) > api.MaxPendingServerDeletes {
		return fmt.Errorf("%w: %d pending server deletes", ErrDeferralOverflow, len(d.servers))
	}
	if len(d.backends)+len(backends) > api.MaxPendingBackendDeletes {
		return fmt.Errorf("%w: %d pending backend deletes", ErrDeferralOverflow, len(d.backends))
	}
	for _, s := range servers {
		d.servers = append(d.servers, attempt[ServerRef]{Target: s})
	}
	for _, b := range backends {
		d.backends = append(d.backends, attempt[string]{Target: b})
	}
	select {
	case d.wake <- struct{}{}:
	default:
	}
	return nil
}

// Pending reports what is still queued, for /v1/state.
func (d *Deferrals) Pending() api.PendingDeletes {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := api.PendingDeletes{}
	for _, s := range d.servers {
		out.Servers = append(out.Servers, s.Target.String())
	}
	for _, b := range d.backends {
		out.Backends = append(out.Backends, b.Target)
	}
	return out
}

// Start drains the queue until the context ends. The ticker is what picks up
// requeued deletes: a server whose client will not let go is retried on the
// next tick, never in a spin.
func (d *Deferrals) Start(ctx context.Context) error {
	ticker := time.NewTicker(retryInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-d.wake:
			d.drain(ctx)
		case <-ticker.C:
			d.drain(ctx)
		}
	}
}

// drain empties the queue once. Every iteration is bounded by the snapshot it
// took, so a requeue cannot spin.
func (d *Deferrals) drain(ctx context.Context) {
	servers, backends := d.take()
	for _, s := range servers {
		if ctx.Err() != nil {
			return
		}
		d.deleteServer(s)
	}
	for _, b := range backends {
		if ctx.Err() != nil {
			return
		}
		d.deleteBackend(b)
	}
}

func (d *Deferrals) take() ([]attempt[ServerRef], []attempt[string]) {
	d.mu.Lock()
	defer d.mu.Unlock()
	servers, backends := d.servers, d.backends
	d.servers, d.backends = nil, nil
	return servers, backends
}

func (d *Deferrals) deleteServer(a attempt[ServerRef]) {
	ref := a.Target.String()
	err := d.run(fmt.Sprintf("wait %d srv-removable %s", deferredWaitMs, ref), "Done")
	if errors.Is(err, ErrWaitExpired) {
		if err = d.run("shutdown sessions server "+ref, ""); err == nil {
			err = d.run(fmt.Sprintf("wait %d srv-removable %s", deferredWaitMs, ref), "Done")
		}
	}
	if err != nil {
		d.requeueServer(a, err)
		return
	}
	if err := d.run("del server "+ref, "Server deleted"); err != nil {
		d.requeueServer(a, err)
		return
	}
	d.observer.DeferredDeleteDone("server")
}

func (d *Deferrals) deleteBackend(a attempt[string]) {
	err := d.run(fmt.Sprintf("wait %d be-removable %s", deferredWaitMs, a.Target), "Done")
	if err == nil {
		err = d.run("del backend "+a.Target, "Backend deleted")
	}
	if err != nil {
		d.requeueBackend(a, err)
		return
	}
	d.observer.DeferredDeleteDone("backend")
}

// run executes one deferred command and applies the same verdict rules as the
// apply path.
func (d *Deferrals) run(command, expect string) error {
	raw, err := d.client.Raw(command)
	if err != nil {
		return err
	}
	results := matchBatch(raw, []Command{{Text: experimentalPrefix, Optional: true}, {Text: command, Expect: expect}})
	return results[1].Err
}

func (d *Deferrals) requeueServer(a attempt[ServerRef], cause error) {
	a.Tries++
	if a.Tries >= api.MaxDeferredAttempts {
		d.logger.Warn("giving up on a deferred server delete", "server", a.Target.String(), "error", cause)
		d.observer.DeferredDeleteDeferred("server")
		return
	}
	d.mu.Lock()
	d.servers = append(d.servers, a)
	d.mu.Unlock()
	d.observer.DeferredDeleteDeferred("server")
}

// requeueBackend leaves a backend that is still referenced unpublished. The
// controller's next diff treats "exists unpublished" as absent, and the next
// reload removes it for good.
func (d *Deferrals) requeueBackend(a attempt[string], cause error) {
	a.Tries++
	if a.Tries >= api.MaxDeferredAttempts {
		d.logger.Warn("giving up on a deferred backend delete", "backend", a.Target, "error", cause)
		d.observer.DeferredDeleteDeferred("backend")
		return
	}
	d.mu.Lock()
	d.backends = append(d.backends, a)
	d.mu.Unlock()
	d.observer.DeferredDeleteDeferred("backend")
}
