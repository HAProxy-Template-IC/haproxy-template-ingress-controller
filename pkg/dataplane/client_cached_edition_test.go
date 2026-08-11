// Copyright 2026 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package dataplane

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewClientCachedVersionPreservesEnterpriseEdition(t *testing.T) {
	c, err := NewClient(t.Context(), &Endpoint{
		URL:                  "http://does-not-need-to-exist",
		Username:             "admin",
		Password:             "password",
		DetectedMajorVersion: 3,
		DetectedMinorVersion: 2,
		DetectedFullVersion:  "v3.2.6-ee1",
	})
	require.NoError(t, err)
	assert.True(t, c.orch.client.Clientset().IsEnterprise())
}
