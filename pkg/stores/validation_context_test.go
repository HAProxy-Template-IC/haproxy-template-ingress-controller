// Copyright 2025 Philipp Hossner
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

package stores

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeHTTPOverlay is a test double for HTTPContentOverlay that lets tests
// control IsEmpty and the lookup behaviour without depending on
// pkg/httpstore (which would create a circular import).
type fakeHTTPOverlay struct {
	empty   bool
	pending []string
	content map[string]string
}

func (f *fakeHTTPOverlay) IsEmpty() bool                 { return f.empty }
func (f *fakeHTTPOverlay) PendingURLs() []string         { return f.pending }
func (f *fakeHTTPOverlay) HasPendingURL(url string) bool { _, ok := f.content[url]; return ok }
func (f *fakeHTTPOverlay) GetContent(url string) (string, bool) {
	c, ok := f.content[url]
	return c, ok
}

// fakeStore is a minimal Store used by tests that only need GetStore to
// return something non-nil; the actual methods don't get exercised.
type fakeStore struct{}

func (s *fakeStore) Get(_ ...string) ([]any, error) { return nil, nil }
func (s *fakeStore) List() ([]any, error)           { return nil, nil }
func (s *fakeStore) Add(_ any, _ []string) error    { return nil }
func (s *fakeStore) Update(_ any, _ []string) error { return nil }
func (s *fakeStore) Delete(_ ...string) error       { return nil }
func (s *fakeStore) Clear() error                   { return nil }

func TestNewValidationContext(t *testing.T) {
	t.Run("nil overlays yields empty map", func(t *testing.T) {
		ctx := NewValidationContext(nil)
		require.NotNil(t, ctx)
		require.NotNil(t, ctx.K8sOverlays)
		assert.Empty(t, ctx.K8sOverlays)
		assert.Nil(t, ctx.HTTPOverlay)
	})

	t.Run("provided overlays are stored", func(t *testing.T) {
		ovs := map[string]*StoreOverlay{"ingress": NewStoreOverlay()}
		ctx := NewValidationContext(ovs)
		// The context stores the same map values; mutating the original is
		// reflected in the context (maps are reference types in Go).
		ovs["new"] = NewStoreOverlay()
		assert.Contains(t, ctx.K8sOverlays, "new")
	})
}

func TestValidationContext_WithHTTPOverlay(t *testing.T) {
	ctx := NewValidationContext(nil)
	httpOv := &fakeHTTPOverlay{empty: false}
	ret := ctx.WithHTTPOverlay(httpOv)

	assert.Same(t, ctx, ret, "WithHTTPOverlay returns same context for chaining")
	assert.Equal(t, httpOv, ctx.HTTPOverlay)
}

func TestValidationContext_IsEmpty(t *testing.T) {
	emptyOverlay := NewStoreOverlay()
	createOverlay := NewStoreOverlayForDelete("default", "x")

	tests := []struct {
		name string
		ctx  *ValidationContext
		want bool
	}{
		{name: "nil context", ctx: nil, want: true},
		{name: "fresh context", ctx: NewValidationContext(nil), want: true},
		{name: "context with empty K8s overlay only", ctx: NewValidationContext(map[string]*StoreOverlay{"x": emptyOverlay}), want: true},
		{name: "context with non-empty K8s overlay", ctx: NewValidationContext(map[string]*StoreOverlay{"x": createOverlay}), want: false},
		{name: "context with nil http overlay", ctx: NewValidationContext(nil).WithHTTPOverlay(nil), want: true},
		{name: "context with empty http overlay", ctx: NewValidationContext(nil).WithHTTPOverlay(&fakeHTTPOverlay{empty: true}), want: true},
		{name: "context with non-empty http overlay", ctx: NewValidationContext(nil).WithHTTPOverlay(&fakeHTTPOverlay{empty: false}), want: false},
		{name: "context with nil entry in K8sOverlays", ctx: NewValidationContext(map[string]*StoreOverlay{"x": nil}), want: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, tt.ctx.IsEmpty())
		})
	}
}

func TestValidationContext_HasK8sOverlays(t *testing.T) {
	createOverlay := NewStoreOverlayForDelete("default", "x")

	tests := []struct {
		name string
		ctx  *ValidationContext
		want bool
	}{
		{name: "nil context", ctx: nil, want: false},
		{name: "no overlays", ctx: NewValidationContext(nil), want: false},
		{name: "empty overlays", ctx: NewValidationContext(map[string]*StoreOverlay{"x": NewStoreOverlay()}), want: false},
		{name: "non-empty overlay", ctx: NewValidationContext(map[string]*StoreOverlay{"x": createOverlay}), want: true},
		{name: "nil overlay entry", ctx: NewValidationContext(map[string]*StoreOverlay{"x": nil}), want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, tt.ctx.HasK8sOverlays())
		})
	}
}

