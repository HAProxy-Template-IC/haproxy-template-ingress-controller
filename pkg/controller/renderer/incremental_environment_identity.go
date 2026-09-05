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

package renderer

import (
	"errors"
	"fmt"
	"reflect"
	"slices"

	"gitlab.com/haproxy-haptic/haptic/pkg/incremental"
)

type incrementalEnvironmentAuthority struct {
	owner        *incrementalRenderState
	graph        *incremental.Graph
	executor     incrementalEnvironmentExecutorIdentity
	declarations *incrementalTypeDeclarationIdentity
	seal         *incrementalEnvironmentAuthority
}

type incrementalEnvironmentExecutorIdentity struct {
	typeOf    reflect.Type
	pointer   uintptr
	value     any
	byPointer bool
	valid     bool
}

type incrementalTypeDeclarationIdentity struct {
	owner   *incrementalEnvironmentAuthority
	entries []incrementalTypeDeclarationEntry
	seal    *incrementalTypeDeclarationIdentity
}

type incrementalTypeDeclarationEntry struct {
	name   string
	typeOf reflect.Type
}

func newIncrementalEnvironmentAuthority(
	owner *incrementalRenderState,
	graph *incremental.Graph,
) *incrementalEnvironmentAuthority {
	authority := &incrementalEnvironmentAuthority{
		owner: owner, graph: graph, executor: newIncrementalEnvironmentExecutorIdentity(owner.engine),
	}
	authority.seal = authority
	return authority
}

func newIncrementalEnvironmentExecutorIdentity(value any) incrementalEnvironmentExecutorIdentity {
	reflected := reflect.ValueOf(value)
	if !reflected.IsValid() {
		return incrementalEnvironmentExecutorIdentity{}
	}
	identity := incrementalEnvironmentExecutorIdentity{typeOf: reflected.Type()}
	if reflected.Kind() == reflect.Pointer {
		if !reflected.IsNil() {
			identity.pointer = reflected.Pointer()
			identity.byPointer = true
			identity.valid = true
		}
	} else if reflected.Type().Comparable() {
		identity.value = value
		identity.valid = true
	}
	return identity
}

func (i incrementalEnvironmentExecutorIdentity) matches(value any) bool {
	reflected := reflect.ValueOf(value)
	if !i.valid || !reflected.IsValid() || reflected.Type() != i.typeOf {
		return false
	}
	if i.byPointer {
		return reflected.Kind() == reflect.Pointer && !reflected.IsNil() && reflected.Pointer() == i.pointer
	}
	return reflected.Type().Comparable() && i.value == value
}

func (s *incrementalRenderState) authenticateEnvironment(types map[string]reflect.Type) error {
	if s.environmentErr != nil {
		return s.environmentErr
	}
	authority := s.environment
	if authority == nil || authority.seal != authority || authority.owner != s ||
		authority.graph == nil || authority.graph != s.graph || !authority.executor.matches(s.engine) {
		return s.failEnvironment(errors.New("incremental environment identity has invalid provenance"))
	}
	if authority.declarations == nil {
		declarations, err := newIncrementalTypeDeclarationIdentity(authority, types)
		if err != nil {
			return s.failEnvironment(err)
		}
		authority.declarations = declarations
		return nil
	}
	if err := authority.declarations.validate(authority, types); err != nil {
		return s.failEnvironment(err)
	}
	return nil
}

func (s *incrementalRenderState) failEnvironment(err error) error {
	s.environmentErr = err
	return err
}

func newIncrementalTypeDeclarationIdentity(
	owner *incrementalEnvironmentAuthority,
	types map[string]reflect.Type,
) (*incrementalTypeDeclarationIdentity, error) {
	entries, err := incrementalTypeDeclarationEntries(types)
	if err != nil {
		return nil, err
	}
	identity := &incrementalTypeDeclarationIdentity{owner: owner, entries: entries}
	identity.seal = identity
	return identity, nil
}

func (i *incrementalTypeDeclarationIdentity) validate(
	owner *incrementalEnvironmentAuthority,
	types map[string]reflect.Type,
) error {
	if i == nil || i.seal != i || i.owner != owner {
		return errors.New("incremental environment type declaration identity has invalid provenance")
	}
	entries, err := incrementalTypeDeclarationEntries(types)
	if err != nil {
		return err
	}
	if !slices.Equal(i.entries, entries) {
		return errors.New("incremental environment type declarations changed")
	}
	return nil
}

func incrementalTypeDeclarationEntries(
	types map[string]reflect.Type,
) ([]incrementalTypeDeclarationEntry, error) {
	names := make([]string, 0, len(types))
	for name, typeOf := range types {
		if name == "" || typeOf == nil {
			return nil, fmt.Errorf("incremental environment has an invalid type declaration %q", name)
		}
		names = append(names, name)
	}
	slices.Sort(names)
	entries := make([]incrementalTypeDeclarationEntry, len(names))
	for index, name := range names {
		entries[index] = incrementalTypeDeclarationEntry{name: name, typeOf: types[name]}
	}
	return entries, nil
}
