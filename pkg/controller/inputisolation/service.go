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

// Package inputisolation selects independently valid watched-resource updates.
package inputisolation

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"slices"
	"strings"
	"sync/atomic"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/pipeline"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/rendercontext"
	"gitlab.com/haproxy-haptic/haptic/pkg/core/config"
	k8sstore "gitlab.com/haproxy-haptic/haptic/pkg/k8s/store"
	"gitlab.com/haproxy-haptic/haptic/pkg/stores"
)

// Pipeline validates the complete rendered configuration and all auxiliary output.
type Pipeline interface {
	Execute(context.Context, stores.StoreProvider, rendercontext.RenderMode, ...rendercontext.Option) (*pipeline.PipelineResult, error)
}

// Rejection identifies a watched resource revision that has not been accepted.
type Rejection struct {
	Store     string `json:"store"`
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
	Deleted   bool   `json:"deleted"`
	Reason    string `json:"reason"`
	revision  stores.Revision
}

// Service retains validated inputs while independently valid changes advance.
type Service struct {
	pipeline     Pipeline
	watches      map[string]config.WatchedResource
	logger       *slog.Logger
	permit       chan struct{}
	accepted     atomic.Pointer[acceptedInputs]
	onRejections func(int)
}

type acceptedInputs struct {
	branches   map[string]*k8sstore.SnapshotBranch
	rejections []Rejection
}

type changeGroup struct {
	changes map[string][]k8sstore.BranchChange
	reason  error
}

type attempt struct {
	service      *Service
	ctx          context.Context
	mode         rendercontext.RenderMode
	opts         []rendercontext.Option
	branches     map[string]*k8sstore.SnapshotBranch
	result       *pipeline.PipelineResult
	rejected     []changeGroup
	initialError error
}

// New creates an input selector. The supplied pipeline must run full validation.
func New(validatingPipeline Pipeline, watches map[string]config.WatchedResource, logger *slog.Logger, onRejections ...func(int)) *Service {
	if logger == nil {
		logger = slog.Default()
	}
	service := &Service{pipeline: validatingPipeline, watches: maps.Clone(watches), logger: logger, permit: make(chan struct{}, 1)}
	if len(onRejections) > 0 {
		service.onRejections = onRejections[0]
	}
	return service
}

// Snapshot returns one atomically accepted input set and its rejection diagnostics.
func (s *Service) Snapshot() (stores.StoreProvider, []Rejection, bool) {
	current := s.accepted.Load()
	if current == nil {
		return nil, nil, false
	}
	return branchProvider(current.branches), slices.Clone(current.rejections), true
}

// AcceptedInputs supplies HTTP validation with one accepted snapshot.
func (s *Service) AcceptedInputs() (stores.StoreProvider, bool) {
	provider, _, ready := s.Snapshot()
	return provider, ready
}

// ObservedInputs projects live inputs onto the same revision family as reconciliation.
func (s *Service) ObservedInputs(ctx context.Context, observed stores.StoreProvider) (stores.StoreProvider, error) {
	base := s.baseBranches()
	groups, err := s.captureChanges(ctx, observed, base)
	if err != nil {
		return nil, err
	}
	return branchProvider(applyGroups(base, groups)), nil
}

