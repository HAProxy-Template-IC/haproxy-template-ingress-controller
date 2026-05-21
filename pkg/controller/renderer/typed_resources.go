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

package renderer

import (
	"log/slog"
	"reflect"

	"gitlab.com/haproxy-haptic/haptic/pkg/k8s/typegen"
	"gitlab.com/haproxy-haptic/haptic/pkg/stores"
)

// addTypedRenderContextEntries populates renderContext with one
// top-level *[]*<generated-struct> entry per resource whose
// typebootstrap produced a Go type AND whose store is registered
// with the provider for this render. The entries match the
// declarations the engine constructor consumed via
// pkg/controller/typebootstrap.BuildEngineDeclarations.
//
// Pulled out of the RenderService method to keep
// buildRenderingContext under the per-function statement budget,
// and to localise the small bit of reflect handling so future
// reviewers can trace one shape — slice of pointer to generated
// struct — in one place.
//
// # Coherence with the untyped resources map
//
// The untyped resources["<name>"] map (built earlier in
// buildRenderingContext) loads its snapshot lazily on first
// .List() / .Fetch() call against each StoreWrapper. This typed
// path loads its snapshot eagerly by calling store.List() right
// here. The two snapshots can diverge by microseconds if the
// underlying store is updated between them. That doesn't matter
// for HAPTIC because templates use ONE shape or the OTHER for a
// given resource within a single render (Scriggo type-checks the
// access either way — they aren't mixable), and the watcher's
// debouncing makes simultaneous updates rare in practice.
//
// # Error policy
//
// Per-resource WrapSlice errors log at warn and skip the entry.
// The chart still renders for that resource via the untyped path.
// A genuine watcher regression (the runtime data shape diverges
// from the schema-declared shape) is the only way WrapSlice
// fails on our actual stores — it would suggest a CRD upgrade
// that broke the schema-data contract, which the operator should
// investigate. The chart-side template references still compile
// (against the declared shape) — they just see an empty iteration.
func addTypedRenderContextEntries(
	renderContext map[string]any,
	provider stores.StoreProvider,
	types map[string]reflect.Type,
	logger *slog.Logger,
) {
	if len(types) == 0 {
		return
	}
	for name, t := range types {
		store := provider.GetStore(name)
		if store == nil {
			// The watcher hasn't registered a store under this
			// name for this render. Common in tests; would only
			// happen in production if the watcher build raced
			// the bootstrap (impossible given iteration
			// ordering, but cheap to guard).
			continue
		}
		items, err := store.List()
		if err != nil {
			logger.Warn("typed resource: store List failed; omitting typed view",
				"resource", name, "error", err)
			continue
		}
		typedSlice, err := typegen.WrapSlice(items, t)
		if err != nil {
			logger.Warn("typed resource: WrapSlice failed; omitting typed view",
				"resource", name, "error", err)
			continue
		}
		// Wrap the slice in a pointer for the *[]*T shape — the
		// declared global is *[]*T and Scriggo's runtime needs the
		// value to match.
		holder := reflect.New(typedSlice.Type())
		holder.Elem().Set(typedSlice)
		renderContext[name] = holder.Interface()
	}
}
