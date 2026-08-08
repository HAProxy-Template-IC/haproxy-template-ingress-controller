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

package watcher

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"gitlab.com/haproxy-haptic/haptic/pkg/k8s/types"
)

// TestSingleWatcher_OnSyncCompleteEmptyCacheIsUntypedNil pins the argument the
// callback receives for an absent object: a nil pointer in an `any` makes a
// non-nil interface, so callers' `obj == nil` guards miss it (#140).
func TestSingleWatcher_OnSyncCompleteEmptyCacheIsUntypedNil(t *testing.T) {
	k8sClient := createFakeClientForConfigMapListing()

	type observation struct {
		called    bool
		isNil     bool
		guardHeld bool
	}
	seen := make(chan observation, 1)

	cfg := types.SingleWatcherConfig{
		GVR:       schema.GroupVersionResource{Version: "v1", Resource: "configmaps"},
		Namespace: "default",
		Name:      "absent-config",
		OnChange:  func(any) error { return nil },
		OnSyncComplete: func(obj any) error {
			// The guard every production callback writes.
			guardHeld := obj == nil
			seen <- observation{called: true, isNil: obj == nil, guardHeld: guardHeld}
			return nil
		},
	}

	w, err := NewSingle(&cfg, k8sClient)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = w.Start(ctx) }()

	select {
	case got := <-seen:
		assert.True(t, got.called, "OnSyncComplete should fire even with an empty cache")
		assert.True(t, got.isNil,
			"an absent object must arrive as an untyped nil, not (*unstructured.Unstructured)(nil)")
		assert.True(t, got.guardHeld,
			"a callback's `obj == nil` guard must hold, or it forwards a resource that panics on first use")
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for OnSyncComplete")
	}
}