// Execute validates observed updates, isolating failures against a validated baseline.
func (s *Service) Execute(ctx context.Context, observed stores.StoreProvider, mode rendercontext.RenderMode, opts ...rendercontext.Option) (*pipeline.PipelineResult, error) {
	select {
	case s.permit <- struct{}{}:
		defer func() { <-s.permit }()
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	base := s.baseBranches()
	groups, err := s.captureChanges(ctx, observed, base)
	if err != nil {
		return nil, err
	}
	a := &attempt{service: s, ctx: ctx, mode: mode, opts: opts, branches: base}
	candidate := applyGroups(base, groups)
	result, err := a.validate(candidate)
	if err == nil {
		return s.accept(ctx, candidate, result, nil)
	}
	if stopIsolation(ctx, err) {
		return nil, err
	}
	a.initialError = err
	a.result, err = a.validate(base)
	if err != nil {
		if stopIsolation(ctx, err) {
			return nil, err
		}
		if bootstrapErr := a.bootstrap(groups); bootstrapErr != nil {
			return nil, bootstrapErr
		}
	}
	if err := a.selectGroups(groups); err != nil {
		return nil, err
	}
	for len(a.rejected) > 0 {
		pending := a.rejected
		a.rejected = nil
		if err := a.selectGroups(pending); err != nil {
			return nil, err
		}
		if len(a.rejected) == len(pending) {
			if err := a.selectComplement(pending); err != nil {
				return nil, err
			}
			break
		}
	}
	rejections := s.describeRejections(a.rejected)
	return s.accept(ctx, a.branches, a.result, rejections)
}

func (s *Service) baseBranches() map[string]*k8sstore.SnapshotBranch {
	if accepted := s.accepted.Load(); accepted != nil {
		return accepted.branches
	}
	branches := make(map[string]*k8sstore.SnapshotBranch, len(s.watches))
	for alias := range s.watches {
		branches[alias] = k8sstore.NewSnapshotBranch(len(s.watches[alias].IndexBy))
	}
	return branches
}

func (s *Service) captureChanges(ctx context.Context, observed stores.StoreProvider, base map[string]*k8sstore.SnapshotBranch) ([]changeGroup, error) {
	snapshots, err := s.pinObservedStores(ctx, observed)
	if err != nil {
		return nil, err
	}
	grouped := map[string]*changeGroup{}
	for _, alias := range slices.Sorted(maps.Keys(s.watches)) {
		watch := s.watches[alias]
		changes, err := base[alias].Changes(ctx, snapshots[alias], watch.IndexBy)
		if err != nil {
			return nil, fmt.Errorf("capturing watched resource %q: %w", alias, err)
		}
		for _, change := range changes {
			apiGroup, _, qualified := strings.Cut(watch.APIVersion, "/")
			if !qualified {
				apiGroup = ""
			}
			identity := apiGroup + "/" + watch.Resources + "/" + change.Namespace() + "/" + change.Name()
			group := grouped[identity]
			if group == nil {
				group = &changeGroup{changes: map[string][]k8sstore.BranchChange{}}
				grouped[identity] = group
			}
			group.changes[alias] = append(group.changes[alias], change)
		}
	}
	groups := make([]changeGroup, 0, len(grouped))
	for _, key := range slices.Sorted(maps.Keys(grouped)) {
		groups = append(groups, *grouped[key])
	}
	return groups, nil
}

func applyGroups(base map[string]*k8sstore.SnapshotBranch, groups []changeGroup) map[string]*k8sstore.SnapshotBranch {
	changes := map[string][]k8sstore.BranchChange{}
	for _, group := range groups {
		for alias, entries := range group.changes {
			changes[alias] = append(changes[alias], entries...)
		}
	}
	result := maps.Clone(base)
	for alias, entries := range changes {
		result[alias] = result[alias].Apply(entries)
	}
	return result
}

func branchProvider(branches map[string]*k8sstore.SnapshotBranch) stores.StoreProvider {
	values := make(map[string]stores.Store, len(branches))
	for alias, branch := range branches {
		values[alias] = branch
	}
	return stores.NewRealStoreProvider(values)
}

func (a *attempt) validate(branches map[string]*k8sstore.SnapshotBranch) (*pipeline.PipelineResult, error) {
	if err := a.ctx.Err(); err != nil {
		return nil, err
	}
	return a.service.pipeline.Execute(a.ctx, branchProvider(branches), a.mode, a.opts...)
}

func (a *attempt) bootstrap(groups []changeGroup) error {
	ordered := slices.Clone(groups)
	slices.SortStableFunc(ordered, func(left, right changeGroup) int {
		return groupMentioned(right, a.initialError.Error()) - groupMentioned(left, a.initialError.Error())
	})
	for index := range ordered {
		remaining := append(slices.Clone(ordered[:index]), ordered[index+1:]...)
		candidate := applyGroups(a.branches, remaining)
		result, err := a.validate(candidate)
		if err == nil {
			a.branches, a.result = candidate, result
			return nil
		}
		if stopInputTrial(a.ctx, err) {
			return err
		}
	}
	return fmt.Errorf("observed inputs are invalid and no validated baseline is available: %w", a.initialError)
}

func groupMentioned(group changeGroup, message string) int {
	for _, changes := range group.changes {
		for _, change := range changes {
			if strings.Contains(message, change.Name()) {
				return 1
			}
		}
	}
	return 0
}

func (a *attempt) selectGroups(groups []changeGroup) error {
	if len(groups) == 0 {
		return nil
	}
	candidate := applyGroups(a.branches, groups)
	result, err := a.validate(candidate)
	if err == nil {
		a.branches, a.result = candidate, result
		return nil
	}
	if stopInputTrial(a.ctx, err) {
		return err
	}
	if len(groups) == 1 {
		group := groups[0]
		group.reason = err
		a.rejected = append(a.rejected, group)
		return nil
	}
	middle := len(groups) / 2
	if err := a.selectGroups(groups[:middle]); err != nil {
		return err
	}
	return a.selectGroups(groups[middle:])
}

// A dependency pair can straddle a bisection; retry it without each remaining offender.
func (a *attempt) selectComplement(groups []changeGroup) error {
	if len(groups) < 2 {
		return nil
	}
	for index := range groups {
		remaining := append(slices.Clone(groups[:index]), groups[index+1:]...)
		candidate := applyGroups(a.branches, remaining)
		result, err := a.validate(candidate)
		if err == nil {
			a.branches, a.result, a.rejected = candidate, result, nil
			return a.selectGroups(groups[index : index+1])
		}
		if stopInputTrial(a.ctx, err) {
			return err
		}
	}
	return nil
}

func stopIsolation(ctx context.Context, err error) bool {
	return stopInputTrial(ctx, err) || errors.Is(err, stores.ErrSnapshotChanged)
}

// A trial can need an unread retained revision that the API no longer serves.
func stopInputTrial(ctx context.Context, err error) bool {
	return ctx.Err() != nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) ||
		pipeline.WaitsForCriticalContent(err)
}

