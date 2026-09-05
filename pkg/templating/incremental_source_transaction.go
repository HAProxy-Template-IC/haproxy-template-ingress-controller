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
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"maps"
	"reflect"
	"slices"
	"sync"
	"sync/atomic"

	"gitlab.com/haproxy-haptic/scriggo"
	"gitlab.com/haproxy-haptic/scriggo/native"
)

type preparedIncrementalSourceTransactionsInput struct {
	seal          *preparedIncrementalSourceTransactionsInput
	proof         *incrementalSourceTransactionTopologyProof
	revoked       atomic.Bool
	authority     *native.VectorAuthority
	sourceSeals   []*incrementalVectorContextSeal
	childSeals    []*incrementalVectorContextSeal
	sourceStarts  []int
	sourceEnds    []int
	childStarts   []int
	childEnds     []int
	childIndexes  []int
	childLanes    []int
	templateNames []string
	shapes        []IncrementalComponentSourceTransactionWave
}

type incrementalSourceTransactionTopologyProof struct {
	seal               *incrementalSourceTransactionTopologyProof
	owner              *preparedIncrementalSourceTransactionsInput
	shapes             immutableCertificateSliceIdentity
	transactions       []immutableCertificateSliceIdentity
	transactionHeaders immutableCertificateSliceIdentity
	children           []immutableCertificateSliceIdentity
	childHeaders       immutableCertificateSliceIdentity
	sourceStarts       immutableCertificateSliceIdentity
	sourceEnds         immutableCertificateSliceIdentity
	childStarts        immutableCertificateSliceIdentity
	childEnds          immutableCertificateSliceIdentity
	childIndexes       immutableCertificateSliceIdentity
	childLanes         immutableCertificateSliceIdentity
	templateNames      immutableCertificateSliceIdentity
	sourceSeals        immutableCertificateSliceIdentity
	childSeals         immutableCertificateSliceIdentity
	digest             [sha256.Size]byte
}

type preparedIncrementalSourceTransactionWave struct {
	start                     int
	bindings                  map[string]any
	contexts                  []context.Context
	children                  []incrementalSourceTransactionChildContext
	nativeFunctionTrampolines []*native.FunctionTrampoline
}

type incrementalSourceTransactionChildContext struct {
	index int
	ctx   context.Context
}

type incrementalSourceTransactionResourceFacadeIdentity struct {
	typeOf  reflect.Type
	pointer uintptr
}

type incrementalSourceTransactionController struct {
	sources   *incrementalSourceTransactionSources
	lifecycle IncrementalComponentSourceTransactionLifecycle
	prepared  *preparedIncrementalSourceTransactionsInput
	ctx       context.Context
	carrier   *incrementalVectorCarrier

	mu         sync.Mutex
	nextWave   int
	activeWave int
	waveLoaded bool
	waveSealed bool
}

type incrementalSourceTransactionSources struct {
	authority *native.VectorAuthority
	lifecycle IncrementalComponentVectorLifecycle
	writer    *incrementalVectorSegmentWriter
	sources   []*incrementalVectorContextSeal
	children  []*incrementalVectorContextSeal
	contexts  []context.Context
	starts    []int
	ends      []int
	indexes   []int
	nextChild []int
	completed []bool

	mu           sync.Mutex
	activeSource int
	activeChild  int
	nextSource   int
	failureChild int
	terminal     error
	aborted      bool
	vmAborted    bool
	vmAbort      int
}

var incrementalSourceTransactionControllerTrampolines = []*native.FunctionTrampoline{
	native.MakeMethodTrampolineWithFrame(
		reflect.TypeFor[*incrementalSourceTransactionController](),
		"BeginWave",
		func(args []reflect.Value) []reflect.Value {
			args[0].Interface().(*incrementalSourceTransactionController).BeginWave(
				args[1].Interface().(native.Env),
				int(args[2].Int()),
			)
			return nil
		},
		func(frame native.FunctionCallFrame) {
			frame.ArgValue(0).Interface().(*incrementalSourceTransactionController).BeginWave(
				frame.ArgEnv(1),
				int(frame.ArgInt(2)),
			)
		},
	),
	native.MakeMethodTrampolineWithFrame(
		reflect.TypeFor[*incrementalSourceTransactionController](),
		"EndWave",
		func(args []reflect.Value) []reflect.Value {
			args[0].Interface().(*incrementalSourceTransactionController).EndWave(
				args[1].Interface().(native.Env),
				int(args[2].Int()),
			)
			return nil
		},
		func(frame native.FunctionCallFrame) {
			frame.ArgValue(0).Interface().(*incrementalSourceTransactionController).EndWave(
				frame.ArgEnv(1),
				int(frame.ArgInt(2)),
			)
		},
	),
}

