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

package templating

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"gitlab.com/haproxy-haptic/scriggo/native"
)

type exactCycleEffectKind uint8

const (
	exactCycleEffectTimeBucket exactCycleEffectKind = iota + 1
	exactCycleEffectRandomBytes
)

type exactCycleEffectObservation struct {
	kind          exactCycleEffectKind
	scope         string
	ordinal       uint64
	globalOrdinal uint64
	callPath      string
	callLine      int
	integerArg    int64
	stringArg     string
	result        string
	expiresAt     time.Time
	expiresAtUnix int64
	generation    uint64
}

type exactCycleEffectAttempt struct {
	mu             sync.Mutex
	owner          *ExactCycleReplayInputs
	program        *ExactCycleReplayProgram
	generation     uint64
	ordinals       map[string]uint64
	nextEffect     uint64
	effects        []exactCycleEffectObservation
	invocations    []ExactCycleRootInvocation
	invocationAuth []ExactCycleRootInvocation
	nextRoot       int
	rootActive     bool
	failed         bool
	finalized      bool
}

type exactCycleEffectContextKey struct{}

// Begin starts an attempt-owned observation stream for one candidate generation.
func (p *ExactCycleReplayProgram) Begin(
	ctx context.Context,
	generation uint64,
	templateContext map[string]any,
) (context.Context, *ExactCycleReplayInputs, error) {
	invocations := make([]ExactCycleRootInvocation, len(p.rootEntryPoints))
	for index, name := range p.rootEntryPoints {
		invocations[index] = ExactCycleRootInvocation{Kind: "template", Name: name}
	}
	return p.BeginWithInvocations(ctx, generation, templateContext, invocations)
}

// BeginWithInvocations starts a candidate against the exact ordered artifact plan.
func (p *ExactCycleReplayProgram) BeginWithInvocations(
	ctx context.Context,
	generation uint64,
	templateContext map[string]any,
	invocations []ExactCycleRootInvocation,
) (context.Context, *ExactCycleReplayInputs, error) {
	if generation == 0 {
		return nil, nil, errors.New("exact cycle replay generation is zero")
	}
	if err := p.validate(); err != nil {
		return nil, nil, err
	}
	inputs, err := p.captureAmbient(generation, templateContext)
	if err != nil {
		return nil, nil, err
	}
	if len(invocations) != len(p.rootEntryPoints) {
		return nil, nil, errors.New("exact cycle replay root invocation plan is incomplete")
	}
	for index := range invocations {
		if invocations[index].Kind == "" || invocations[index].Name != p.rootEntryPoints[index] {
			return nil, nil, fmt.Errorf("exact cycle replay root invocation %d is invalid", index)
		}
	}
	attempt := &exactCycleEffectAttempt{
		owner: inputs, program: p, generation: generation, ordinals: make(map[string]uint64),
		invocations: slices.Clone(invocations), invocationAuth: slices.Clone(invocations),
	}
	inputs.attempt = attempt
	ctx = context.WithValue(ctx, exactCycleEffectContextKey{}, attempt)
	execution := &exactCycleReplayExecution{program: p, attempt: attempt}
	execution.seal = execution
	return context.WithValue(ctx, exactCycleReplayExecutionContextKey{}, execution), inputs, nil
}

// Finalize seals every effect after all root entry points have returned.
func (s *ExactCycleReplayInputs) Finalize() error {
	if s == nil || s.program == nil || s.attempt == nil {
		return errors.New("exact cycle replay inputs have no active effect attempt")
	}
	attempt := s.attempt
	attempt.mu.Lock()
	defer attempt.mu.Unlock()
	if attempt.owner != s || attempt.program != s.program || attempt.generation != s.generation ||
		attempt.finalized || attempt.failed || s.finalized || s.seal != nil ||
		attempt.rootActive || attempt.nextRoot != len(attempt.invocations) ||
		!slices.Equal(attempt.invocations, attempt.invocationAuth) ||
		len(attempt.invocations) != len(s.program.rootEntryPoints) {
		return errors.New("exact cycle replay effect attempt has invalid provenance")
	}
	attempt.finalized = true
	s.effects = slices.Clone(attempt.effects)
	sort.Slice(s.effects, func(i, j int) bool {
		return compareExactCycleEffects(&s.effects[i], &s.effects[j]) < 0
	})
	s.effectAuth = slices.Clone(s.effects)
	s.roots = slices.Clone(attempt.invocations)
	s.rootAuth = slices.Clone(s.roots)
	s.finalized = true
	s.seal = s
	return s.validate(s.program)
}

