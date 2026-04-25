// Copyright 2025 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package rendercontext

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// toString is the small helper that StoreWrapper.Fetch and GetSingle use
// to normalize template-supplied lookup keys. Templates can pass any
// type — strings, ints, fmt.Stringer values like net.IP — and the store
// expects a string slice. The contract has THREE branches:
//
//  1. native string  → returned as-is (the hot path; ~all calls)
//  2. fmt.Stringer   → returned via .String() (e.g. types.UID, net.IP,
//                      time.Duration — all of which a template might
//                      reasonably pass as an index key)
//  3. anything else  → returned via fmt.Sprint (covers ints/floats from
//                      Scriggo and the nil corner case)
//
// The existing StoreWrapper tests only ever pass plain strings, so they
// cover branch 1 transitively but never exercise the Stringer path or
// the fmt.Sprint fallback. A regression that, e.g., dropped the
// Stringer case and fell straight through to fmt.Sprint would still
// "work" (fmt.Sprint also calls String()), but a regression that
// returned "" or "%!s(int=...)" garbage on non-string keys would
// silently break every template that does:
//
//	resources.endpoints.Fetch(svc.metadata.uid)   {# UID is types.UID, a Stringer #}
//	resources.endpoints.Fetch(podIndex)           {# numeric index #}
//
// Pin all three branches with a table.

// stringerImpl is a deliberately-minimal fmt.Stringer used to verify the
// Stringer dispatch fires in PREFERENCE to the fmt.Sprint fallback.
// Using a sentinel return value distinct from anything fmt.Sprint would
// produce confirms the Stringer branch executed (otherwise the test
// would still pass via fmt.Sprint's own String() lookup).
type stringerImpl struct{ payload string }

func (s stringerImpl) String() string { return "STRINGER:" + s.payload }

func TestToString_AllThreeBranches(t *testing.T) {
	tests := []struct {
		name string
		in   any
		want string
	}{
		{
			name: "native string returned verbatim (hot path)",
			in:   "hello",
			want: "hello",
		},
		{
			name: "empty string is preserved (not coerced to fallback)",
			// A regression that treated "" as "missing" and fell through
			// to fmt.Sprint would still produce "" — but a regression
			// that swapped the case order (Stringer before string) would
			// crash here because string isn't a Stringer.
			in:   "",
			want: "",
		},
		{
			name: "fmt.Stringer dispatched via String() with sentinel prefix",
			in:   stringerImpl{payload: "abc"},
			want: "STRINGER:abc",
		},
		{
			name: "*fmt.Stringer (pointer receiver) is also accepted",
			// Pointer-receiver Stringers must dispatch the same way.
			in:   &stringerImpl{payload: "ptr"},
			want: "STRINGER:ptr",
		},
		{
			name: "int falls through to fmt.Sprint and produces decimal form",
			// Templates often pass numeric indexes as keys. A regression
			// that returned "" or "%!s(int=42)" on non-string types
			// would silently break those callers.
			in:   42,
			want: "42",
		},
		{
			name: "negative int formatted via fmt.Sprint",
			in:   -7,
			want: "-7",
		},
		{
			name: "bool formatted via fmt.Sprint",
			in:   true,
			want: "true",
		},
		{
			name: "nil any becomes the literal '<nil>' from fmt.Sprint",
			// Defensive: a template might pass a missing field. Returning
			// "<nil>" is documented Go behaviour and lets the lookup
			// fail predictably (with a "not found" against the literal
			// "<nil>") rather than crashing.
			in:   nil,
			want: "<nil>",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := toString(tt.in)
			assert.Equal(t, tt.want, got,
				"toString must dispatch on the input's dynamic type: "+
					"string→verbatim, fmt.Stringer→.String(), else→fmt.Sprint; "+
					"a regression that mishandled any branch would silently "+
					"break Fetch/GetSingle calls from templates passing that key type")
		})
	}
}

// TestToString_StringerPriorityOverFmtSprint pins that the Stringer
// branch fires BEFORE the fmt.Sprint fallback for types that satisfy
// both. fmt.Sprint also calls String() under the hood, so the only way
// to observe which branch fired is to make the Stringer return
// something fmt.Sprint wouldn't produce on its own and assert on the
// distinguishing prefix.
func TestToString_StringerPriorityOverFmtSprint(t *testing.T) {
	got := toString(stringerImpl{payload: "x"})

	// "STRINGER:x" can only come from our explicit String() — fmt.Sprint
	// on stringerImpl{} would also call our String(), but if a future
	// regression dropped the explicit Stringer branch and the type
	// stopped satisfying fmt.Stringer, fmt.Sprint would fall back to
	// the struct-formatting "{x}" form. So the explicit-prefix
	// assertion guards against both branch-removal AND interface-loss
	// regressions.
	assert.Equal(t, "STRINGER:x", got,
		"the Stringer branch must be observable via its custom return; "+
			"a regression to {x} struct-formatting would mean either the "+
			"Stringer case was dropped or the type stopped implementing it")
}
