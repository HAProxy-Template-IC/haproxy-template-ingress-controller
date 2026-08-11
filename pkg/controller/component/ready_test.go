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

package component

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestReadySignal_StartsUnsignalled(t *testing.T) {
	r := NewReadySignal()
	select {
	case <-r.SubscriptionReady():
		t.Fatal("channel closed before MarkReady was called")
	default:
	}
}

func TestReadySignal_MarkReadyClosesChannel(t *testing.T) {
	r := NewReadySignal()
	r.MarkReady()

	select {
	case <-r.SubscriptionReady():
		// good
	case <-time.After(time.Second):
		t.Fatal("channel not closed after MarkReady")
	}
}

func TestReadySignal_MarkReadyIdempotent(t *testing.T) {
	r := NewReadySignal()

	// Calling MarkReady twice must not panic on the second close.
	r.MarkReady()
	assert.NotPanics(t, r.MarkReady)
	assert.NotPanics(t, r.MarkReady)
}

func TestReadySignal_RearmCreatesNextLifecycleSignal(t *testing.T) {
	r := NewReadySignal()
	r.MarkReady()
	first := r.SubscriptionReady()
	r.Rearm()
	second := r.SubscriptionReady()

	assert.NotEqual(t, first, second)
	select {
	case <-second:
		t.Fatal("rearmed channel closed before the next MarkReady")
	default:
	}
	r.MarkReady()
	select {
	case <-second:
	case <-time.After(time.Second):
		t.Fatal("rearmed channel not closed after MarkReady")
	}
}