func (s *Service) describeRejections(groups []changeGroup) []Rejection {
	var rejected []Rejection
	for _, group := range groups {
		for _, alias := range slices.Sorted(maps.Keys(group.changes)) {
			for _, change := range group.changes[alias] {
				rejected = append(rejected, Rejection{Store: alias, Namespace: change.Namespace(), Name: change.Name(), Deleted: change.Deleted(), Reason: group.reason.Error(), revision: change.Revision()})
			}
		}
	}
	return rejected
}

func (s *Service) accept(ctx context.Context, branches map[string]*k8sstore.SnapshotBranch, result *pipeline.PipelineResult, rejected []Rejection) (*pipeline.PipelineResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if result == nil {
		return nil, errors.New("input validation returned no rendered result")
	}
	previous := s.accepted.Swap(&acceptedInputs{branches: branches, rejections: rejected})
	if s.onRejections != nil {
		s.onRejections(len(rejected))
	}
	if previous != nil && sameRejectedChanges(previous.rejections, rejected) {
		return result, nil
	}
	for _, rejection := range rejected {
		s.logger.Warn("Watched resource change rejected; other resources continue updating. Correct this resource to apply the change.",
			"store", rejection.Store, "namespace", rejection.Namespace, "name", rejection.Name, "deleted", rejection.Deleted, "reason", rejection.Reason, "diagnostics", "/debug/vars/inputRejections")
	}
	return result, nil
}

func sameRejectedChanges(left, right []Rejection) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		a, b := &left[index], &right[index]
		if a.Store != b.Store || a.Namespace != b.Namespace || a.Name != b.Name ||
			a.Deleted != b.Deleted || a.revision != b.revision {
			return false
		}
	}
	return true
}
