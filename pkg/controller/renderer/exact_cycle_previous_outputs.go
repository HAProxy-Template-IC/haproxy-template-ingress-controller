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

package renderer

import (
	"errors"
	"maps"
	"sync"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/rendercontext"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/renderplan"
)

type exactCycleCurrentAuxFilesRoot struct {
	files map[string]string
	seal  *exactCycleCurrentAuxFilesRoot
}

type exactCycleCurrentAuxFilesSource struct {
	root *exactCycleCurrentAuxFilesRoot
	auth *exactCycleCurrentAuxFilesRoot
	seal *exactCycleCurrentAuxFilesSource
}

var emptyExactCycleCurrentAuxFilesRoot = newExactCycleCurrentAuxFilesRoot(nil)

func newExactCycleCurrentAuxFilesRoot(files map[string]string) *exactCycleCurrentAuxFilesRoot {
	root := &exactCycleCurrentAuxFilesRoot{files: maps.Clone(files)}
	if root.files == nil {
		root.files = map[string]string{}
	}
	root.seal = root
	return root
}

func newExactCycleCurrentAuxFilesSource(
	root *exactCycleCurrentAuxFilesRoot,
) *exactCycleCurrentAuxFilesSource {
	source := &exactCycleCurrentAuxFilesSource{root: root, auth: root}
	source.seal = source
	return source
}

func (s *exactCycleCurrentAuxFilesSource) ValidateAuthentication() error {
	if s == nil || s.seal != s || s.root == nil || s.root != s.auth || s.root.seal != s.root {
		return errors.New("currentFiles source has invalid provenance")
	}
	return nil
}

func (s *exactCycleCurrentAuxFilesSource) SameRoot(other rendercontext.CurrentAuxFilesSource) (bool, error) {
	if err := s.ValidateAuthentication(); err != nil {
		return false, err
	}
	typed, ok := other.(*exactCycleCurrentAuxFilesSource)
	if !ok {
		return false, nil
	}
	if err := typed.ValidateAuthentication(); err != nil {
		return false, err
	}
	return s.root == typed.root, nil
}

func (s *exactCycleCurrentAuxFilesSource) MaterializeCurrentAuxFiles() (map[string]string, error) {
	if err := s.ValidateAuthentication(); err != nil {
		return nil, err
	}
	return maps.Clone(s.root.files), nil
}

type unversionedCurrentAuxFilesSource struct {
	provider func() map[string]string
	once     sync.Once
	files    map[string]string
	seal     *unversionedCurrentAuxFilesSource
}

func newUnversionedCurrentAuxFilesSource(provider func() map[string]string) *unversionedCurrentAuxFilesSource {
	source := &unversionedCurrentAuxFilesSource{provider: provider}
	source.seal = source
	return source
}

func (s *unversionedCurrentAuxFilesSource) ValidateAuthentication() error {
	if s == nil || s.seal != s || s.provider == nil {
		return errors.New("currentFiles source has invalid provenance")
	}
	return nil
}

func (s *unversionedCurrentAuxFilesSource) SameRoot(rendercontext.CurrentAuxFilesSource) (bool, error) {
	if err := s.ValidateAuthentication(); err != nil {
		return false, err
	}
	return false, nil
}

func (s *unversionedCurrentAuxFilesSource) MaterializeCurrentAuxFiles() (map[string]string, error) {
	if err := s.ValidateAuthentication(); err != nil {
		return nil, err
	}
	s.once.Do(func() {
		s.files = maps.Clone(s.provider())
	})
	return maps.Clone(s.files), nil
}

type exactCycleCurrentConfigRoot struct {
	projection *exactCycleCurrentConfigProjection
	plan       *renderplan.Snapshot
	auth       exactCycleCurrentConfigRootAuthentication
	seal       *exactCycleCurrentConfigRoot
}

type exactCycleCurrentConfigSource struct {
	root *exactCycleCurrentConfigRoot
	auth *exactCycleCurrentConfigRoot
	seal *exactCycleCurrentConfigSource
}

func newExactCycleCurrentConfigSource(root *exactCycleCurrentConfigRoot) *exactCycleCurrentConfigSource {
	source := &exactCycleCurrentConfigSource{root: root, auth: root}
	source.seal = source
	return source
}