// Generation returns the attempt generation authenticated by this input snapshot.
func (s *ExactCycleReplayInputs) Generation() (uint64, error) {
	if s == nil || s.program == nil {
		return 0, errors.New("exact cycle replay inputs have invalid provenance")
	}
	if err := s.validate(s.program); err != nil {
		return 0, err
	}
	return s.generation, nil
}

func (p *ExactCycleReplayProgram) captureAmbient(
	generation uint64,
	templateContext map[string]any,
) (*ExactCycleReplayInputs, error) {
	if p.owner.profilingEnabled || p.owner.IsTracingEnabled() || p.owner.IsFilterDebugEnabled() {
		return nil, errors.New("exact cycle replay is unavailable while template instrumentation is enabled")
	}
	values := make([]exactCycleReplayAmbientValue, len(p.ambientNames))
	auth := make([]exactCycleReplayAmbientValue, len(p.ambientNames))
	for index, name := range p.ambientNames {
		value, found := templateContext[name]
		cloned, err := cloneIncrementalSerialization(value)
		if err != nil {
			return nil, fmt.Errorf("exact cycle replay ambient %q: %w", name, err)
		}
		authenticated, err := cloneIncrementalSerialization(cloned)
		if err != nil {
			return nil, fmt.Errorf("authenticating exact cycle replay ambient %q: %w", name, err)
		}
		values[index] = exactCycleReplayAmbientValue{name: name, found: found, value: cloned}
		auth[index] = exactCycleReplayAmbientValue{name: name, found: found, value: authenticated}
	}
	protocols := make([]exactCycleReplayProtocolValue, len(p.protocolNames))
	protocolAuth := make([]exactCycleReplayProtocolValue, len(p.protocolNames))
	for index, name := range p.protocolNames {
		state, err := captureExactCycleProtocolState(name, templateContext)
		if err != nil {
			return nil, err
		}
		protocols[index] = exactCycleReplayProtocolValue{name: name, state: state}
		protocolAuth[index] = exactCycleReplayProtocolValue{name: name, state: state}
	}
	return &ExactCycleReplayInputs{
		program: p, generation: generation, values: values, auth: auth,
		protocols: protocols, protocolAuth: protocolAuth,
	}, nil
}

func compareExactCycleEffects(left, right *exactCycleEffectObservation) int {
	if left.globalOrdinal < right.globalOrdinal {
		return -1
	}
	if left.globalOrdinal > right.globalOrdinal {
		return 1
	}
	return strings.Compare(left.scope, right.scope)
}

func equalExactCycleEffect(left, right *exactCycleEffectObservation) bool {
	return left.kind == right.kind && left.scope == right.scope && left.ordinal == right.ordinal &&
		left.globalOrdinal == right.globalOrdinal &&
		left.callPath == right.callPath && left.callLine == right.callLine && left.integerArg == right.integerArg &&
		left.stringArg == right.stringArg && left.result == right.result &&
		left.expiresAt.Equal(right.expiresAt) &&
		left.expiresAtUnix == right.expiresAtUnix && left.generation == right.generation
}

func validateExactCycleEffect(effect *exactCycleEffectObservation, generation uint64) error {
	if effect.scope == "" || effect.ordinal == 0 || effect.globalOrdinal == 0 ||
		effect.callPath == "" || effect.callLine <= 0 ||
		effect.generation == 0 || effect.generation != generation {
		return errors.New("exact cycle replay effect has invalid provenance")
	}
	switch effect.kind {
	case exactCycleEffectTimeBucket:
		if effect.integerArg <= 0 || effect.stringArg == "" || effect.expiresAt.IsZero() ||
			effect.expiresAt.UnixNano() != effect.expiresAtUnix {
			return errors.New("exact cycle replay time bucket is invalid")
		}
	case exactCycleEffectRandomBytes:
		if effect.integerArg < 0 || int64(len(effect.result)) != effect.integerArg ||
			!effect.expiresAt.IsZero() || effect.expiresAtUnix != 0 {
			return errors.New("exact cycle replay random result is invalid")
		}
	default:
		return errors.New("exact cycle replay effect kind is invalid")
	}
	return nil
}

