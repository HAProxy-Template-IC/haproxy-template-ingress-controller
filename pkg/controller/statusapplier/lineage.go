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

package statusapplier

import (
	"crypto/sha256"
	"slices"
)

const maxStatusWriteHistory = 16

type statusWrite struct {
	resourceVersion string
	phase           string
	payload         [sha256.Size]byte
	ownership       [sha256.Size]byte
}

func (entry *statusCacheEntry) supersededWrites(uid, nextVersion, sourceVersion string, ownership statusOwnership) []statusWrite {
	if entry.uid != uid || entry.latestResourceVersion == "" {
		return nil
	}
	if entry.latestResourceVersion == nextVersion {
		return entry.superseded
	}
	history := entry.superseded
	if len(history) == maxStatusWriteHistory {
		copy(history, history[1:])
		history = history[:len(history)-1]
	}
	history = append(history, statusWrite{
		resourceVersion: entry.latestResourceVersion,
		phase:           entry.lastPhase,
		payload:         sha256.Sum256(entry.lastPayload),
		ownership:       entry.ownership[fieldManagerPrefix+"-"+entry.lastPhase],
	})
	knownSource := sourceVersion == entry.baseResourceVersion || sourceVersion == entry.latestResourceVersion ||
		slices.ContainsFunc(history, func(write statusWrite) bool { return write.resourceVersion == sourceVersion })
	for i := range history {
		if !knownSource || history[i].ownership != ownership[fieldManagerPrefix+"-"+history[i].phase] {
			history[i].ownership = [sha256.Size]byte{}
		}
	}
	return history
}

func (entry *statusCacheEntry) isSupersededEcho(sourceVersion, phase string, payload []byte) bool {
	if entry.phaseBaseVersions[phase] == sourceVersion {
		return false
	}
	digest := sha256.Sum256(payload)
	for _, write := range entry.superseded {
		if write.resourceVersion == sourceVersion && write.phase == phase && write.payload == digest {
			return entry.lastPhase == phase || (write.ownership != [sha256.Size]byte{} &&
				write.ownership == entry.ownership[fieldManagerPrefix+"-"+phase])
		}
	}
	return false
}

func (entry *statusCacheEntry) rememberPhaseBase(phase, sourceVersion string) {
	if entry.phaseBaseVersions == nil {
		entry.phaseBaseVersions = make(map[string]string)
	}
	entry.phaseBaseVersions[phase] = sourceVersion
}
