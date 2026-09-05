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
	"reflect"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/renderoutput"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/renderplan"
	"gitlab.com/haproxy-haptic/haptic/pkg/persistenttree"
)

type exactCycleCurrentConfigServer struct {
	address string
	port    int64
}

type exactCycleCurrentConfigProjection = persistenttree.Tree[*persistenttree.Tree[exactCycleCurrentConfigServer]]

type exactCycleCurrentConfigRootAuthentication struct {
	projection *exactCycleCurrentConfigProjection
	plan       *renderplan.Snapshot
}

func currentConfigRootForPlan(
	plan *renderplan.Plan,
	previous *exactCycleCurrentConfigRoot,
) *exactCycleCurrentConfigRoot {
	if plan == nil {
		return nil
	}
	projection := currentConfigProjection(plan.Backends)
	candidate := sealCurrentConfigRoot(projection, nil)
	if previous == nil || previous.validate() != nil {
		return candidate
	}
	left, leftErr := previous.materialize()
	right, rightErr := candidate.materialize()
	if leftErr == nil && rightErr == nil && reflect.DeepEqual(left, right) {
		return previous
	}
	return candidate
}

func currentConfigRootForOutputTransition(
	previousRoot *exactCycleCurrentConfigRoot,
	previousOutput *renderoutput.Snapshot,
	nextOutput *renderoutput.Snapshot,
	delta *renderplan.Delta,
	fullPlan *renderplan.Plan,
) (*exactCycleCurrentConfigRoot, error) {
	if nextOutput == nil {
		return nil, errors.New("currentConfig output is nil")
	}
	nextPlan, err := nextOutput.PlanSnapshot()
	if err != nil {
		return nil, err
	}
	if previousOutput == nil || delta == nil {
		if fullPlan == nil {
			return nil, errors.New("currentConfig full plan is unavailable")
		}
		return sealCurrentConfigRoot(currentConfigProjection(fullPlan.Backends), nextPlan), nil
	}
	previousPlan, err := previousOutput.PlanSnapshot()
	if err != nil {
		return nil, err
	}
	if nextPlan == previousPlan {
		if previousRoot == nil || previousRoot.plan != previousPlan {
			return nil, errors.New("currentConfig root does not match the previous plan")
		}
		return previousRoot, nil
	}
	if previousRoot == nil || previousRoot.plan != previousPlan {
		return nil, errors.New("currentConfig root does not match the previous plan")
	}
	if err := previousRoot.validate(); err != nil {
		return nil, err
	}
	applied, err := delta.Apply(previousPlan)
	if err != nil || applied != nextPlan {
		return nil, errors.Join(errors.New("currentConfig plan delta does not match the output"), err)
	}
	changes, err := delta.Changes()
	if err != nil {
		return nil, err
	}
	projection := applyCurrentConfigBackendChanges(previousRoot.projection, changes.Backends)
	return sealCurrentConfigRoot(projection, nextPlan), nil
}

func applyCurrentConfigBackendChanges(
	previous *exactCycleCurrentConfigProjection,
	changes []renderplan.NamedChange[renderplan.Backend],
) *exactCycleCurrentConfigProjection {
	transaction := previous.Txn()
	for index := range changes {
		change := &changes[index]
		if change.After == nil || len(change.After.Servers) == 0 {
			transaction.Delete([]byte(change.Name))
			continue
		}
		servers := currentConfigServers(change.After.Servers)
		previousServers, found := transaction.Get([]byte(change.Name))
		if found && currentConfigServerTreesEqual(previousServers, servers) {
			continue
		}
		transaction.Insert([]byte(change.Name), servers)
	}
	return transaction.Commit()
}

func currentConfigServerTreesEqual(
	left *persistenttree.Tree[exactCycleCurrentConfigServer],
	right *persistenttree.Tree[exactCycleCurrentConfigServer],
) bool {
	if left == right {
		return true
	}
	if left == nil || right == nil || left.Len() != right.Len() {
		return false
	}
	equal := true
	left.Root().Walk(func(name string, server exactCycleCurrentConfigServer) bool {
		candidate, found := right.Root().Get([]byte(name))
		if !found || candidate != server {
			equal = false
			return true
		}
		return false
	})
	return equal
}

func currentConfigProjection(
	backends map[string]renderplan.Backend,
) *exactCycleCurrentConfigProjection {
	transaction := persistenttree.New[*persistenttree.Tree[exactCycleCurrentConfigServer]]().Txn()
	for name := range backends {
		backend := backends[name]
		if len(backend.Servers) == 0 {
			continue
		}
		transaction.Insert([]byte(name), currentConfigServers(backend.Servers))
	}
	return transaction.Commit()
}

func currentConfigServers(
	servers []renderplan.Server,
) *persistenttree.Tree[exactCycleCurrentConfigServer] {
	transaction := persistenttree.New[exactCycleCurrentConfigServer]().Txn()
	for index := range servers {
		server := &servers[index]
		transaction.Insert([]byte(server.Name), exactCycleCurrentConfigServer{
			address: server.Address,
			port:    int64(server.Port),
		})
	}
	return transaction.Commit()
}

func sealCurrentConfigRoot(
	projection *exactCycleCurrentConfigProjection,
	plan *renderplan.Snapshot,
) *exactCycleCurrentConfigRoot {
	root := &exactCycleCurrentConfigRoot{projection: projection, plan: plan}
	root.auth = exactCycleCurrentConfigRootAuthentication{projection: projection, plan: plan}
	root.seal = root
	return root
}

func (r *exactCycleCurrentConfigRoot) validate() error {
	if r == nil || r.seal != r || r.projection == nil ||
		r.auth.projection != r.projection || r.auth.plan != r.plan {
		return errors.New("currentConfig root has invalid provenance")
	}
	if r.plan != nil {
		return r.plan.ValidateAuthentication()
	}
	return nil
}

func (r *exactCycleCurrentConfigRoot) materialize() (renderplan.CurrentConfig, error) {
	if err := r.validate(); err != nil {
		return renderplan.CurrentConfig{}, err
	}
	current := renderplan.CurrentConfig{
		ServerIndex: make(map[string]map[string]renderplan.ServerAddr, r.projection.Len()),
	}
	r.projection.Root().Walk(func(backend string, servers *persistenttree.Tree[exactCycleCurrentConfigServer]) bool {
		materialized := make(map[string]renderplan.ServerAddr, servers.Len())
		servers.Root().Walk(func(name string, server exactCycleCurrentConfigServer) bool {
			port := server.port
			materialized[name] = renderplan.ServerAddr{Address: server.address, Port: &port}
			return false
		})
		current.ServerIndex[backend] = materialized
		return false
	})
	return current, nil
}
