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

package configchange

import (
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/events"
	coreconfig "gitlab.com/haproxy-haptic/haptic/pkg/core/config"
)

// ResolvedConfig is the effective configuration and the discovery outcome
// that produced it. Validators and the next controller iteration consume this
// pair together.
type ResolvedConfig struct {
	Config     *coreconfig.Config
	Resolution *coreconfig.Resolution
}

// ValidatedSnapshot is the complete input accepted for one controller
// iteration. Config is effective; RawConfig retains candidate API versions for
// later discovery re-resolution.
type ValidatedSnapshot struct {
	RawConfig          *coreconfig.Config
	Config             *coreconfig.Config
	Resolution         *coreconfig.Resolution
	TemplateConfig     any
	ConfigVersion      string
	Credentials        *coreconfig.Credentials
	CredentialsVersion string
	Sources            []events.ConfigSourceRef
}

// ReloadReason identifies independent desired-state changes carried by a reload.
type ReloadReason uint8

const (
	ReloadReasonConfig ReloadReason = 1 << iota
	ReloadReasonCredentials
	ReloadReasonEffectiveConfig
)

// Has reports whether the reason set contains reason.
func (r ReloadReason) Has(reason ReloadReason) bool {
	return r&reason != 0
}

// ReloadRequest carries the authoritative state for the next iteration. An
// effective-config reason requires re-resolving Snapshot.RawConfig; otherwise
// Snapshot.Config is already validated and must be used verbatim.
type ReloadRequest struct {
	Snapshot *ValidatedSnapshot
	Reasons  ReloadReason
}

func cloneSnapshot(snapshot *ValidatedSnapshot) *ValidatedSnapshot {
	if snapshot == nil {
		return nil
	}
	clone := *snapshot
	clone.Sources = append([]events.ConfigSourceRef(nil), snapshot.Sources...)
	return &clone
}