func (e *ScriggoEngine) IncrementalComponentSourceTransactionsEligibility() bool {
	return e != nil && validIncrementalVectorCarrier(e, e.incrementalVectorCarrier) &&
		e.incrementalVectorCarrier.sourceTransactionTemplate != nil &&
		e.incrementalVectorCarrier.sourceTransactionErr == nil
}

func (e *ScriggoEngine) RenderIncrementalComponentSourceTransactions(
	ctx context.Context,
	input IncrementalComponentSourceTransactionsInput,
) (err error) {
	carrier := e.incrementalVectorCarrier
	if !validIncrementalVectorCarrier(e, carrier) || carrier.sourceTransactionTemplate == nil ||
		carrier.sourceTransactionErr != nil {
		if carrier != nil && carrier.sourceTransactionErr != nil {
			return fmt.Errorf("incremental source transaction carrier is unavailable: %w", carrier.sourceTransactionErr)
		}
		return errors.New("incremental source transaction carrier is unavailable")
	}
	prepared, err := prepareIncrementalSourceTransactionsInput(ctx, carrier, input)
	if err != nil {
		abortIncrementalVectorInput(input.Lifecycle, err)
		return err
	}
	sources := newIncrementalSourceTransactionSources(prepared, input.Lifecycle)
	controller := &incrementalSourceTransactionController{
		sources: sources, lifecycle: input.Lifecycle, prepared: prepared,
		ctx: ctx, carrier: carrier, activeWave: -1,
	}
	defer func() {
		if recovered := recover(); recovered != nil {
			cause := fmt.Errorf("incremental source transaction panic: %v", recovered)
			failure := sources.failureIndex()
			sources.abort(cause)
			err = &IncrementalComponentBatchError{Index: failure, Err: cause}
			return
		}
		if err != nil {
			sources.abort(err)
		}
	}()
	values := maps.Clone(input.SharedContext)
	if values == nil {
		values = make(map[string]any, 9)
	}
	values[incrementalSourceTransactionStartsName] = prepared.sourceStarts
	values[incrementalSourceTransactionEndsName] = prepared.sourceEnds
	values[incrementalSourceChildStartsName] = prepared.childStarts
	values[incrementalSourceChildEndsName] = prepared.childEnds
	values[incrementalSourceChildIndexesName] = prepared.childIndexes
	values[incrementalSourceChildLanesName] = prepared.childLanes
	values[incrementalVectorRuntimeName] = controller
	boundary := native.NewVectorBoundary()
	fiberBoundary := native.NewVectorFiberBoundary()
	values[incrementalVectorBoundaryName] = boundary
	values[incrementalSourceFiberBoundaryName] = fiberBoundary
	bindingNames := make([]string, len(carrier.bindings))
	for index, binding := range carrier.bindings {
		bindingNames[index] = binding.name
	}
	runOptions := &scriggo.RunOptions{
		Context:                   ctx,
		Deterministic:             true,
		ObserveMutationContext:    observeIncrementalVectorMutation,
		ObserveNativeCallContext:  observeIncrementalVectorNativeCall,
		BeforeNativeCallContext:   beforeIncrementalNativeCall,
		NativeFunctionTrampolines: incrementalSourceTransactionNativeFunctionTrampolines(nil),
		Vector: &scriggo.VectorRunOptions{
			Authority: prepared.authority, Count: len(prepared.sourceSeals),
			DeferredBindings: bindingNames, VMNative: true,
			Boundary: boundary, Lifecycle: sources,
			FiberBoundary: fiberBoundary, FiberLifecycle: sources,
		},
	}
	if err = runScriggoTemplate(
		ctx,
		incrementalSourceTransactionTemplatePath,
		carrier.sourceTransactionTemplate,
		sources.writer,
		values,
		runOptions,
	); err != nil {
		failure := sources.failureIndex()
		cause := err
		if failure >= 0 && failure < len(prepared.templateNames) {
			cause = remapIncrementalVectorCarrierError(prepared.templateNames[failure], err)
		}
		return &IncrementalComponentBatchError{Index: failure, Err: cause}
	}
	if err = controller.complete(); err != nil {
		return &IncrementalComponentBatchError{Index: sources.failureIndex(), Err: err}
	}
	return nil
}