func (s *exactCycleCurrentConfigSource) ValidateAuthentication() error {
	if s == nil || s.seal != s || s.root != s.auth {
		return errors.New("currentConfig source has invalid provenance")
	}
	if s.root != nil {
		return s.root.validate()
	}
	return nil
}

func (s *exactCycleCurrentConfigSource) SameRoot(other rendercontext.CurrentConfigSource) (bool, error) {
	if err := s.ValidateAuthentication(); err != nil {
		return false, err
	}
	typed, ok := other.(*exactCycleCurrentConfigSource)
	if !ok {
		return false, nil
	}
	if err := typed.ValidateAuthentication(); err != nil {
		return false, err
	}
	if s.root == nil || typed.root == nil {
		return s.root == typed.root, nil
	}
	return s.root.projection == typed.root.projection, nil
}

func (s *exactCycleCurrentConfigSource) MaterializeCurrentConfig() (*renderplan.CurrentConfig, error) {
	if err := s.ValidateAuthentication(); err != nil {
		return nil, err
	}
	if s.root == nil {
		return nil, nil
	}
	current, err := s.root.materialize()
	if err != nil {
		return nil, err
	}
	return &current, nil
}

type exactCyclePreviousOutputs struct {
	currentConfig rendercontext.CurrentConfigSource
	currentFiles  rendercontext.CurrentAuxFilesSource
	useConfig     bool
	useFiles      bool
	auth          exactCyclePreviousOutputsAuthentication
	seal          *exactCyclePreviousOutputs
}

type exactCyclePreviousOutputsAuthentication struct {
	currentConfig rendercontext.CurrentConfigSource
	currentFiles  rendercontext.CurrentAuxFilesSource
	useConfig     bool
	useFiles      bool
}

func newExactCyclePreviousOutputs(
	currentConfig rendercontext.CurrentConfigSource,
	currentFiles rendercontext.CurrentAuxFilesSource,
	useConfig bool,
	useFiles bool,
) *exactCyclePreviousOutputs {
	if !useConfig {
		currentConfig = nil
	}
	if !useFiles {
		currentFiles = nil
	}
	result := &exactCyclePreviousOutputs{
		currentConfig: currentConfig, currentFiles: currentFiles, useConfig: useConfig, useFiles: useFiles,
	}
	result.auth = exactCyclePreviousOutputsAuthentication{
		currentConfig: result.currentConfig, currentFiles: result.currentFiles,
		useConfig: result.useConfig, useFiles: result.useFiles,
	}
	result.seal = result
	return result
}

func (p *exactCyclePreviousOutputs) matches(current *exactCyclePreviousOutputs) (bool, error) {
	if err := p.validate(); err != nil {
		return false, err
	}
	if err := current.validate(); err != nil {
		return false, err
	}
	if p.useConfig != current.useConfig || p.useFiles != current.useFiles {
		return false, nil
	}
	if p.useConfig {
		configSame, err := sameCurrentConfigSource(p.currentConfig, current.currentConfig)
		if err != nil || !configSame {
			return configSame, err
		}
	}
	if p.useFiles {
		return sameCurrentAuxFilesSource(p.currentFiles, current.currentFiles)
	}
	return true, nil
}

func (p *exactCyclePreviousOutputs) validate() error {
	if p == nil || p.seal != p || p.currentConfig != p.auth.currentConfig ||
		p.currentFiles != p.auth.currentFiles || p.useConfig != p.auth.useConfig ||
		p.useFiles != p.auth.useFiles || !p.useConfig && p.currentConfig != nil ||
		!p.useFiles && p.currentFiles != nil {
		return errors.New("previous-output sources have invalid provenance")
	}
	if p.currentConfig != nil {
		if err := p.currentConfig.ValidateAuthentication(); err != nil {
			return err
		}
	}
	if p.currentFiles != nil {
		return p.currentFiles.ValidateAuthentication()
	}
	return nil
}

func sameCurrentConfigSource(left, right rendercontext.CurrentConfigSource) (bool, error) {
	if left == nil || right == nil {
		return left == nil && right == nil, nil
	}
	return left.SameRoot(right)
}

func sameCurrentAuxFilesSource(left, right rendercontext.CurrentAuxFilesSource) (bool, error) {
	if left == nil || right == nil {
		return left == nil && right == nil, nil
	}
	return left.SameRoot(right)
}
