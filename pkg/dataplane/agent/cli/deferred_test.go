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
	"errors"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/agent/api"
)

type countingObserver struct{ done, deferred, abandoned map[string]int }

func newCountingObserver() *countingObserver {
	return &countingObserver{done: map[string]int{}, deferred: map[string]int{}, abandoned: map[string]int{}}
}
func (o *countingObserver) DeferredDeleteDone(kind string)      { o.done[kind]++ }
func (o *countingObserver) DeferredDeleteDeferred(kind string)  { o.deferred[kind]++ }
func (o *countingObserver) DeferredDeleteAbandoned(kind string) { o.abandoned[kind]++ }

// A delete the agent gives up on is reported as abandoned, not as one more
// retry: the object stays in the worker until a reload, which an alert must
// be able to tell apart from "still draining".
func TestARequeuePastTheAttemptCapIsAbandoned(t *testing.T) {
	observer := newCountingObserver()
	d := NewDeferrals(nil, slog.New(slog.DiscardHandler), observer)
	cause := errors.New("Wait delay expired")

	// The drain pops an item before it retries it; mirror that here.
	server := attempt[ServerRef]{Target: ServerRef{Backend: "be", Server: "srv"}}
	for i := 1; i < api.MaxDeferredAttempts; i++ {
		d.requeueServer(server, cause)
		server, d.servers = d.servers[len(d.servers)-1], d.servers[:len(d.servers)-1]
	}
	assert.Equal(t, api.MaxDeferredAttempts-1, observer.deferred["server"])
	assert.Zero(t, observer.abandoned["server"])

	d.requeueServer(server, cause)
	assert.Equal(t, 1, observer.abandoned["server"], "the last attempt is abandoned")
	assert.Equal(t, api.MaxDeferredAttempts-1, observer.deferred["server"], "and not counted as deferred")
	assert.Empty(t, d.servers, "an abandoned delete leaves the queue")

	backend := attempt[string]{Target: "be", Tries: api.MaxDeferredAttempts - 1}
	d.requeueBackend(backend, cause)
	assert.Equal(t, 1, observer.abandoned["backend"])
	assert.Empty(t, d.backends)
}