func incrementalSourceTransactionNativeFunctionTrampolines(
	base []*native.FunctionTrampoline,
) []*native.FunctionTrampoline {
	result := make(
		[]*native.FunctionTrampoline,
		0,
		len(incrementalSourceTransactionControllerTrampolines)+
			len(incrementalNativeFunctionFrameTrampolines)+len(base),
	)
	result = append(result, incrementalSourceTransactionControllerTrampolines...)
	result = append(result, incrementalNativeFunctionFrameTrampolines...)
	return append(result, base...)
}

func prepareIncrementalSourceTransactionsInput(
	ctx context.Context,
	carrier *incrementalVectorCarrier,
	input IncrementalComponentSourceTransactionsInput,
) (*preparedIncrementalSourceTransactionsInput, error) {
	if ctx == nil || isNilValue(input.Lifecycle) || len(input.Waves) == 0 {
		return nil, errors.New("incremental source transaction input is incomplete")
	}
	prepared := &preparedIncrementalSourceTransactionsInput{
		authority:    native.NewVectorAuthority(),
		sourceStarts: make([]int, len(input.Waves)),
		sourceEnds:   make([]int, len(input.Waves)),
		shapes:       cloneIncrementalSourceTransactionWaves(input.Waves),
	}
	childCount := 0
	for waveIndex := range prepared.shapes {
		var err error
		childCount, err = prepared.appendSourceTransactionWave(carrier, waveIndex, childCount)
		if err != nil {
			return nil, err
		}
	}
	if len(prepared.childStarts) == 0 || childCount != len(prepared.childIndexes) {
		return nil, errors.New("incremental source transaction topology is not canonical")
	}
	prepared.templateNames = make([]string, childCount)
	seen := make([]bool, childCount)
	for offset, child := range prepared.childIndexes {
		if child >= childCount || seen[child] {
			return nil, errors.New("incremental source transaction repeats a child index")
		}
		seen[child] = true
		prepared.templateNames[child] = carrier.entryPoints[prepared.childLanes[offset]]
	}
	prepared.sourceSeals = newIncrementalVectorContextSeals(len(prepared.childStarts))
	prepared.childSeals = newIncrementalVectorContextSeals(childCount)
	prepared.sealTopology()
	if err := prepared.authenticateTopology(); err != nil {
		return nil, err
	}
	return prepared, nil
}

func (p *preparedIncrementalSourceTransactionsInput) appendSourceTransactionWave(
	carrier *incrementalVectorCarrier,
	waveIndex int,
	childCount int,
) (int, error) {
	p.sourceStarts[waveIndex] = len(p.childStarts)
	for transactionIndex, transaction := range p.shapes[waveIndex].Transactions {
		if len(transaction.Children) == 0 {
			return 0, fmt.Errorf("incremental source transaction wave %d row %d is empty", waveIndex, transactionIndex)
		}
		p.childStarts = append(p.childStarts, len(p.childIndexes))
		for _, child := range transaction.Children {
			lane, found := carrier.laneByName[child.TemplateName]
			if !found || carrier.entryPoints[lane] != child.TemplateName || child.Index < 0 {
				return 0, fmt.Errorf("incremental source transaction child %q is invalid", child.TemplateName)
			}
			p.childIndexes = append(p.childIndexes, child.Index)
			p.childLanes = append(p.childLanes, lane)
			childCount = max(childCount, child.Index+1)
		}
		p.childEnds = append(p.childEnds, len(p.childIndexes))
	}
	p.sourceEnds[waveIndex] = len(p.childStarts)
	return childCount, nil
}

func cloneIncrementalSourceTransactionWaves(
	waves []IncrementalComponentSourceTransactionWave,
) []IncrementalComponentSourceTransactionWave {
	owned := make([]IncrementalComponentSourceTransactionWave, len(waves))
	for waveIndex := range waves {
		if waves[waveIndex].Transactions == nil {
			continue
		}
		owned[waveIndex].Transactions = make(
			[]IncrementalComponentSourceTransaction,
			len(waves[waveIndex].Transactions),
		)
		for transactionIndex := range waves[waveIndex].Transactions {
			owned[waveIndex].Transactions[transactionIndex].Children = slices.Clone(
				waves[waveIndex].Transactions[transactionIndex].Children,
			)
		}
	}
	return owned
}