func TestValidationContext_HasHTTPOverlay(t *testing.T) {
	tests := []struct {
		name string
		ctx  *ValidationContext
		want bool
	}{
		{name: "nil context", ctx: nil, want: false},
		{name: "no http overlay", ctx: NewValidationContext(nil), want: false},
		{name: "empty http overlay", ctx: NewValidationContext(nil).WithHTTPOverlay(&fakeHTTPOverlay{empty: true}), want: false},
		{name: "non-empty http overlay", ctx: NewValidationContext(nil).WithHTTPOverlay(&fakeHTTPOverlay{empty: false}), want: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, tt.ctx.HasHTTPOverlay())
		})
	}
}

func TestOverlayStoreProvider_GetStore(t *testing.T) {
	base := NewRealStoreProvider(map[string]Store{
		"ingress":  &fakeStore{},
		"services": &fakeStore{},
	})

	t.Run("nil context returns base store", func(t *testing.T) {
		p := NewOverlayStoreProvider(base, nil)
		got := p.GetStore("ingress")
		assert.NotNil(t, got)
	})

	t.Run("missing base store returns nil", func(t *testing.T) {
		p := NewOverlayStoreProvider(base, NewValidationContext(nil))
		assert.Nil(t, p.GetStore("missing"))
	})

	t.Run("base store returned when no overlay matches", func(t *testing.T) {
		ctx := NewValidationContext(map[string]*StoreOverlay{"other": NewStoreOverlayForDelete("ns", "name")})
		p := NewOverlayStoreProvider(base, ctx)
		got := p.GetStore("ingress")
		assert.NotNil(t, got)
		// Without overlay it must be the same concrete store, not a CompositeStore
		_, isComposite := got.(*CompositeStore)
		assert.False(t, isComposite, "expected base store, got CompositeStore")
	})

	t.Run("overlay returns CompositeStore", func(t *testing.T) {
		overlay := NewStoreOverlayForDelete("default", "ingress-1")
		ctx := NewValidationContext(map[string]*StoreOverlay{"ingress": overlay})
		p := NewOverlayStoreProvider(base, ctx)
		got := p.GetStore("ingress")
		require.NotNil(t, got)
		_, isComposite := got.(*CompositeStore)
		assert.True(t, isComposite, "expected CompositeStore")
	})

	t.Run("nil overlay value returns base store", func(t *testing.T) {
		ctx := NewValidationContext(map[string]*StoreOverlay{"ingress": nil})
		p := NewOverlayStoreProvider(base, ctx)
		got := p.GetStore("ingress")
		assert.NotNil(t, got)
		_, isComposite := got.(*CompositeStore)
		assert.False(t, isComposite, "nil overlay must not produce CompositeStore")
	})
}

func TestOverlayStoreProvider_StoreNames(t *testing.T) {
	base := NewRealStoreProvider(map[string]Store{
		"ingress":  &fakeStore{},
		"services": &fakeStore{},
	})

	t.Run("nil context returns base names only", func(t *testing.T) {
		p := NewOverlayStoreProvider(base, nil)
		got := p.StoreNames()
		assert.ElementsMatch(t, []string{"ingress", "services"}, got)
	})

	t.Run("merges overlay-only stores", func(t *testing.T) {
		overlays := map[string]*StoreOverlay{
			"ingress": NewStoreOverlayForDelete("ns", "x"),
			"newkind": NewStoreOverlayForDelete("ns", "y"),
		}
		p := NewOverlayStoreProvider(base, NewValidationContext(overlays))
		got := p.StoreNames()
		assert.ElementsMatch(t, []string{"ingress", "services", "newkind"}, got)
	})
}

func TestOverlayStoreProvider_GetHTTPOverlay(t *testing.T) {
	base := NewRealStoreProvider(nil)
	httpOv := &fakeHTTPOverlay{empty: false}

	t.Run("nil context", func(t *testing.T) {
		p := NewOverlayStoreProvider(base, nil)
		assert.Nil(t, p.GetHTTPOverlay())
	})

	t.Run("context without http overlay", func(t *testing.T) {
		p := NewOverlayStoreProvider(base, NewValidationContext(nil))
		assert.Nil(t, p.GetHTTPOverlay())
	})

	t.Run("context with http overlay", func(t *testing.T) {
		ctx := NewValidationContext(nil).WithHTTPOverlay(httpOv)
		p := NewOverlayStoreProvider(base, ctx)
		assert.Equal(t, httpOv, p.GetHTTPOverlay())
	})
}

func TestOverlayStoreProvider_IsValidationMode(t *testing.T) {
	base := NewRealStoreProvider(map[string]Store{"x": &fakeStore{}})
	createOverlay := NewStoreOverlayForDelete("ns", "x")

	tests := []struct {
		name string
		ctx  *ValidationContext
		want bool
	}{
		{name: "nil context not validation mode", ctx: nil, want: false},
		{name: "empty context not validation mode", ctx: NewValidationContext(nil), want: false},
		{name: "K8s overlay triggers validation mode", ctx: NewValidationContext(map[string]*StoreOverlay{"x": createOverlay}), want: true},
		{name: "http overlay triggers validation mode", ctx: NewValidationContext(nil).WithHTTPOverlay(&fakeHTTPOverlay{empty: false}), want: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := NewOverlayStoreProvider(base, tt.ctx)
			assert.Equal(t, tt.want, p.IsValidationMode())
		})
	}
}

