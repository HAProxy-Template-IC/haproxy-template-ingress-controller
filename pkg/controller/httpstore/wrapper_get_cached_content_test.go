// Copyright 2025 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package httpstore

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/testutil"
	purehttpstore "gitlab.com/haproxy-haptic/haptic/pkg/httpstore"
)

// getCachedContent encodes the validation-vs-production content
// routing contract for the HTTP-template wrapper. The two negative
// cases (cache miss in either mode) are already covered; the two
// POSITIVE cases — content found, returned from the correct source
// — are the ones that lock in the asymmetric two-version cache
// invariant. They are NOT covered by the existing tests.
//
// The contract is asymmetric:
//
//   - Validation mode (overlay != nil) → return what the overlay
//     supplies. The overlay's GetContent prefers PENDING content
//     when present (delegating to store.GetForValidation). This is
//     deliberate: the validator must see the new content to test
//     it before the system promotes it.
//
//   - Production mode (overlay == nil) → return ACCEPTED content
//     only via store.Get. Production renders MUST NOT see pending
//     (unvalidated) content; the two-version cache pattern only
//     works if the production path strictly bypasses the overlay.
//
// A regression that swapped these would surface as two distinct
// silent failures depending on direction:
//
//   - Validation returning accepted-only → never test the new
//     pending content; bugs pass validation and only blow up in
//     production after promotion.
//   - Production returning overlay (pending) → unvalidated content
//     reaches HAProxy, defeating the whole point of the
//     pending/accepted split.

func TestHTTPStoreWrapper_GetCachedContent_ValidationModeReturnsContent(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()
	component := New(bus, logger, 0)

	// LoadFixture writes directly to the store as ACCEPTED content
	// (no pending). We use it because it bypasses the HTTP fetch
	// machinery; the assertion that matters here is that the
	// validation-mode path returns SOMETHING via the overlay path,
	// regardless of whether the overlay rewrites it.
	const url = "http://example.com/content.txt"
	const expectedContent = "fixture-content-for-validation-mode"
	component.GetStore().LoadFixture(url, expectedContent)

	overlay := purehttpstore.NewHTTPOverlay(component.GetStore())
	wrapper := NewHTTPStoreWrapper(context.Background(), component, logger, overlay, SourceModeReadOnly)

	got, ok, err := wrapper.getCachedContent(url, "")

	require.NoError(t, err)
	require.True(t, ok,
		"validation mode MUST return content when the overlay finds it — "+
			"a regression that fell through to the production-mode store.Get "+
			"branch would still find this fixture but would skip the overlay's "+
			"pending-content priority, breaking validation of new HTTP content")
	assert.Equal(t, expectedContent, got,
		"the returned content MUST be exactly what the overlay supplies — "+
			"any rewriting at the wrapper layer would break the contract that "+
			"validation sees the same bytes the validator inspects")
}

func TestHTTPStoreWrapper_GetCachedContent_ProductionModeReturnsAcceptedContent(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()
	component := New(bus, logger, 0)

	const url = "http://example.com/content.txt"
	const expectedContent = "fixture-content-for-production-mode"
	component.GetStore().LoadFixture(url, expectedContent)

	// Production mode: nil overlay. The wrapper MUST consult
	// store.Get directly (which returns accepted content only).
	wrapper := NewHTTPStoreWrapper(context.Background(), component, logger, nil, SourceModeAuthoritative)

	got, ok, err := wrapper.getCachedContent(url, "")

	require.NoError(t, err)
	require.True(t, ok,
		"production mode MUST return accepted content when present in the "+
			"underlying store — a regression that wrongly required an overlay "+
			"would silently break every production render that uses HTTP")
	assert.Equal(t, expectedContent, got,
		"the returned content MUST be exactly what store.Get supplies — "+
			"any rewriting would break HAProxy config determinism and break "+
			"the reload-detection checksum chain")
}
