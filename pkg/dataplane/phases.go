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

package dataplane

import "fmt"

// SyncPhase identifies one of the three phases of a configuration sync.
//
// HAProxy configs can reference auxiliary files (maps, SSL certs, error
// pages, etc.). A single sync therefore runs in three phases,
// sequenced so that referenced files exist before the config mentions them
// and orphaned files are only cleaned up after the config that stopped
// referencing them has been applied:
//
//  1. PhasePreConfig  — create or update auxiliary files. Includes verifying
//     any reloads those operations trigger, so the next phase's config
//     operations don't race against pending reloads.
//  2. PhaseConfig     — apply HAProxy configuration changes by pushing the full
//     rendered config (raw push), reloading only when structural changes are
//     present.
//  3. PhasePostConfig — delete auxiliary files that the new config no longer
//     references. Must run after PhaseConfig succeeds.
//
// The enum is advisory — used in log fields and error context — rather than
// a control-flow driver; the orchestrator still calls the phase-specific
// helpers directly.
type SyncPhase int

const (
	// PhasePreConfig syncs auxiliary files BEFORE the HAProxy config that
	// references them.
	PhasePreConfig SyncPhase = iota + 1

	// PhaseConfig applies the HAProxy configuration change.
	PhaseConfig

	// PhasePostConfig deletes auxiliary files that are no longer referenced.
	PhasePostConfig
)

// String returns a human-readable phase name for logging.
func (p SyncPhase) String() string {
	switch p {
	case PhasePreConfig:
		return "pre-config"
	case PhaseConfig:
		return "config"
	case PhasePostConfig:
		return "post-config"
	default:
		return fmt.Sprintf("unknown-phase(%d)", int(p))
	}
}