func (p *preparedIncrementalSourceTransactionsInput) sealTopology() {
	proof := &incrementalSourceTransactionTopologyProof{
		owner:         p,
		shapes:        immutableCertificateSlice(p.shapes),
		sourceStarts:  immutableCertificateSlice(p.sourceStarts),
		sourceEnds:    immutableCertificateSlice(p.sourceEnds),
		childStarts:   immutableCertificateSlice(p.childStarts),
		childEnds:     immutableCertificateSlice(p.childEnds),
		childIndexes:  immutableCertificateSlice(p.childIndexes),
		childLanes:    immutableCertificateSlice(p.childLanes),
		templateNames: immutableCertificateSlice(p.templateNames),
		sourceSeals:   immutableCertificateSlice(p.sourceSeals),
		childSeals:    immutableCertificateSlice(p.childSeals),
	}
	proof.transactions = make([]immutableCertificateSliceIdentity, len(p.shapes))
	for waveIndex := range p.shapes {
		proof.transactions[waveIndex] = immutableCertificateSlice(p.shapes[waveIndex].Transactions)
		for transactionIndex := range p.shapes[waveIndex].Transactions {
			proof.children = append(proof.children, immutableCertificateSlice(
				p.shapes[waveIndex].Transactions[transactionIndex].Children,
			))
		}
	}
	proof.transactionHeaders = immutableCertificateSlice(proof.transactions)
	proof.childHeaders = immutableCertificateSlice(proof.children)
	proof.digest = incrementalSourceTransactionTopologyDigest(p)
	proof.seal = proof
	p.proof = proof
	p.seal = p
}

func (p *preparedIncrementalSourceTransactionsInput) authenticateTopology() error {
	if p == nil || p.revoked.Load() {
		return errors.New("incremental source transaction topology is revoked")
	}
	fail := func() error {
		p.revoked.Store(true)
		return errors.New("incremental source transaction topology has invalid provenance")
	}
	proof := p.proof
	if p.seal != p || proof == nil || proof.seal != proof || proof.owner != p || p.authority == nil ||
		!p.topologyHeadersAuthentic(proof) {
		return fail()
	}
	if !p.topologyShapesAuthentic(proof) {
		return fail()
	}
	if incrementalSourceTransactionTopologyDigest(p) != proof.digest {
		return fail()
	}
	if !authenticIncrementalVectorContextSeals(p.sourceSeals) ||
		!authenticIncrementalVectorContextSeals(p.childSeals) {
		return fail()
	}
	return nil
}

func (p *preparedIncrementalSourceTransactionsInput) topologyHeadersAuthentic(
	proof *incrementalSourceTransactionTopologyProof,
) bool {
	return immutableCertificateSlice(p.shapes) == proof.shapes &&
		immutableCertificateSlice(proof.transactions) == proof.transactionHeaders &&
		immutableCertificateSlice(proof.children) == proof.childHeaders &&
		immutableCertificateSlice(p.sourceStarts) == proof.sourceStarts &&
		immutableCertificateSlice(p.sourceEnds) == proof.sourceEnds &&
		immutableCertificateSlice(p.childStarts) == proof.childStarts &&
		immutableCertificateSlice(p.childEnds) == proof.childEnds &&
		immutableCertificateSlice(p.childIndexes) == proof.childIndexes &&
		immutableCertificateSlice(p.childLanes) == proof.childLanes &&
		immutableCertificateSlice(p.templateNames) == proof.templateNames &&
		immutableCertificateSlice(p.sourceSeals) == proof.sourceSeals &&
		immutableCertificateSlice(p.childSeals) == proof.childSeals &&
		len(proof.transactions) == len(p.shapes)
}

func (p *preparedIncrementalSourceTransactionsInput) topologyShapesAuthentic(
	proof *incrementalSourceTransactionTopologyProof,
) bool {
	childHeader := 0
	for waveIndex := range p.shapes {
		if immutableCertificateSlice(p.shapes[waveIndex].Transactions) != proof.transactions[waveIndex] {
			return false
		}
		for transactionIndex := range p.shapes[waveIndex].Transactions {
			if childHeader >= len(proof.children) || immutableCertificateSlice(
				p.shapes[waveIndex].Transactions[transactionIndex].Children,
			) != proof.children[childHeader] {
				return false
			}
			childHeader++
		}
	}
	return childHeader == len(proof.children)
}

func authenticIncrementalVectorContextSeals(seals []*incrementalVectorContextSeal) bool {
	for index, seal := range seals {
		if seal == nil || seal.seal != seal || seal.index != index {
			return false
		}
	}
	return true
}

