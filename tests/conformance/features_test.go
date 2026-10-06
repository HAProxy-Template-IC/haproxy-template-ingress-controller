//go:build gateway_conformance

// Copyright 2026 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package conformance

import (
	"testing"

	"github.com/stretchr/testify/require"
	"sigs.k8s.io/gateway-api/pkg/features"
)

func TestRetryCodesFeature(t *testing.T) {
	for _, name := range []features.FeatureName{"HTTPRouteRetry", "HTTPRouteRetryCodes"} {
		t.Run(string(name), func(t *testing.T) {
			available := []features.Feature{{Name: "HTTPRouteRetryConnectionError"}, {Name: name}}
			selected, err := retryCodesFeature(available)
			require.NoError(t, err)
			require.Equal(t, name, selected)
		})
	}
	t.Run("unknown name fails", func(t *testing.T) {
		_, err := retryCodesFeature([]features.Feature{{Name: "HTTPRouteRetryConnectionError"}})
		require.ErrorContains(t, err, "no recognized retry-codes feature")
	})
	t.Run("installed upstream registry", func(t *testing.T) {
		_, err := retryCodesFeature(features.AllFeatures.UnsortedList())
		require.NoError(t, err)
	})
}
