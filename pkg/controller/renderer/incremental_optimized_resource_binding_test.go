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
	"log/slog"
	"reflect"
	"runtime"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/rendercontext"
	"gitlab.com/haproxy-haptic/haptic/pkg/core/config"
	k8sstore "gitlab.com/haproxy-haptic/haptic/pkg/k8s/store"
	"gitlab.com/haproxy-haptic/haptic/pkg/stores"
	"gitlab.com/haproxy-haptic/haptic/pkg/templating"
)

func TestOptimizedIncrementalResourceBindingRejectsRetainedGeneration(t *testing.T) {
	session := &incrementalRenderSession{}
	batch, authority, engine := prepareOptimizedIncrementalResourceBinding(
		t,
		session,
		`{{ len(resources.routes.List()) }}`,
	)
	preparedA := prepareOptimizedIncrementalResourceComponent(t, session, batch, authority, "A")
	preparedB := prepareOptimizedIncrementalResourceComponent(t, session, batch, authority, "B")
	readerA := preparedA.reader.(*batchCapabilityTrackingReader)
	readerB := preparedB.reader.(*batchCapabilityTrackingReader)
	readerB.input = readerA.input
	retainedA, err := batchCapabilityResourceCall(preparedA.templateContext["resources"], "List")
	require.NoError(t, err)
	_, err = batchCapabilityResourceCall(preparedA.templateContext["resources"], "Fetch")
	require.ErrorContains(t, err, "resources.routes.Fetch is unavailable")
	_, err = batchCapabilityResourceCall(preparedA.templateContext["resources"], "GetSingle")
	require.ErrorContains(t, err, "resources.routes.GetSingle is unavailable")

	var crossGenerationErr error
	outputs, err := engine.RenderIncrementalComponents(
		t.Context(),
		"component",
		[]templating.IncrementalComponentBatchItem{
			{
				Context:         preparedA.ctx,
				TemplateContext: preparedA.templateContext,
				Activate:        preparedA.activate,
				Deactivate:      preparedA.deactivate,
			},
			{
				Context:         preparedB.ctx,
				TemplateContext: preparedB.templateContext,
				Activate: func() error {
					if activateErr := preparedB.activate(); activateErr != nil {
						return activateErr
					}
					crossGenerationErr = retainedA(preparedB.ctx)
					return nil
				},
				Deactivate: preparedB.deactivate,
			},
		},
	)
	require.NoError(t, err)
	assert.Equal(t, []string{"1", "1"}, outputs)
	require.ErrorContains(t, crossGenerationErr, "outside incremental component generation")
	assert.Equal(t, int64(1), readerA.exactReads.Load())
	assert.Equal(t, int64(1), readerB.exactReads.Load())

	afterBatchErr := retainedA(preparedA.ctx)
	require.ErrorContains(t, afterBatchErr, "outside incremental component generation")
	assert.Equal(t, int64(1), readerA.exactReads.Load(), "rejected retained calls reached A's reader")
	assert.Equal(t, int64(1), readerB.exactReads.Load(), "a retained A facade registered a B dependency")

	_, err = session.finishPreparedComponent(preparedA, outputs[0])
	require.Error(t, err, "a swallowed retained-facade rejection must poison only A")
	_, err = session.finishPreparedComponent(preparedB, outputs[1])
	require.NoError(t, err)
}

func TestOptimizedIncrementalResourceBindingRejectsEquivalentRootBeforeActivation(t *testing.T) {
	for _, test := range []struct {
		name        string
		replacement func(any, any) any
	}{
		{
			name: "equivalent shallow copy",
			replacement: func(_ any, prepared any) any {
				value := reflect.ValueOf(prepared)
				clone := reflect.New(value.Elem().Type())
				clone.Elem().Set(value.Elem())
				return clone.Interface()
			},
		},
		{
			name: "shared batch root",
			replacement: func(batch any, _ any) any {
				return batch
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			session := &incrementalRenderSession{}
			batch, authority, engine := prepareOptimizedIncrementalResourceBinding(
				t,
				session,
				`{{ len(resources.routes.List()) }}`,
			)
			prepared := prepareOptimizedIncrementalResourceComponent(t, session, batch, authority, "A")
			reader := prepared.reader.(*batchCapabilityTrackingReader)
			prepared.templateContext["resources"] = test.replacement(
				batch.resources,
				prepared.templateContext["resources"],
			)
			var activated atomic.Bool

			_, err := engine.RenderIncrementalComponents(
				t.Context(),
				"component",
				[]templating.IncrementalComponentBatchItem{{
					Context:         prepared.ctx,
					TemplateContext: prepared.templateContext,
					Activate: func() error {
						activated.Store(true)
						return prepared.activate()
					},
					Deactivate: prepared.deactivate,
				}},
			)
			require.ErrorContains(t, err, "does not match resources")
			assert.False(t, activated.Load(), "forged resources reached component activation")
			assert.Zero(t, reader.exactReads.Load(), "forged resources reached the reader")
		})
	}
}

