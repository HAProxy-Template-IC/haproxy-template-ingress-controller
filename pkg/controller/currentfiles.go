// Copyright 2025 Philipp Hossner
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

package controller

import (
	"maps"
	"sync"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane"
)

// currentFilesAuthority owns the accepted auxiliary output for one leader term.
type currentFilesAuthority struct {
	mu sync.RWMutex

	published   *publishedAuxFiles
	generation  uint64
	active      bool
	hasAccepted bool
	accepted    map[string]string
}

func newCurrentFilesAuthority(published *publishedAuxFiles) *currentFilesAuthority {
	return &currentFilesAuthority{published: published}
}

func (a *currentFilesAuthority) BeginTerm() uint64 {
	a.mu.Lock()
	defer a.mu.Unlock()

	a.generation++
	a.active = true
	a.hasAccepted = false
	a.accepted = nil
	return a.generation
}

func (a *currentFilesAuthority) EndTerm(generation uint64) {
	a.mu.Lock()
	defer a.mu.Unlock()

	if !a.active || a.generation != generation {
		return
	}
	a.active = false
	a.hasAccepted = false
	a.accepted = nil
}

func (a *currentFilesAuthority) Snapshot(generation uint64) (map[string]string, error) {
	a.mu.RLock()
	if a.active && a.generation == generation && a.hasAccepted {
		files := maps.Clone(a.accepted)
		a.mu.RUnlock()
		if err := a.publishedAvailabilityError(); err != nil {
			return nil, err
		}
		return files, nil
	}
	a.mu.RUnlock()

	return a.publishedSnapshot()
}

func (a *currentFilesAuthority) publishedAvailabilityError() error {
	if a.published == nil {
		return nil
	}
	return a.published.availabilityError()
}

func (a *currentFilesAuthority) Accept(generation uint64, auxiliaryFiles *dataplane.AuxiliaryFiles) {
	files := auxiliaryFiles.CurrentFiles()

	a.mu.Lock()
	defer a.mu.Unlock()

	if !a.active || a.generation != generation {
		return
	}
	a.hasAccepted = true
	a.accepted = maps.Clone(files)
}

func (a *currentFilesAuthority) publishedSnapshot() (map[string]string, error) {
	if a.published == nil {
		return map[string]string{}, nil
	}
	return a.published.get()
}
