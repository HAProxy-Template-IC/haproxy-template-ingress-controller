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
	// DeferredDeleteAbandoned is a delete the agent gave up on: the object
	// stays until the next reload, which is a distinct fact from "retrying".
	DeferredDeleteAbandoned(kind string)
}

type noopObserver struct{}

func (noopObserver) DeferredDeleteDone(string)      {}
func (noopObserver) DeferredDeleteDeferred(string)  {}
func (noopObserver) DeferredDeleteAbandoned(string) {}

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
	// inFlight* is the item the single drain goroutine is working on. It is
	// still outstanding, so the caps and /v1/state have to count it.
	inFlightServer  *ServerRef
	inFlightBackend *string
	wake            chan struct{}
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
	for i := range ops {
		op := &ops[i]
		switch op.Kind {
		case api.OpServerDel:
			servers = append(servers, ServerRef{Backend: op.Backend, Server: op.Server})
		case api.OpBackendDel:
			backends = append(backends, op.Backend)
		case api.OpServerWaitRemovable, api.OpBackendWaitRemovable, api.OpShutdownSessions:
			// The queue owns the wait and the session shutdown that follows it.
		default:
			inline = append(inline, *op)
		}
	}
	return inline, servers, backends
}

// Enqueue adds a batch of deletes. Past the caps the caller must reload: an
// unbounded queue would hide a leak until the pod ran out of proxies.
func (d *Deferrals) Enqueue(servers []ServerRef, backends []string) error {
	// The queue builds its own command lines later, out of reach of the
	// compilers' checks, so the names pass the same grammar here.
	for _, s := range servers {
		if err := errors.Join(
			validateToken("backend", s.Backend),
			validateToken("server", s.Server),
		); err != nil {
			return err
		}
	}
	for _, b := range backends {
		if err := validateToken("backend", b); err != nil {
			return err
		}
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if pending := d.outstandingServersLocked(); pending+len(servers) > api.MaxPendingServerDeletes {
		return fmt.Errorf("%w: %d pending server deletes", ErrDeferralOverflow, pending)
	}
	if pending := d.outstandingBackendsLocked(); pending+len(backends) > api.MaxPendingBackendDeletes {
		return fmt.Errorf("%w: %d pending backend deletes", ErrDeferralOverflow, pending)
	}
	for _, s := range servers {
		d.servers = append(d.servers, attempt[ServerRef]{Target: s})
	}
	for _, b := range backends {
		d.backends = append(d.backends, attempt[string]{Target: b})
	}
	return nil
}

// Wake starts the drain. It is separate from Enqueue because the queue holds
// the tail of a sequence whose head — `disable server` — the apply still has
// to run: a `wait …-removable` issued before it would burn its budget on a
// server that is still taking traffic. A missed wake costs one ticker period.
func (d *Deferrals) Wake() {
	select {
	case d.wake <- struct{}{}:
	default:
	}
}

// Pending reports what is still to happen, for /v1/state: the queue plus the
// delete the drain is inside, which is outstanding until it reports back.
func (d *Deferrals) Pending() api.PendingDeletes {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := api.PendingDeletes{}
	if d.inFlightServer != nil {
		out.Servers = append(out.Servers, d.inFlightServer.String())
	}
	for _, s := range d.servers {
		out.Servers = append(out.Servers, s.Target.String())
	}
	if d.inFlightBackend != nil {
		out.Backends = append(out.Backends, *d.inFlightBackend)
	}
	for _, b := range d.backends {
		out.Backends = append(out.Backends, b.Target)
	}
	return out
}

func (d *Deferrals) outstandingServersLocked() int {
	if d.inFlightServer == nil {
		return len(d.servers)
	}
	return len(d.servers) + 1
}

func (d *Deferrals) outstandingBackendsLocked() int {
	if d.inFlightBackend == nil {
		return len(d.backends)
	}
	return len(d.backends) + 1
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

// drain works the queue once, one item at a time, so a delete that is still
// running counts against the caps and shows up in /v1/state. The pass is
// bounded by the queue it found, so a requeue cannot spin.
func (d *Deferrals) drain(ctx context.Context) {
	servers, backends := d.queued()
	for range servers {
		if ctx.Err() != nil {
			return
		}
		a, took := d.takeServer()
		if !took {
			break
		}
		if err := d.deleteServer(a); err != nil {
			d.requeueServer(a, err)
			continue
		}
		d.completeServer()
	}
	for range backends {
		if ctx.Err() != nil {
			return
		}
		a, took := d.takeBackend()
		if !took {
			return
		}
		if err := d.deleteBackend(a); err != nil {
			d.requeueBackend(a, err)
			continue
		}
		d.completeBackend()
	}
}

func (d *Deferrals) queued() (servers, backends int) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.servers), len(d.backends)
}

