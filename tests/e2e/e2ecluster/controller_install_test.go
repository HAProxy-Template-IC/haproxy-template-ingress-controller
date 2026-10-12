//go:build e2e

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

package e2ecluster

import (
	"slices"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestControllerInstallStagesPreserveRequestedRelease(t *testing.T) {
	for _, args := range [][]string{
		{"upgrade", "--install", "haptic", "chart", "--wait"},
		{"upgrade", "--install", "haptic", "chart", "--wait", "--set", "controller.replicaCount=3"},
	} {
		original := slices.Clone(args)
		stages, err := ControllerInstallStages(args, "staged")
		require.NoError(t, err)
		require.Len(t, stages, 2)
		require.Equal(t, []string{"--set", "controller.replicaCount=1"}, stages[0][len(args):])
		require.Equal(t, original, stages[0][:len(args)])
		require.Equal(t, original, stages[1])
		require.Equal(t, original, args)
		stages[0][0] = "changed"
		require.Equal(t, original, stages[1])
		require.Equal(t, original, args)
	}
}

func TestControllerInstallStagesModes(t *testing.T) {
	for _, mode := range []string{"", "parallel"} {
		stages, err := ControllerInstallStages([]string{"--wait"}, mode)
		require.NoError(t, err)
		require.Equal(t, [][]string{{"--wait"}}, stages)
	}
	_, err := ControllerInstallStages(nil, "unknown")
	require.Error(t, err)
}