func TestOverlayStoreProvider_Validate(t *testing.T) {
	base := NewRealStoreProvider(map[string]Store{"ingress": &fakeStore{}})

	tests := []struct {
		name      string
		ctx       *ValidationContext
		wantError bool
	}{
		{name: "nil context valid", ctx: nil},
		{name: "empty K8sOverlays valid", ctx: NewValidationContext(nil)},
		{name: "matching overlay valid", ctx: NewValidationContext(map[string]*StoreOverlay{"ingress": NewStoreOverlay()})},
		{name: "missing store fails", ctx: NewValidationContext(map[string]*StoreOverlay{"missing": NewStoreOverlay()}), wantError: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := NewOverlayStoreProvider(base, tt.ctx)
			err := p.Validate()
			if tt.wantError {
				require.Error(t, err)
				assert.Contains(t, err.Error(), "non-existent store")
				return
			}
			assert.NoError(t, err)
		})
	}
}

// inner is the minimal embedded store interface used by TypesStoreAdapter.
type recordingInner struct {
	getCalls    [][]string
	listCalled  bool
	addCalls    []addCall
	updateCalls []addCall
	deleteCalls [][]string
	clearCalled bool
	modCount    uint64
	supports    bool
	returnErr   error
}

type addCall struct {
	resource any
	keys     []string
}

func (r *recordingInner) Get(keys ...string) ([]any, error) {
	r.getCalls = append(r.getCalls, keys)
	return nil, r.returnErr
}
func (r *recordingInner) List() ([]any, error) {
	r.listCalled = true
	return nil, r.returnErr
}
func (r *recordingInner) Add(resource any, keys []string) error {
	r.addCalls = append(r.addCalls, addCall{resource, keys})
	return r.returnErr
}
func (r *recordingInner) Update(resource any, keys []string) error {
	r.updateCalls = append(r.updateCalls, addCall{resource, keys})
	return r.returnErr
}
func (r *recordingInner) Delete(keys ...string) error {
	r.deleteCalls = append(r.deleteCalls, keys)
	return r.returnErr
}
func (r *recordingInner) Clear() error {
	r.clearCalled = true
	return r.returnErr
}
func (r *recordingInner) ModCount() (uint64, bool) {
	return r.modCount, r.supports
}

func TestTypesStoreAdapter_Delegation(t *testing.T) {
	inner := &recordingInner{}
	adapter := &TypesStoreAdapter{Inner: inner}

	_, _ = adapter.Get("k1", "k2")
	_, _ = adapter.List()
	_ = adapter.Add("res", []string{"a"})
	_ = adapter.Update("res", []string{"b"})
	_ = adapter.Delete("c")
	_ = adapter.Clear()

	assert.Equal(t, [][]string{{"k1", "k2"}}, inner.getCalls)
	assert.True(t, inner.listCalled)
	assert.Equal(t, []addCall{{"res", []string{"a"}}}, inner.addCalls)
	assert.Equal(t, []addCall{{"res", []string{"b"}}}, inner.updateCalls)
	assert.Equal(t, [][]string{{"c"}}, inner.deleteCalls)
	assert.True(t, inner.clearCalled)
}

func TestTypesStoreAdapter_PropagatesErrors(t *testing.T) {
	wantErr := errors.New("boom")
	inner := &recordingInner{returnErr: wantErr}
	adapter := &TypesStoreAdapter{Inner: inner}

	_, err := adapter.Get("x")
	assert.ErrorIs(t, err, wantErr)
	_, err = adapter.List()
	assert.ErrorIs(t, err, wantErr)
	assert.ErrorIs(t, adapter.Add("r", nil), wantErr)
	assert.ErrorIs(t, adapter.Update("r", nil), wantErr)
	assert.ErrorIs(t, adapter.Delete("k"), wantErr)
	assert.ErrorIs(t, adapter.Clear(), wantErr)
}

func TestTypesStoreAdapter_ModCount(t *testing.T) {
	t.Run("inner supports tracking", func(t *testing.T) {
		inner := &recordingInner{modCount: 42, supports: true}
		adapter := &TypesStoreAdapter{Inner: inner}
		count, ok := adapter.ModCount()
		assert.Equal(t, uint64(42), count)
		assert.True(t, ok)
	})

	t.Run("inner does not support tracking", func(t *testing.T) {
		// Use a minimal struct that satisfies the embedded interface but
		// has no ModCount method.
		nonTracking := &fakeStore{}
		adapter := &TypesStoreAdapter{Inner: nonTracking}
		count, ok := adapter.ModCount()
		assert.Equal(t, uint64(0), count)
		assert.False(t, ok)
	})
}
