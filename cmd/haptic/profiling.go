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
	"fmt"
	"os"
	"runtime"
	"strconv"
)

// configureContentionProfiling fills /debug/pprof/mutex and /debug/pprof/block.
// Both stay empty unless enabled, because sampling lock waits costs every contended path.
func configureContentionProfiling() (mutexFraction, blockRate int, err error) {
	if mutexFraction, err = nonNegativeEnv("MUTEX_PROFILE_FRACTION"); err != nil {
		return 0, 0, err
	}
	if blockRate, err = nonNegativeEnv("BLOCK_PROFILE_RATE"); err != nil {
		return 0, 0, err
	}
	runtime.SetMutexProfileFraction(mutexFraction)
	runtime.SetBlockProfileRate(blockRate)
	return mutexFraction, blockRate, nil
}

func nonNegativeEnv(name string) (int, error) {
	raw := os.Getenv(name)
	if raw == "" {
		return 0, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < 0 {
		return 0, fmt.Errorf("%s must be a non-negative integer, got %q", name, raw)
	}
	return value, nil
}
