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

package configpublisher

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/runtime"
	k8stesting "k8s.io/client-go/testing"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/auxiliaryfiles"
)

// retry.OnError reports a context cancellation as the last retriable error,
// which is nil when none preceded it, so a write interrupted by shutdown used
// to come back as (nil result, nil error) and PublishConfig dereferenced the
// missing HAProxyCfg (observed as a SIGSEGV on lost leadership mid-publish).
func TestPublishConfig_InterruptedRuntimeConfigWriteIsAnError(t *testing.T) {
	_, _, crdClient, publisher := newTestPublisher(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	crdClient.PrependReactor("get", "haproxycfgs", func(_ k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, context.Canceled
	})

	req := basePublishRequest()
	result, err := publisher.PublishConfig(ctx, &req)

	require.Error(t, err)
	assert.ErrorIs(t, err, context.Canceled)
	assert.Nil(t, result)
}

func TestPublishConfig_InterruptedAuxiliaryWriteIsAnError(t *testing.T) {
	_, _, crdClient, publisher := newTestPublisher(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	crdClient.PrependReactor("get", "haproxymapfiles", func(_ k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, context.Canceled
	})

	req := basePublishRequest()
	req.AuxiliaryFiles = &AuxiliaryFiles{
		MapFiles: []auxiliaryfiles.MapFile{{Path: "/etc/haproxy/maps/host.map", Content: "example.com backend1\n"}},
	}
	_, err := publisher.PublishConfig(ctx, &req)

	require.Error(t, err)
	assert.ErrorIs(t, err, context.Canceled)
}
