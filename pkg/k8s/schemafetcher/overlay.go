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

package schemafetcher

import (
	"context"

	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/kube-openapi/pkg/validation/spec"
)

// Overlay composes Fetchers in priority order: the first to return a
// schema for a GVK wins. Used to layer a user-supplied schema
// directory ([DirFetcher]) on top of the controller's embedded
// defaults ([builtin.NewFetcher]) so the common case (Gateway API
// chart out-of-the-box) just works, while operators with custom
// CRDs add files to a directory without re-shipping any of the
// embedded ones.
//
// kubeconform users will recognise the model: multiple
// `--schema-location` flags stack the same way, dir-by-dir.
//
// Lookup terminates on the first hit; misses are coalesced into a
// single [ErrSchemaNotAvailable] using the LAST fetcher's GVK so the
// error includes the most-specific not-found message rather than a
// composite. That keeps the operator-facing error short and
// debuggable (one not-found per GVK, not one per layer).
//
// Concurrency: safe for concurrent use iff every wrapped fetcher is.
// All current implementations ([DirFetcher], [MapFetcher],
// [ClusterFetcher]) satisfy that.
type Overlay struct {
	layers []Fetcher
}

// NewOverlay returns an Overlay that consults `layers` in order. nil
// layers are skipped (operators can pass a conditionally-constructed
// DirFetcher without an explicit nil check). At least one non-nil
// layer is required — panics on construction otherwise. The panic is
// the right behaviour because an empty Overlay would silently
// fail-not-found for every GVK, which is a configuration mistake
// callers should fix, not paper over.
func NewOverlay(layers ...Fetcher) *Overlay {
	pruned := make([]Fetcher, 0, len(layers))
	for _, l := range layers {
		if l != nil {
			pruned = append(pruned, l)
		}
	}
	if len(pruned) == 0 {
		panic("schemafetcher: NewOverlay requires at least one non-nil layer")
	}
	return &Overlay{layers: pruned}
}

// Fetch implements [Fetcher]. Iterates layers in order, returning
// the first non-not-found response. NotFound errors are silently
// fallen-through (the whole point of the overlay); any other error
// (network, parse, etc.) short-circuits — the operator probably
// wants to see "OpenAPI v3 fetch failed" rather than a "not found"
// covering it up.
func (o *Overlay) Fetch(ctx context.Context, gvk schema.GroupVersionKind) (*spec.Schema, map[string]spec.Schema, error) {
	var lastErr error
	for _, l := range o.layers {
		sch, components, err := l.Fetch(ctx, gvk)
		if err == nil {
			return sch, components, nil
		}
		if !IsNotFound(err) {
			// Real error — surface it without trying further layers.
			// Examples: parse failure in a DirFetcher entry,
			// OpenAPI v3 endpoint rejection in ClusterFetcher,
			// context cancellation.
			return nil, nil, err
		}
		lastErr = err
	}
	// All layers said not-found. Return the last layer's error so
	// the GVK propagates correctly into ErrSchemaNotAvailable.
	return nil, nil, lastErr
}
