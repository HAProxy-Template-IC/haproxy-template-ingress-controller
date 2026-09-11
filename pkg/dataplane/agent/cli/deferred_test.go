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
	"log/slog"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/agent/api"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/agent/haproxytest"
)

type countingObserver struct{ done, deferred, abandoned, superseded map[string]int }

func testWorkerInfo(pid int) api.HAProxyInfo {
	return api.HAProxyInfo{WorkerPID: pid, WorkerStartTimeUnixMicros: 1_700_000_000_000_000}
}

func newCountingObserver() *countingObserver {
	return &countingObserver{done: map[string]int{}, deferred: map[string]int{}, abandoned: map[string]int{}, superseded: map[string]int{}}
}

func TestWorkerAdoptionRetiresOnlyOutgoingDeletes(t *testing.T) {
	observer := newCountingObserver()
	d := NewDeferrals(nil, slog.New(slog.DiscardHandler), observer)
	d.SetWorker(testWorkerInfo(1000))
	servers, backends := []ServerRef{{Backend: "be", Server: "srv"}}, []string{"be"}
	require.NoError(t, d.Check(servers, backends))
	assert.Empty(t, d.Pending().Servers)
	require.NoError(t, d.Enqueue(testWorkerInfo(1000), servers, backends))
	server, ok := d.takeServer()
	require.True(t, ok)
	d.SetWorker(testWorkerInfo(1000))
	assert.Len(t, d.Pending().Servers, 1, "a failed reload leaves the worker and its cleanup intact")
	d.SetWorker(testWorkerInfo(1001))
	assert.Empty(t, d.Pending().Servers)
	assert.Empty(t, d.Pending().Backends)
	assert.ErrorIs(t, d.Enqueue(testWorkerInfo(1000), servers, backends), ErrWorkerGone)
	require.NoError(t, d.Enqueue(testWorkerInfo(1001), servers, backends))
	d.requeueServer(server, ErrRejected)
	assert.Len(t, d.Pending().Servers, 1, "old in-flight work must not rejoin the replacement queue")
	assert.Len(t, d.Pending().Backends, 1)
	assert.Equal(t, map[string]int{"server": 1, "backend": 1}, observer.superseded)
	assert.Empty(t, observer.abandoned)
}

func TestWorkerAdoptionDetectsTheSamePIDWithANewStartTime(t *testing.T) {
	observer := newCountingObserver()
	d := NewDeferrals(nil, slog.New(slog.DiscardHandler), observer)
	old := testWorkerInfo(1000)
	d.SetWorker(old)
	require.NoError(t, d.Enqueue(old, nil, []string{"queued", "in-flight"}))
	inFlight, ok := d.takeBackend()
	require.True(t, ok)
	replacement := old
	replacement.WorkerStartTimeUnixMicros++
	d.SetWorker(replacement)
	assert.Empty(t, d.Pending().Backends)
	assert.ErrorIs(t, d.Enqueue(old, nil, []string{"old"}), ErrWorkerGone)
	require.NoError(t, d.Enqueue(replacement, nil, []string{"new"}))
	d.requeueBackend(inFlight, ErrRejected)
	assert.Equal(t, []string{"new"}, d.Pending().Backends)
	assert.Equal(t, 2, observer.superseded["backend"])
	assert.Empty(t, observer.abandoned)
}

func TestDeferredWorkerSessionRejectsIdentityMismatchAndCancels(t *testing.T) {
	model := haproxytest.Start(t)
	client, err := New(t.Context(), Config{
		WorkerSocket: model.WorkerSocket(), MasterSocket: model.MasterSocket(),
		Logger: slog.New(slog.DiscardHandler),
	})
	require.NoError(t, err)
	_, err = client.openWorker(t.Context(), testWorkerInfo(999))
	require.ErrorIs(t, err, ErrWorkerGone)
	old := testWorkerInfo(1000)
	old.WorkerStartTimeUnixMicros--
	_, err = client.openWorker(t.Context(), old)
	require.ErrorIs(t, err, ErrWorkerGone)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	session, err := client.openWorker(ctx, testWorkerInfo(1000))
	require.NoError(t, err)
	defer session.close()
	cancel()
	_, err = session.execute("show info")
	require.Error(t, err)
	assert.NotContains(t, model.Sent(), "del backend reused")
}

func TestWorkerCancellationInterruptsAnIncompleteReply(t *testing.T) {
	model := haproxytest.Start(t)
	client, err := New(t.Context(), Config{
		WorkerSocket: model.WorkerSocket(), MasterSocket: model.MasterSocket(),
		Logger: slog.New(slog.DiscardHandler),
	})
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	session, err := client.openWorker(ctx, testWorkerInfo(1000))
	require.NoError(t, err)
	defer session.close()
	entered, release := make(chan struct{}), make(chan struct{})
	unblock := sync.OnceFunc(func() { close(release) })
	t.Cleanup(unblock)
	model.With(func(m *haproxytest.Model) {
		m.Reject = func(string) (string, bool) {
			close(entered)
			<-release
			return "", false
		}
	})
	done := make(chan error, 1)
	go func() { _, err := session.execute("show info float"); done <- err }()
	<-entered
	cancel()
	require.Error(t, <-done)
	unblock()
}

func TestDeferredBackendDeleteDoesNotCrossReload(t *testing.T) {
	model := haproxytest.Start(t)
	client, err := New(t.Context(), Config{
		WorkerSocket: model.WorkerSocket(), MasterSocket: model.MasterSocket(),
		Logger: slog.New(slog.DiscardHandler),
	})
	require.NoError(t, err)
	observer := newCountingObserver()
	d := NewDeferrals(client, slog.New(slog.DiscardHandler), observer)
	d.SetWorker(testWorkerInfo(1000))
	entered, release := make(chan struct{}), make(chan struct{})
	unblock := sync.OnceFunc(func() { close(release) })
	t.Cleanup(unblock)
	model.With(func(m *haproxytest.Model) {
		m.Backends["reused"] = &haproxytest.Backend{}
		m.Reject = func(command string) (string, bool) {
			if strings.HasPrefix(command, "wait ") {
				close(entered)
				<-release
			}
			return "", false
		}
	})
	require.NoError(t, d.Enqueue(testWorkerInfo(1000), nil, []string{"reused"}))
	done := make(chan struct{})
	go func() { d.drain(t.Context()); close(done) }()
	<-entered
	model.With(func(m *haproxytest.Model) {
		m.Pid++
		m.Backends["reused"] = &haproxytest.Backend{}
	})
	unblock()
	<-done
	d.drain(t.Context())
	assert.True(t, model.HasBackend("reused"), "the old delete must not reach the replacement worker")
	assert.NotContains(t, model.Sent(), "del backend reused")
	assert.Equal(t, 1, observer.superseded["backend"])
	assert.Empty(t, observer.abandoned)
}
func (o *countingObserver) DeferredDeleteDone(kind string)       { o.done[kind]++ }
func (o *countingObserver) DeferredDeleteDeferred(kind string)   { o.deferred[kind]++ }
func (o *countingObserver) DeferredDeleteAbandoned(kind string)  { o.abandoned[kind]++ }
func (o *countingObserver) DeferredDeleteSuperseded(kind string) { o.superseded[kind]++ }

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