func TestOptimizedIncrementalResourceBindingExpandsWholeValueEscapes(t *testing.T) {
	tests := []struct {
		name             string
		source           string
		wantRoutes       bool
		wantServices     bool
		wantAllCallables bool
	}{
		{
			name:             "whole member",
			source:           `{% store := resources.routes %}{{ len(store.List()) }}`,
			wantRoutes:       true,
			wantAllCallables: true,
		},
		{
			name:             "whole root",
			source:           `{{ tostring(resources) }}`,
			wantRoutes:       true,
			wantServices:     true,
			wantAllCallables: true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			session := newOptimizedIncrementalResourceBindingSession()
			batch, authority, _ := prepareOptimizedIncrementalResourceBinding(t, session, test.source)
			prepared := prepareOptimizedIncrementalResourceComponent(t, session, batch, authority, "A")
			resources := prepared.templateContext["resources"]

			assert.Equal(t, test.wantRoutes, optimizedResourceMemberAvailable(resources, "Routes"))
			assert.Equal(t, test.wantServices, optimizedResourceMemberAvailable(resources, "Services"))
			if test.wantAllCallables {
				for _, member := range []string{"Routes", "Services"} {
					if !optimizedResourceMemberAvailable(resources, member) {
						continue
					}
					for _, callable := range []string{"List", "Fetch", "GetSingle"} {
						assert.True(t, optimizedResourceCallableAvailable(resources, member, callable),
							"%s.%s", member, callable)
					}
				}
			}
		})
	}
}

func TestOptimizedIncrementalResourceBindingRevocationDrainsAcceptedCall(t *testing.T) {
	session := &incrementalRenderSession{}
	batch, authority, _ := prepareOptimizedIncrementalResourceBinding(
		t,
		session,
		`{{ len(resources.routes.List()) }}`,
	)
	reader := newBatchCapabilityBlockingReader("A")
	prepared := prepareBatchCapabilityGenerationComponentWithReader(
		t,
		session,
		batch,
		authority,
		&incrementalComponent{name: "A", entryPoint: "component"},
		reader,
	)
	call, err := batchCapabilityResourceCall(prepared.templateContext["resources"], "List")
	require.NoError(t, err)
	require.NoError(t, prepared.activate())
	callDone := make(chan error, 1)
	go func() { callDone <- call(prepared.ctx) }()
	<-reader.started

	revoked := make(chan struct{})
	go func() {
		prepared.deactivate()
		close(revoked)
	}()
	for prepared.lease.state.Load() != incrementalCapabilityLeaseRevoking {
		runtime.Gosched()
	}
	select {
	case <-revoked:
		t.Fatal("revocation completed before the accepted resource call drained")
	default:
	}

	close(reader.release)
	require.NoError(t, <-callDone)
	<-revoked
	assert.Equal(t, int64(1), reader.exactReads.Load())
	_, err = session.finishPreparedComponent(prepared, "stable")
	require.NoError(t, err, "an accepted call that drains during revocation must not poison publication")
}

