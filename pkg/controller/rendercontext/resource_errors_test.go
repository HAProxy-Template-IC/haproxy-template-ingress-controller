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

package rendercontext

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/testutil"
	"gitlab.com/haproxy-haptic/haptic/pkg/core/config"
	"gitlab.com/haproxy-haptic/haptic/pkg/stores"
	"gitlab.com/haproxy-haptic/haptic/pkg/stores/storetest"
	"gitlab.com/haproxy-haptic/haptic/pkg/templating"
)

func TestStoreWrapper_RecordsReadFailure(t *testing.T) {
	wantErr := errors.New("list unavailable")
	errorCollector := NewResourceErrorCollector()
	wrapper := &StoreWrapper{
		Store:          &storetest.MockStore{ListErr: wantErr},
		ResourceType:   "widgets",
		Logger:         testutil.NewTestLogger(),
		resourceErrors: errorCollector,
	}

	assert.Empty(t, wrapper.List())
	require.ErrorIs(t, errorCollector.Err(), wantErr)
	assert.Contains(t, errorCollector.Err().Error(), `resource "widgets" List failed`)
}

func TestStoreWrapper_RecordsAmbiguousGetSingle(t *testing.T) {
	errorCollector := NewResourceErrorCollector()
	wrapper := &StoreWrapper{
		Store: &storetest.MockStore{Items: []any{
			createResourceMap("first"),
			createResourceMap("second"),
		}},
		ResourceType:   "widgets",
		Logger:         testutil.NewTestLogger(),
		resourceErrors: errorCollector,
	}

	assert.Nil(t, wrapper.GetSingle("default", "shared"))
	require.Error(t, errorCollector.Err())
	assert.Contains(t, errorCollector.Err().Error(), "matched 2 objects; use Fetch or configure unique indexBy values")
}

func TestBuilder_RecordsTypedMaterializationFailure(t *testing.T) {
	elemType := reflect.StructOf([]reflect.StructField{
		{Name: "Count", Type: reflect.TypeOf(0), Tag: `json:"count"`},
	})
	cfg := &config.Config{
		WatchedResources: map[string]config.WatchedResource{
			"widgets": {IndexBy: []string{"metadata.namespace", "metadata.name"}},
		},
	}
	storeMap := map[string]stores.Store{
		"widgets": &storetest.MockStore{Items: []any{
			map[string]any{
				"metadata": map[string]any{"namespace": "default", "name": "bad"},
				"count":    "not-a-number",
			},
		}},
	}
	bctx := NewBuilder(
		t.Context(),
		cfg,
		&templating.PathResolver{},
		testutil.NewTestLogger(),
		WithStores(storeMap),
		WithTypedResources(map[string]reflect.Type{"widgets": elemType}),
	).Build()

	resources := reflect.ValueOf(bctx.Context["resources"]).Elem()
	list := resources.FieldByName("Widgets").Elem().FieldByName("List")
	result := list.Call(nil)[0]
	assert.Zero(t, result.Len())

	err := bctx.Err(t.Context())
	require.Error(t, err)
	assert.Contains(t, err.Error(), `resource "widgets" List could not materialize its typed object`)
}

func TestBuildResult_ErrPreservesContextCause(t *testing.T) {
	readErr := errors.New("read failed")
	collector := NewResourceErrorCollector()
	collector.Record(readErr)
	bctx := &BuildResult{ResourceErrors: collector}

	wantCause := errors.New("leader term ended")
	ctx, cancel := context.WithCancelCause(t.Context())
	cancel(wantCause)

	assert.Same(t, wantCause, bctx.Err(ctx))
}

func TestBuildResult_ErrClassifiesResourceInputFailure(t *testing.T) {
	wantErr := errors.New("read failed")
	collector := NewResourceErrorCollector()
	collector.Record(wantErr)
	bctx := &BuildResult{ResourceErrors: collector}

	err := bctx.Err(t.Context())
	var resourceInputErr *ResourceInputError
	require.ErrorAs(t, err, &resourceInputErr)
	assert.ErrorIs(t, err, wantErr)
}

func TestResourceErrorCollector_ConcurrentRecord(t *testing.T) {
	collector := NewResourceErrorCollector()
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func(value int) {
			defer wg.Done()
			collector.Record(fmt.Errorf("read-%d", value%4))
		}(i)
	}
	wg.Wait()

	err := collector.Err()
	require.Error(t, err)
	for i := 0; i < 4; i++ {
		assert.Contains(t, err.Error(), fmt.Sprintf("read-%d", i))
	}
}