func exactCycleEffectAttemptFromEnv(env native.Env, function string) (*exactCycleEffectAttempt, bool) {
	value := env.Context().Value(exactCycleEffectContextKey{})
	if value == nil {
		return nil, false
	}
	attempt, ok := value.(*exactCycleEffectAttempt)
	if !ok || attempt == nil || attempt.owner == nil {
		env.Stop(fmt.Errorf("%s has an invalid exact-cycle render attempt", function))
		return nil, true
	}
	return attempt, true
}

func (a *exactCycleEffectAttempt) append(
	env native.Env,
	kind exactCycleEffectKind,
	integerArg int64,
	stringArg string,
	result string,
	expiresAt time.Time,
) bool {
	scope, ok := IncrementalScope(env.Context())
	if !ok {
		env.Stop(errors.New("exact-cycle effect has no root scope"))
		return false
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.finalized || a.owner == nil || a.program == nil || a.generation == 0 ||
		a.owner.program != a.program || a.owner.generation != a.generation {
		env.Stop(errors.New("exact-cycle effect attempt is no longer active"))
		return false
	}
	expiresAtUnix := int64(0)
	if !expiresAt.IsZero() {
		expiresAtUnix = expiresAt.UnixNano()
	}
	a.ordinals[scope]++
	a.nextEffect++
	a.effects = append(a.effects, exactCycleEffectObservation{
		kind: kind, scope: scope, ordinal: a.ordinals[scope], globalOrdinal: a.nextEffect,
		callPath: env.CallPath(), callLine: env.CallLine(),
		integerArg: integerArg, stringArg: stringArg, result: result, expiresAt: expiresAt,
		expiresAtUnix: expiresAtUnix, generation: a.generation,
	})
	return true
}

func scriggoCycleTimeBucket(env native.Env, seconds int64, layout string) string {
	if seconds <= 0 || seconds > int64((365*24*time.Hour)/time.Second) || layout == "" {
		env.Stop(errors.New("cycleTimeBucket requires a layout and a duration between 1 second and 365 days"))
		return ""
	}
	now := time.Now()
	unix := now.Unix()
	result := exactCycleTimeBucketResult(now, seconds, layout)
	untilBoundary := time.Duration(seconds-unix%seconds)*time.Second - time.Duration(now.Nanosecond())
	expiresAt := now.Add(untilBoundary)
	attempt, observed := exactCycleEffectAttemptFromEnv(env, FuncCycleTimeBucket)
	if !observed {
		return result
	}
	if attempt == nil {
		return ""
	}
	if !attempt.append(env, exactCycleEffectTimeBucket, seconds, layout, result, expiresAt) {
		return ""
	}
	return result
}

func exactCycleTimeBucketResult(now time.Time, seconds int64, layout string) string {
	unix := now.Unix()
	bucketUnix := unix - unix%seconds
	return time.Unix(bucketUnix, 0).UTC().Format(layout)
}

func scriggoCycleRandomBytes(env native.Env, length int) string {
	if length < 0 || length > 1<<20 {
		env.Stop(errors.New("cycleRandomBytes length must be between 0 and 1048576"))
		return ""
	}
	result := scriggoRandBytes(length)
	if len(result) != length {
		env.Stop(errors.New("cycleRandomBytes failed to produce the requested bytes"))
		return ""
	}
	attempt, observed := exactCycleEffectAttemptFromEnv(env, FuncCycleRandomBytes)
	if !observed {
		return result
	}
	if attempt == nil {
		return ""
	}
	if !attempt.append(env, exactCycleEffectRandomBytes, int64(length), "", result, time.Time{}) {
		return ""
	}
	return result
}
