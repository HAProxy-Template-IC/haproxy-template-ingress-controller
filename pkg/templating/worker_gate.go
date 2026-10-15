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

package templating

import "context"

// WorkerGate admits the extra workers of a parallel batch. A batch's first
// worker always runs, so a gate that admits none still lets the batch finish.
type WorkerGate interface {
	// TryAcquire reports whether one more worker may run a chunk now.
	TryAcquire() bool
	// Release returns a worker admitted by TryAcquire.
	Release()
}

type workerGateContextKey struct{}

// WithWorkerGate makes parallel batches rendered under ctx admit their extra
// workers through gate.
func WithWorkerGate(ctx context.Context, gate WorkerGate) context.Context {
	return context.WithValue(ctx, workerGateContextKey{}, gate)
}

// WorkerGateOf returns the gate WithWorkerGate attached to ctx, or nil.
func WorkerGateOf(ctx context.Context) WorkerGate {
	gate, _ := ctx.Value(workerGateContextKey{}).(WorkerGate)
	return gate
}

type admitAllWorkers struct{}

func (admitAllWorkers) TryAcquire() bool { return true }
func (admitAllWorkers) Release()         {}
