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

package queryidentity

import (
	"hash/maphash"
	"sync"

	"gitlab.com/haproxy-haptic/haptic/pkg/incremental"
)

const rootShardCount = 64

// Fields are the exact values encoded by one component query key.
type Fields struct {
	Component string
	Source    string
	Namespace string
	Name      string
}

type rootShard[O comparable] struct {
	mu      sync.RWMutex
	current map[incremental.QueryKey]*root[O]
}

type rootSet[O comparable] struct {
	seed   maphash.Seed
	shards [rootShardCount]rootShard[O]
}

type authorityProof[O comparable] struct {
	owner      *Authority[O]
	ownerToken O
	roots      *rootSet[O]
	seed       maphash.Seed
	seal       *authorityProof[O]
}

// Authority owns opaque query identities for one render session.
type Authority[O comparable] struct {
	owner O
	roots *rootSet[O]
	proof *authorityProof[O]
}

type rootProof[O comparable] struct {
	owner      *root[O]
	authority  *Authority[O]
	ownerToken O
	key        incremental.QueryKey
	fields     Fields
	seal       *rootProof[O]
}

type root[O comparable] struct {
	authority *Authority[O]
	owner     O
	key       incremental.QueryKey
	fields    Fields
	proof     rootProof[O]
	seal      *root[O]
}

// NewAuthority creates an isolated query-identity owner.
func NewAuthority[O comparable](owner O) *Authority[O] {
	roots := &rootSet[O]{seed: maphash.MakeSeed()}
	authority := &Authority[O]{owner: owner, roots: roots}
	proof := &authorityProof[O]{owner: authority, ownerToken: owner, roots: roots, seed: roots.seed}
	proof.seal = proof
	authority.proof = proof
	return authority
}

// Register replaces the current generation for key with a distinct exact identity.
func (a *Authority[O]) Register(owner O, key incremental.QueryKey, fields Fields) bool {
	opaque := key.Opaque()
	if !a.valid(owner) || opaque == "" {
		return false
	}
	value := &root[O]{authority: a, owner: owner, key: key, fields: fields}
	value.proof = rootProof[O]{owner: value, authority: a, ownerToken: owner, key: key, fields: fields}
	value.proof.seal = &value.proof
	value.seal = value

	shard := a.roots.shard(key)
	shard.mu.Lock()
	if shard.current == nil {
		shard.current = make(map[incremental.QueryKey]*root[O])
	}
	shard.current[key] = value
	shard.mu.Unlock()
	return true
}

// Lookup returns fields only for the current authenticated generation of key.
func (a *Authority[O]) Lookup(owner O, key incremental.QueryKey) (Fields, bool) {
	opaque := key.Opaque()
	if !a.valid(owner) || opaque == "" {
		return Fields{}, false
	}
	shard := a.roots.shard(key)
	shard.mu.RLock()
	value := shard.current[key]
	valid := value != nil && value.seal == value && value.authority == a && value.owner == owner
	if valid {
		proof := &value.proof
		valid = proof.seal == proof && proof.owner == value && proof.authority == a && proof.ownerToken == owner &&
			proof.key == value.key && proof.fields == value.fields && value.key == key
	}
	fields := valueFields(value, valid)
	shard.mu.RUnlock()
	return fields, valid
}

func (r *rootSet[O]) shard(key incremental.QueryKey) *rootShard[O] {
	index := maphash.Comparable(r.seed, key) % rootShardCount
	return &r.shards[index]
}

func valueFields[O comparable](value *root[O], valid bool) Fields {
	if !valid {
		return Fields{}
	}
	return value.fields
}

func (a *Authority[O]) valid(owner O) bool {
	return a != nil && a.roots != nil && a.proof != nil && a.proof.seal == a.proof &&
		a.roots.seed != (maphash.Seed{}) && a.owner == owner && a.proof.owner == a &&
		a.proof.ownerToken == owner && a.proof.roots == a.roots && a.proof.seed == a.roots.seed
}