func incrementalSourceTransactionTopologyDigest(
	p *preparedIncrementalSourceTransactionsInput,
) [sha256.Size]byte {
	hasher := sha256.New()
	writeInt := func(value int) {
		_ = binary.Write(hasher, binary.LittleEndian, int64(value))
	}
	writeString := func(value string) {
		writeInt(len(value))
		_, _ = hasher.Write([]byte(value))
	}
	writeInts := func(values []int) {
		writeInt(len(values))
		for _, value := range values {
			writeInt(value)
		}
	}
	writeInts(p.sourceStarts)
	writeInts(p.sourceEnds)
	writeInts(p.childStarts)
	writeInts(p.childEnds)
	writeInts(p.childIndexes)
	writeInts(p.childLanes)
	writeInt(len(p.templateNames))
	for _, name := range p.templateNames {
		writeString(name)
	}
	writeInt(len(p.shapes))
	for _, wave := range p.shapes {
		writeInt(len(wave.Transactions))
		for _, transaction := range wave.Transactions {
			writeInt(len(transaction.Children))
			for _, child := range transaction.Children {
				writeString(child.TemplateName)
				writeInt(child.Index)
			}
		}
	}
	var digest [sha256.Size]byte
	hasher.Sum(digest[:0])
	return digest
}

func newIncrementalVectorContextSeals(count int) []*incrementalVectorContextSeal {
	seals := make([]*incrementalVectorContextSeal, count)
	for index := range seals {
		seal := &incrementalVectorContextSeal{index: index}
		seal.seal = seal
		seals[index] = seal
	}
	return seals
}

func newIncrementalSourceTransactionSources(
	prepared *preparedIncrementalSourceTransactionsInput,
	lifecycle IncrementalComponentVectorLifecycle,
) *incrementalSourceTransactionSources {
	return &incrementalSourceTransactionSources{
		authority: prepared.authority, lifecycle: lifecycle,
		writer:  newIncrementalVectorSegmentWriter(len(prepared.childSeals)),
		sources: prepared.sourceSeals, children: prepared.childSeals,
		contexts: make([]context.Context, len(prepared.childSeals)),
		starts:   prepared.childStarts, ends: prepared.childEnds, indexes: prepared.childIndexes,
		nextChild: slices.Clone(prepared.childStarts), completed: make([]bool, len(prepared.childSeals)),
		activeSource: -1, activeChild: -1, failureChild: -1,
	}
}

func (s *incrementalSourceTransactionSources) Begin(ctx context.Context, source int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.terminal != nil {
		return s.terminal
	}
	if source != s.nextSource || s.activeSource >= 0 || source < 0 || source >= len(s.sources) {
		return s.failLocked(fmt.Errorf("incremental source transaction row %d cannot begin", source))
	}
	seal, _ := ctx.Value(incrementalVectorContextKey{}).(*incrementalVectorContextSeal)
	if seal == nil || seal.seal != seal || seal.index != source || s.sources[source] != seal {
		return s.failLocked(errors.New("incremental source transaction context has invalid provenance"))
	}
	s.activeSource = source
	return nil
}

func (s *incrementalSourceTransactionSources) Finish(source int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.terminal != nil {
		return s.terminal
	}
	if source != s.activeSource || s.activeChild >= 0 {
		return s.failLocked(fmt.Errorf("incremental source transaction row %d cannot finish", source))
	}
	for offset := s.starts[source]; offset < s.ends[source]; offset++ {
		if !s.completed[s.indexes[offset]] {
			return s.failLocked(fmt.Errorf("incremental source transaction child %d did not complete", s.indexes[offset]))
		}
	}
	return nil
}

func (s *incrementalSourceTransactionSources) Commit(source int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.terminal != nil {
		return s.terminal
	}
	if source != s.activeSource || source != s.nextSource || s.activeChild >= 0 {
		return s.failLocked(fmt.Errorf("incremental source transaction row %d cannot commit", source))
	}
	s.activeSource = -1
	s.nextSource++
	return nil
}

func (s *incrementalSourceTransactionSources) Abort(_ int, cause error) {
	s.mu.Lock()
	if s.aborted || s.vmAborted {
		s.mu.Unlock()
		return
	}
	s.vmAborted = true
	s.vmAbort = s.activeChild
	_ = s.failLocked(cause)
	s.activeChild = -1
	s.activeSource = -1
	s.writer.abort()
	s.mu.Unlock()
}

