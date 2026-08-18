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

package files

import (
	"errors"
	"fmt"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/agent/api"
)

// Transaction is one apply's file work: back up what it is about to change,
// then install the staged parts and drop the paths the manifest no longer
// names. It never runs a runtime command and never reloads.
type Transaction struct {
	store     *Store
	journal   *Journal
	installs  []*Staged
	deletes   []string
	configRel string
}

// Begin opens a transaction whose rollback target is the journal's contents.
// configRel is installed last so the tree is never a config newer than the
// files it references.
func (s *Store) Begin(j *Journal, configRel string) *Transaction {
	return &Transaction{store: s, journal: j, configRel: configRel}
}

// Install schedules a verified part.
func (t *Transaction) Install(staged *Staged) { t.installs = append(t.installs, staged) }

// Delete schedules the removal of a path the manifest dropped.
func (t *Transaction) Delete(rel string) { t.deletes = append(t.deletes, rel) }

// Changes counts the paths this transaction touches.
func (t *Transaction) Changes() int { return len(t.installs) + len(t.deletes) }

// Backup records the last-known-good version of every path the transaction
// touches. It is the first phase that can fail, and it changes no content.
func (t *Transaction) Backup() error {
	if n := t.Changes(); n > api.MaxFiles {
		return fmt.Errorf("transaction touches %d paths, over the %d-file limit", n, api.MaxFiles)
	}
	for _, staged := range t.installs {
		if err := t.store.backup(staged.Rel, t.journal); err != nil {
			return err
		}
	}
	for _, rel := range t.deletes {
		if err := t.store.backupDeleted(rel, t.journal); err != nil {
			return err
		}
	}
	return nil
}

// Write installs the staged parts, config last, and then drops the deleted
// paths. A failure leaves the journal complete, so the caller can restore.
func (t *Transaction) Write() error {
	var config *Staged
	for _, staged := range t.installs {
		if staged.Rel == t.configRel {
			config = staged
			continue
		}
		if err := t.store.install(staged); err != nil {
			return err
		}
	}
	if config != nil {
		if err := t.store.install(config); err != nil {
			return err
		}
	}
	var errs []error
	for _, rel := range t.deletes {
		errs = append(errs, t.store.unlink(rel))
	}
	return errors.Join(errs...)
}

// Discard drops every staged part that has not been installed. The tree is
// untouched by a discarded transaction.
func (t *Transaction) Discard() {
	for _, staged := range t.installs {
		staged.Discard()
	}
}
