// Copyright 2026 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package renderer

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAdmissionRenderSlotsAreHalfTheCPUs(t *testing.T) {
	for cpus, want := range map[int]int{1: 1, 2: 1, 3: 1, 4: 2, 8: 4, 16: 8} {
		assert.Equal(t, want, admissionRenderSlots(cpus), "cpus=%d", cpus)
	}
}

// TestAdmissionSlotRefusesAnExpiredWait pins that a request expiring while
// it waits for a slot is refused with the context's error, and that a
// released slot admits the next request.
func TestAdmissionSlotRefusesAnExpiredWait(t *testing.T) {
	service := &RenderService{admissionSlots: make(chan struct{}, 1)}
	release, err := service.acquireAdmissionSlot(t.Context())
	require.NoError(t, err)

	expired, cancel := context.WithCancel(t.Context())
	cancel()
	_, err = service.acquireAdmissionSlot(expired)
	require.ErrorIs(t, err, context.Canceled)

	release()
	release, err = service.acquireAdmissionSlot(t.Context())
	require.NoError(t, err)
	release()

	unlimited := &RenderService{}
	release, err = unlimited.acquireAdmissionSlot(expired)
	require.NoError(t, err, "a service without slots never waits")
	release()
}