func (s *incrementalSourceTransactionSources) BeginChild(
	source, child int,
) (context.Context, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.terminal != nil {
		return nil, s.terminal
	}
	if source != s.activeSource || s.activeChild >= 0 || !s.ownsChildLocked(source, child) ||
		s.nextChild[source] >= s.ends[source] || s.indexes[s.nextChild[source]] != child ||
		s.completed[child] {
		return nil, s.failLocked(fmt.Errorf("incremental source transaction child %d cannot begin", child))
	}
	ctx := s.contexts[child]
	seal, _ := ctx.Value(incrementalVectorContextKey{}).(*incrementalVectorContextSeal)
	if ctx == nil || seal == nil || seal.seal != seal || seal.index != child || s.children[child] != seal {
		return nil, s.failLocked(errors.New("incremental source transaction child context has invalid provenance"))
	}
	s.failureChild = child
	if err := s.lifecycle.Begin(child); err != nil {
		return nil, s.failLocked(err)
	}
	if err := s.writer.begin(child); err != nil {
		s.lifecycle.Abort(child, err)
		return nil, s.failLocked(err)
	}
	s.activeChild = child
	return ctx, nil
}

func (s *incrementalSourceTransactionSources) EndChild(source, child int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.terminal != nil {
		return s.terminal
	}
	if source != s.activeSource || child != s.activeChild || !s.ownsChildLocked(source, child) {
		return s.failLocked(fmt.Errorf("incremental source transaction child %d cannot end", child))
	}
	output, err := s.writer.end(child)
	if err != nil {
		return s.failLocked(err)
	}
	if err := s.lifecycle.End(child, output); err != nil {
		return s.failLocked(err)
	}
	s.completed[child] = true
	s.nextChild[source]++
	s.activeChild = -1
	return nil
}

func (s *incrementalSourceTransactionSources) ownsChildLocked(source, child int) bool {
	if source < 0 || source >= len(s.starts) || child < 0 || child >= len(s.completed) {
		return false
	}
	for offset := s.starts[source]; offset < s.ends[source]; offset++ {
		if s.indexes[offset] == child {
			return true
		}
	}
	return false
}

func (s *incrementalSourceTransactionSources) loadChildren(
	children []incrementalSourceTransactionChildContext,
) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.terminal != nil || s.activeSource >= 0 || s.activeChild >= 0 {
		return errors.New("incremental source transaction child contexts cannot be loaded")
	}
	for _, child := range children {
		if child.index < 0 || child.index >= len(s.contexts) || child.ctx == nil || s.contexts[child.index] != nil {
			return s.failLocked(errors.New("incremental source transaction child context has invalid provenance"))
		}
		seal, _ := child.ctx.Value(incrementalVectorContextKey{}).(*incrementalVectorContextSeal)
		if seal == nil || seal.seal != seal || seal.index != child.index || s.children[child.index] != seal {
			return s.failLocked(errors.New("incremental source transaction child context has invalid provenance"))
		}
		s.contexts[child.index] = child.ctx
	}
	return nil
}

func (s *incrementalSourceTransactionSources) failureIndex() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return max(s.failureChild, 0)
}

func (s *incrementalSourceTransactionSources) complete() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.terminal != nil {
		return s.terminal
	}
	if s.activeSource >= 0 || s.activeChild >= 0 || s.nextSource != len(s.sources) || s.writer.active >= 0 {
		return s.failLocked(errors.New("incremental source transaction lifecycle did not complete"))
	}
	for child, completed := range s.completed {
		if !completed {
			return s.failLocked(fmt.Errorf("incremental source transaction child %d was omitted", child))
		}
	}
	return nil
}

func (s *incrementalSourceTransactionSources) abort(cause error) {
	s.mu.Lock()
	if s.aborted {
		s.mu.Unlock()
		return
	}
	if cause == nil {
		cause = errors.New("incremental source transaction aborted")
	}
	s.aborted = true
	_ = s.failLocked(cause)
	active := s.activeChild
	if s.vmAborted {
		active = s.vmAbort
	}
	s.activeChild = -1
	s.activeSource = -1
	s.writer.abort()
	s.mu.Unlock()
	s.lifecycle.Abort(active, cause)
}

func (s *incrementalSourceTransactionSources) failLocked(err error) error {
	if err == nil {
		err = errors.New("incremental source transaction failed")
	}
	if s.terminal == nil {
		s.terminal = err
	}
	return s.terminal
}

