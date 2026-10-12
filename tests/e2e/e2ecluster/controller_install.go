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
	"fmt"
	"slices"
)

// ControllerInstallStages preserves the requested release after an optional single-replica bootstrap.
func ControllerInstallStages(args []string, mode string) ([][]string, error) {
	switch mode {
	case "", "parallel":
		return [][]string{slices.Clone(args)}, nil
	case "staged":
		first := append(slices.Clone(args), "--set", "controller.replicaCount=1")
		return [][]string{first, slices.Clone(args)}, nil
	default:
		return nil, fmt.Errorf("controller startup must be parallel or staged, got %q", mode)
	}
}
