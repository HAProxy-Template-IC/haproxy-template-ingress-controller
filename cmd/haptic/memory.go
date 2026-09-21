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

package main

import (
	"log/slog"

	"github.com/KimMachineGun/automemlimit/memlimit"
)

func configureMemoryLimit(logger *slog.Logger) {
	if _, err := memlimit.Set(memlimit.WithLogger(logger)); err != nil {
		logger.Warn("Failed to set GOMEMLIMIT from cgroup", "error", err)
	}
}
