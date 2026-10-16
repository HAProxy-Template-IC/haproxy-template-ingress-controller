// Copyright 2026 Philipp Hossner
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

package lifecycle

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

type drainingMock struct {
	*mockComponent
	order *[]string
	err   error
}

func (d *drainingMock) Drain(context.Context) error {
	*d.order = append(*d.order, d.name)
	return d.err
}

func TestDrainLeaderOnlyRunsInRegistrationOrderAndStopsAtTheFirstFailure(t *testing.T) {
	var order []string
	registry := NewRegistry()
	registry.Register(&drainingMock{mockComponent: newMockComponent("all-replica"), order: &order}, false)
	registry.Register(&drainingMock{mockComponent: newMockComponent("first"), order: &order}, true)
	registry.Register(newMockComponent("not-a-drainer"), true)
	registry.Register(&drainingMock{mockComponent: newMockComponent("failing"), order: &order, err: errors.New("refused")}, true)
	registry.Register(&drainingMock{mockComponent: newMockComponent("after-failure"), order: &order}, true)

	require.NoError(t, registry.DrainLeaderOnly(t.Context()), "components that are not running are not drained")
	require.Empty(t, order)

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	run, err := registry.StartLeaderOnly(ctx)
	require.NoError(t, err)

	require.ErrorContains(t, registry.DrainLeaderOnly(t.Context()), "draining failing: refused")
	require.Equal(t, []string{"first", "failing"}, order)
	cancel()
	require.NoError(t, run.Wait())
}
