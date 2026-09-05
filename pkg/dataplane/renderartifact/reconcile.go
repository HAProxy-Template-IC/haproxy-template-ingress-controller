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

package renderartifact

import "errors"

// ReconcileSnapshot returns desired through an authenticated transition from base.
func ReconcileSnapshot(
	authority *Authority,
	base *Snapshot,
	desired *Snapshot,
) (*Snapshot, *Delta, error) {
	if err := authority.ValidateSnapshot(base); err != nil {
		return nil, nil, err
	}
	if err := authority.ValidateSnapshot(desired); err != nil {
		return nil, nil, err
	}
	transaction, err := BeginTransaction(authority, base)
	if err != nil {
		return nil, nil, err
	}
	same, err := base.SameRoot(desired)
	if err != nil {
		return nil, nil, err
	}
	if !same {
		if err := reconcileArtifactSnapshots(transaction, base, desired); err != nil {
			return nil, nil, err
		}
	}
	next, delta, err := transaction.Commit()
	if err != nil {
		return nil, nil, err
	}
	equal, err := next.ExactEqual(desired)
	if err != nil {
		return nil, nil, err
	}
	if !equal {
		return nil, nil, errors.New("reconciled artifact snapshot does not match desired state")
	}
	return next, delta, nil
}

func reconcileArtifactSnapshots(
	transaction *Transaction,
	base *Snapshot,
	desired *Snapshot,
) error {
	baseCursor := newSnapshotCursor(base.root)
	desiredCursor := newSnapshotCursor(desired.root)
	baseArtifact, baseFound, err := baseCursor.next(base.authority)
	if err != nil {
		return err
	}
	desiredArtifact, desiredFound, err := desiredCursor.next(desired.authority)
	if err != nil {
		return err
	}
	for baseFound || desiredFound {
		switch comparison := compareReconciledArtifacts(baseArtifact, baseFound, desiredArtifact, desiredFound); {
		case comparison < 0:
			if err := transaction.Delete(exactArtifactHandle(base, baseArtifact)); err != nil {
				return err
			}
			baseArtifact, baseFound, err = baseCursor.next(base.authority)
		case comparison > 0:
			if err := transaction.Insert(
				desiredArtifact.descriptor.value,
				desiredArtifact.content,
			); err != nil {
				return err
			}
			desiredArtifact, desiredFound, err = desiredCursor.next(desired.authority)
		default:
			if err := transaction.Replace(
				exactArtifactHandle(base, baseArtifact),
				desiredArtifact.descriptor.value,
				desiredArtifact.content,
			); err != nil {
				return err
			}
			baseArtifact, baseFound, err = baseCursor.next(base.authority)
			if err == nil {
				desiredArtifact, desiredFound, err = desiredCursor.next(desired.authority)
			}
		}
		if err != nil {
			return err
		}
	}
	return nil
}

func compareReconciledArtifacts(
	base *Artifact,
	baseFound bool,
	desired *Artifact,
	desiredFound bool,
) int {
	if !baseFound {
		return 1
	}
	if !desiredFound {
		return -1
	}
	return compareArtifactKeys(base.descriptor.key, desired.descriptor.key)
}

func exactArtifactHandle(base *Snapshot, artifact *Artifact) *Handle {
	handle := &Handle{base: base, artifact: artifact, key: artifact.descriptor.key}
	handle.seal = handle
	return handle
}