func TestOptimizedIncrementalResourceBindingKeepsConcurrentGenerationsIsolated(t *testing.T) {
	session := &incrementalRenderSession{}
	batch, authority, _ := prepareOptimizedIncrementalResourceBinding(
		t,
		session,
		`{{ len(resources.routes.List()) }}`,
	)
	preparedA := prepareOptimizedIncrementalResourceComponent(t, session, batch, authority, "A")
	preparedB := prepareOptimizedIncrementalResourceComponent(t, session, batch, authority, "B")
	preparedB.reader.(*batchCapabilityTrackingReader).input =
		preparedA.reader.(*batchCapabilityTrackingReader).input
	callA, err := batchCapabilityResourceCall(preparedA.templateContext["resources"], "List")
	require.NoError(t, err)
	callB, err := batchCapabilityResourceCall(preparedB.templateContext["resources"], "List")
	require.NoError(t, err)
	require.NoError(t, preparedA.activate())
	require.NoError(t, preparedB.activate())

	assert.NotSame(t, preparedA.lease.derivedResolver, preparedB.lease.derivedResolver)
	assert.NotSame(t, preparedA.lease.derived, preparedB.lease.derived)
	errs := make(chan error, 2)
	go func() { errs <- callA(preparedA.ctx) }()
	go func() { errs <- callB(preparedB.ctx) }()
	require.NoError(t, <-errs)
	require.NoError(t, <-errs)
	preparedA.deactivate()
	preparedB.deactivate()

	assert.Equal(t, int64(1), preparedA.reader.(*batchCapabilityTrackingReader).exactReads.Load())
	assert.Equal(t, int64(1), preparedB.reader.(*batchCapabilityTrackingReader).exactReads.Load())
	_, err = session.finishPreparedComponent(preparedA, "A")
	require.NoError(t, err)
	_, err = session.finishPreparedComponent(preparedB, "B")
	require.NoError(t, err)
}

func prepareOptimizedIncrementalResourceBinding(
	t *testing.T,
	session *incrementalRenderSession,
	source string,
) (*incrementalBatchCapabilities, *incrementalCapabilityAuthority, *templating.ScriggoEngine) {
	t.Helper()
	batch, authority := prepareBatchCapabilityGenerationBatch(t, session)
	templating.RegisterIncrementalResourceDeclaration(batch.resources)
	engine, err := templating.New(map[string]string{"component": source}, &templating.Options{
		EntryPoints:            []string{"component"},
		IncrementalEntryPoints: []string{"component"},
		Declarations:           map[string]any{"resources": batch.resources},
	})
	require.NoError(t, err)
	session.state.engine = engine
	return batch, authority, engine
}

func prepareOptimizedIncrementalResourceComponent(
	t *testing.T,
	session *incrementalRenderSession,
	batch *incrementalBatchCapabilities,
	authority *incrementalCapabilityAuthority,
	name string,
) *preparedIncrementalComponent {
	t.Helper()
	return prepareBatchCapabilityGenerationComponent(
		t,
		session,
		batch,
		authority,
		&incrementalComponent{name: name, entryPoint: "component"},
	)
}

func newOptimizedIncrementalResourceBindingSession() *incrementalRenderSession {
	resourceConfig := func(resources string) config.WatchedResource {
		return config.WatchedResource{
			APIVersion: "example.test/v1",
			Resources:  resources,
			IndexBy:    []string{"metadata.namespace", "metadata.name"},
		}
	}
	routes := k8sstore.NewMemoryStore(2)
	services := k8sstore.NewMemoryStore(2)
	return &incrementalRenderSession{
		state: &incrementalRenderState{config: &config.Config{WatchedResources: map[string]config.WatchedResource{
			"routes":   resourceConfig("routes"),
			"services": resourceConfig("services"),
		}}},
		stores: map[string]stores.Store{"routes": routes, "services": services},
		baseContext: map[string]any{"controller": map[string]templating.ResourceStore{
			"routes": &rendercontext.StoreWrapper{
				Store: routes, ResourceType: "routes", Logger: slog.Default(),
				IndexBy: []string{"metadata.namespace", "metadata.name"},
			},
		}},
	}
}

func optimizedResourceMemberAvailable(resources any, member string) bool {
	value := reflect.ValueOf(resources)
	if !value.IsValid() || value.Kind() != reflect.Pointer || value.IsNil() ||
		value.Elem().Kind() != reflect.Struct {
		return false
	}
	field := value.Elem().FieldByName(member)
	return field.IsValid() && field.Kind() == reflect.Pointer && !field.IsNil()
}

func optimizedResourceCallableAvailable(resources any, member, callable string) bool {
	if !optimizedResourceMemberAvailable(resources, member) {
		return false
	}
	resource := reflect.ValueOf(resources).Elem().FieldByName(member).Elem()
	field := resource.FieldByName(callable)
	return field.IsValid() && field.Kind() == reflect.Func && !field.IsNil()
}
