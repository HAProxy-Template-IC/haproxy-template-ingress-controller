// Copyright 2025 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package watcher

import (
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
)

func TestWatcher_ConvertToUnstructured_TypedRuntimeObjectReturnsNil(t *testing.T) {
	k8sClient := newTestClient(t)
	cfg := validWatcherConfig()

	_, err := New(cfg, k8sClient, slog.Default())
	require.NoError(t, err)

	pod := &corev1.Pod{}
	got := watchResource(pod)
	assert.Nil(t, got)
}