func (c *incrementalSourceTransactionController) BeginWave(env native.Env, wave int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.prepared == nil {
		env.Stop(errors.New("incremental source transaction topology is unavailable"))
		return
	}
	if err := c.prepared.authenticateTopology(); err != nil {
		env.Stop(err)
		return
	}
	shapes := c.prepared.shapes
	if wave != c.nextWave || c.activeWave >= 0 || wave < 0 || wave >= len(shapes) {
		env.Stop(fmt.Errorf("incremental source transaction wave %d is invalid", wave))
		return
	}
	batch, err := c.lifecycle.LoadSourceTransactionWave(c.ctx, wave)
	if err != nil {
		env.Stop(err)
		return
	}
	if len(shapes[wave].Transactions) == 0 {
		if len(batch.Bindings) != 0 || len(batch.Contexts) != 0 || len(batch.ChildContexts) != 0 {
			env.Stop(fmt.Errorf("incremental source transaction wave %d loaded unexpected rows", wave))
			return
		}
		c.activeWave = wave
		c.waveLoaded = true
		c.waveSealed = false
		return
	}
	prepared, err := prepareIncrementalSourceTransactionWave(c.carrier, shapes[wave], batch, c.sources)
	if err != nil {
		env.Stop(err)
		return
	}
	if err := c.sources.loadChildren(prepared.children); err != nil {
		env.Stop(err)
		return
	}
	vectorEnv, ok := env.(native.VectorEnv)
	if !ok {
		env.Stop(errors.New("incremental source transaction vector environment is unavailable"))
		return
	}
	if err := vectorEnv.LoadVectorRangeOwned(
		c.sources.authority,
		prepared.start,
		prepared.bindings,
		prepared.contexts,
		prepared.nativeFunctionTrampolines,
	); err != nil {
		env.Stop(err)
		return
	}
	c.activeWave = wave
	c.waveLoaded = true
	c.waveSealed = false
}

func (c *incrementalSourceTransactionController) EndWave(env native.Env, wave int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.prepared == nil {
		env.Stop(errors.New("incremental source transaction topology is unavailable"))
		return
	}
	if err := c.prepared.authenticateTopology(); err != nil {
		env.Stop(err)
		return
	}
	if wave != c.activeWave || wave != c.nextWave || !c.waveLoaded || c.waveSealed {
		env.Stop(fmt.Errorf("incremental source transaction wave %d cannot end", wave))
		return
	}
	if err := c.lifecycle.SealWave(wave); err != nil {
		env.Stop(err)
		return
	}
	c.waveSealed = true
	c.waveLoaded = false
	c.activeWave = -1
	c.nextWave++
}

func (c *incrementalSourceTransactionController) complete() error {
	c.mu.Lock()
	if c.prepared == nil {
		c.mu.Unlock()
		return errors.New("incremental source transaction topology is unavailable")
	}
	if err := c.prepared.authenticateTopology(); err != nil {
		c.mu.Unlock()
		return err
	}
	complete := c.nextWave == len(c.prepared.shapes) && c.activeWave < 0 && !c.waveLoaded && c.waveSealed
	c.mu.Unlock()
	if !complete {
		return errors.New("incremental source transaction wave lifecycle did not complete")
	}
	return c.sources.complete()
}

func prepareIncrementalSourceTransactionWave(
	carrier *incrementalVectorCarrier,
	shape IncrementalComponentSourceTransactionWave,
	batch IncrementalComponentSourceTransactionBatch,
	sources *incrementalSourceTransactionSources,
) (*preparedIncrementalSourceTransactionWave, error) {
	count := len(shape.Transactions)
	if carrier == nil || sources == nil || count == 0 || len(batch.Contexts) != count ||
		len(batch.Bindings) != len(carrier.bindings) {
		return nil, errors.New("incremental source transaction loaded wave does not match its shape")
	}
	childCount := 0
	for _, transaction := range shape.Transactions {
		childCount += len(transaction.Children)
	}
	if len(batch.ChildContexts) != childCount {
		return nil, errors.New("incremental source transaction loaded child contexts do not match its shape")
	}
	columns, bindings, err := prepareIncrementalSourceTransactionBindings(carrier, batch, count)
	if err != nil {
		return nil, err
	}
	start := sources.nextSource
	contexts := make([]context.Context, count)
	children := make([]incrementalSourceTransactionChildContext, 0, childCount)
	nativeFunctions := newIncrementalResourceNativeFunctionCollector(nil)
	resourceFacades := make(map[incrementalSourceTransactionResourceFacadeIdentity]struct{})
	childOffset := 0
	for rowIndex, transaction := range shape.Transactions {
		bound, err := prepareIncrementalSourceTransactionRow(
			batch.Contexts[rowIndex], sources, columns, start, rowIndex,
		)
		if err != nil {
			return nil, err
		}
		contexts[rowIndex] = bound
		if err := collectIncrementalSourceTransactionResources(
			columns, rowIndex, resourceFacades, nativeFunctions,
		); err != nil {
			return nil, err
		}
		for _, child := range transaction.Children {
			childCtx := batch.ChildContexts[childOffset]
			childOffset++
			if child.Index < 0 || child.Index >= len(sources.children) || childCtx == nil {
				return nil, errors.New("incremental source transaction child context is invalid")
			}
			boundChild, err := bindIncrementalVectorContext(childCtx, sources.children[child.Index])
			if err != nil {
				return nil, fmt.Errorf("incremental source transaction child %d: %w", child.Index, err)
			}
			children = append(children, incrementalSourceTransactionChildContext{index: child.Index, ctx: boundChild})
		}
	}
	return &preparedIncrementalSourceTransactionWave{
		start: start, bindings: bindings, contexts: contexts, children: children,
		nativeFunctionTrampolines: nativeFunctions.trampolines,
	}, nil
}

