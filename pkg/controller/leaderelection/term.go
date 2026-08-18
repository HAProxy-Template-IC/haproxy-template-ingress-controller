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

package leaderelection

// Term is one leadership term as its writers see it: the identity and epoch
// every apply carries, and the way to hand the term back when something proves
// this controller is not the fleet's writer any more.
type Term struct {
	*LeaseEpoch
	standDown func(reason string)
}

// NewTerm binds the term's epoch to the way its holder gives leadership up.
// standDown must release the Lease — only a fresh acquisition claims a fresh
// epoch, so anything short of it leaves this replica elected and refused.
func NewTerm(epoch *LeaseEpoch, standDown func(reason string)) *Term {
	return &Term{LeaseEpoch: epoch, standDown: standDown}
}

// StandDown gives leadership up.
func (t *Term) StandDown(reason string) {
	if t == nil || t.standDown == nil {
		return
	}
	t.standDown(reason)
}
