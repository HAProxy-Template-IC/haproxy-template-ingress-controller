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

package component

// mailboxAbsorbed returns how many events the mailbox intake has taken off
// the subscription channel and still holds in the queue, counting collapsed
// events via their superseded tally. Test-only introspection used to pace
// publishers so tests assert the mailbox CONTRACT (intake drains while the
// handler is busy) rather than racing the intake goroutine's scheduling.
func (b *Base) mailboxAbsorbed() int {
	b.mbMu.Lock()
	defer b.mbMu.Unlock()
	n := 0
	for i := range b.mbQueue {
		n += 1 + b.mbQueue[i].superseded
	}
	return n
}