func (d *Deferrals) takeServer() (attempt[ServerRef], bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if len(d.servers) == 0 {
		return attempt[ServerRef]{}, false
	}
	a := d.servers[0]
	d.servers = d.servers[1:]
	target := a.Target
	d.inFlightServer = &target
	return a, true
}

func (d *Deferrals) takeBackend() (attempt[string], bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if len(d.backends) == 0 {
		return attempt[string]{}, false
	}
	a := d.backends[0]
	d.backends = d.backends[1:]
	target := a.Target
	d.inFlightBackend = &target
	return a, true
}

func (d *Deferrals) completeServer() {
	d.mu.Lock()
	d.inFlightServer = nil
	d.mu.Unlock()
	d.observer.DeferredDeleteDone("server")
}

func (d *Deferrals) completeBackend() {
	d.mu.Lock()
	d.inFlightBackend = nil
	d.mu.Unlock()
	d.observer.DeferredDeleteDone("backend")
}

func (d *Deferrals) deleteServer(a attempt[ServerRef]) error {
	ref := a.Target.String()
	err := d.run(fmt.Sprintf("wait %d srv-removable %s", deferredWaitMs, ref), expectDone)
	if errors.Is(err, ErrWaitExpired) {
		if err = d.run("shutdown sessions server "+ref, ""); err == nil {
			err = d.run(fmt.Sprintf("wait %d srv-removable %s", deferredWaitMs, ref), expectDone)
		}
	}
	if err != nil {
		return err
	}
	return d.run("del server "+ref, "Server deleted")
}

func (d *Deferrals) deleteBackend(a attempt[string]) error {
	err := d.run(fmt.Sprintf("wait %d be-removable %s", deferredWaitMs, a.Target), expectDone)
	if err == nil {
		err = d.run("del backend "+a.Target, "Backend deleted")
	}
	return err
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
	give := a.Tries >= api.MaxDeferredAttempts
	d.mu.Lock()
	if !give {
		d.servers = append(d.servers, a)
	}
	d.inFlightServer = nil
	d.mu.Unlock()
	if give {
		d.logger.Warn("giving up on a deferred server delete", "server", a.Target.String(), "error", cause)
		d.observer.DeferredDeleteAbandoned("server")
		return
	}
	d.observer.DeferredDeleteDeferred("server")
}

// requeueBackend leaves a backend that is still referenced unpublished. The
// controller's next diff treats "exists unpublished" as absent, and the next
// reload removes it for good.
func (d *Deferrals) requeueBackend(a attempt[string], cause error) {
	a.Tries++
	give := a.Tries >= api.MaxDeferredAttempts
	d.mu.Lock()
	if !give {
		d.backends = append(d.backends, a)
	}
	d.inFlightBackend = nil
	d.mu.Unlock()
	if give {
		d.logger.Warn("giving up on a deferred backend delete", "backend", a.Target, "error", cause)
		d.observer.DeferredDeleteAbandoned("backend")
		return
	}
	d.observer.DeferredDeleteDeferred("backend")
}
