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

package rendercycle

import (
	"errors"
	"strconv"
	"sync/atomic"
)

var errInvalidOccurrence = errors.New("render occurrence is invalid")

var occurrenceSequence atomic.Uint64

type occurrenceAuthentication struct {
	owner    *Occurrence
	snapshot *Snapshot
	proof    string
}

// Occurrence identifies one execution that produced an authenticated cycle.
type Occurrence struct {
	snapshot *Snapshot
	proof    string
	seal     *Occurrence
	auth     occurrenceAuthentication
}

// NewOccurrence seals a new process-local occurrence of snapshot.
func NewOccurrence(snapshot *Snapshot) (*Occurrence, error) {
	if err := snapshot.ValidateAuthentication(); err != nil {
		return nil, errors.Join(errInvalidOccurrence, err)
	}
	sequence := occurrenceSequence.Add(1)
	if sequence == 0 {
		panic("render occurrence sequence exhausted")
	}
	occurrence := &Occurrence{
		snapshot: snapshot,
		proof:    "o:" + strconv.FormatUint(sequence, 10),
	}
	occurrence.seal = occurrence
	occurrence.auth = occurrenceAuthentication{
		owner: occurrence, snapshot: occurrence.snapshot, proof: occurrence.proof,
	}
	return occurrence, nil
}

// ValidateAuthentication verifies the occurrence and its exact cycle binding.
func (o *Occurrence) ValidateAuthentication() error {
	if o == nil || o.seal != o || o.auth.owner != o || o.snapshot == nil ||
		o.auth.snapshot != o.snapshot || o.proof == "" || o.auth.proof != o.proof {
		return errInvalidOccurrence
	}
	if err := o.snapshot.ValidateAuthentication(); err != nil {
		return errors.Join(errInvalidOccurrence, err)
	}
	return nil
}

// Snapshot returns the exact cycle bound to this occurrence.
func (o *Occurrence) Snapshot() (*Snapshot, error) {
	if err := o.ValidateAuthentication(); err != nil {
		return nil, err
	}
	return o.snapshot, nil
}

// Proof returns the diagnostic process-local occurrence identifier.
func (o *Occurrence) Proof() (string, error) {
	if err := o.ValidateAuthentication(); err != nil {
		return "", err
	}
	return o.proof, nil
}

// Same reports whether other is this exact occurrence.
func (o *Occurrence) Same(other *Occurrence) (bool, error) {
	if err := o.ValidateAuthentication(); err != nil {
		return false, err
	}
	if err := other.ValidateAuthentication(); err != nil {
		return false, err
	}
	return o == other, nil
}