func prepareIncrementalSourceTransactionBindings(
	carrier *incrementalVectorCarrier,
	batch IncrementalComponentSourceTransactionBatch,
	count int,
) (columns map[string]reflect.Value, bindings map[string]any, err error) {
	columns = make(map[string]reflect.Value, len(carrier.bindings))
	bindings = make(map[string]any, len(carrier.bindings))
	for _, binding := range carrier.bindings {
		column, exists := batch.Bindings[binding.name]
		if !exists {
			return nil, nil, fmt.Errorf("incremental source transaction binding %q is missing", binding.name)
		}
		value := reflect.ValueOf(column)
		if !value.IsValid() || value.Kind() != reflect.Slice || value.Len() != count {
			return nil, nil, fmt.Errorf(
				"incremental source transaction binding %q must be an owned %s slice of length %d",
				binding.name, binding.variableType, count,
			)
		}
		if err := validateIncrementalVectorColumn(binding.name, value, binding.variableType); err != nil {
			return nil, nil, fmt.Errorf("incremental source transaction binding %q: %w", binding.name, err)
		}
		columns[binding.name] = value
		owned, err := ownedIncrementalSourceTransactionColumn(value, binding, count)
		if err != nil {
			return nil, nil, err
		}
		bindings[binding.name] = owned.Interface()
	}
	for name := range batch.Bindings {
		if _, exists := columns[name]; !exists {
			return nil, nil, fmt.Errorf("incremental source transaction binding %q is not eligible", name)
		}
	}
	return columns, bindings, nil
}

func ownedIncrementalSourceTransactionColumn(
	value reflect.Value,
	binding incrementalVectorBinding,
	count int,
) (reflect.Value, error) {
	if value.Type().Elem() == binding.variableType {
		return value, nil
	}
	owned := reflect.MakeSlice(reflect.SliceOf(binding.variableType), count, count)
	for index := range count {
		normalized, err := normalizeIncrementalVectorValue(value.Index(index), binding.variableType)
		if err != nil {
			return reflect.Value{}, fmt.Errorf(
				"incremental source transaction binding %q item %d: %w", binding.name, index, err,
			)
		}
		owned.Index(index).Set(normalized)
	}
	return owned, nil
}

func prepareIncrementalSourceTransactionRow(
	rowCtx context.Context,
	sources *incrementalSourceTransactionSources,
	columns map[string]reflect.Value,
	start, rowIndex int,
) (context.Context, error) {
	if rowCtx == nil {
		return nil, fmt.Errorf("incremental source transaction row %d context is nil", rowIndex)
	}
	if err := validateIncrementalVectorItemContext(rowCtx, columns, rowIndex); err != nil {
		return nil, fmt.Errorf("incremental source transaction row %d: %w", rowIndex, err)
	}
	globalSource := start + rowIndex
	if globalSource < 0 || globalSource >= len(sources.sources) {
		return nil, errors.New("incremental source transaction row index is invalid")
	}
	bound, err := bindIncrementalVectorContext(rowCtx, sources.sources[globalSource])
	if err != nil {
		return nil, fmt.Errorf("incremental source transaction row %d: %w", rowIndex, err)
	}
	return bound, nil
}

func collectIncrementalSourceTransactionResources(
	columns map[string]reflect.Value,
	rowIndex int,
	resourceFacades map[incrementalSourceTransactionResourceFacadeIdentity]struct{},
	nativeFunctions *incrementalResourceNativeFunctionCollector,
) error {
	resources := columns[declResources].Index(rowIndex)
	if resources.Kind() != reflect.Pointer || resources.IsNil() {
		return fmt.Errorf("incremental source transaction row %d resources are invalid", rowIndex)
	}
	resourceIdentity := incrementalSourceTransactionResourceFacadeIdentity{
		typeOf: resources.Type(), pointer: resources.Pointer(),
	}
	if _, seen := resourceFacades[resourceIdentity]; !seen {
		resourceFacades[resourceIdentity] = struct{}{}
		nativeFunctions.add(resources.Interface())
	}
	return nil
}
