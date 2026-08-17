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

package deployplan

import (
	"strconv"
	"strings"
)

// Caps is what one pod can do at runtime: what its HAProxy version accepts and
// which op kinds its agent executes.
type Caps struct {
	DynamicServers  bool            // add server / del server
	DynamicBackends bool            // add backend / del backend, 3.4+
	ServerInitState bool            // add server ... init-state up, 3.1+
	AgentOps        map[string]bool // nil: the agent executes every kind
}

// CapsFor derives the capabilities of a pod running haproxyVersion whose agent
// reported agentOps. An unreadable version leaves every capability false, so a
// pod HAPTIC cannot identify reloads instead of receiving ops it may reject.
func CapsFor(haproxyVersion string, agentOps []string) Caps {
	caps := Caps{}
	if len(agentOps) > 0 {
		caps.AgentOps = make(map[string]bool, len(agentOps))
		for _, kind := range agentOps {
			caps.AgentOps[kind] = true
		}
	}
	major, minor, ok := parseMajorMinor(haproxyVersion)
	if !ok {
		return caps
	}
	caps.DynamicServers = notBelow(major, minor, 3, 0)
	caps.ServerInitState = notBelow(major, minor, 3, 1)
	caps.DynamicBackends = notBelow(major, minor, 3, 4)
	return caps
}

// executes reports whether the pod's agent runs this op kind.
func (c *Caps) executes(kind string) bool {
	return c.AgentOps == nil || c.AgentOps[kind]
}

func notBelow(major, minor, wantMajor, wantMinor int) bool {
	return major > wantMajor || (major == wantMajor && minor >= wantMinor)
}

// parseMajorMinor reads the leading "<major>.<minor>" of a version string,
// ignoring a "v" prefix, the patch level and any suffix.
func parseMajorMinor(version string) (major, minor int, ok bool) {
	fields := strings.SplitN(strings.TrimPrefix(strings.TrimSpace(version), "v"), ".", 3)
	major, err := strconv.Atoi(leadingDigits(fields[0]))
	if err != nil {
		return 0, 0, false
	}
	if len(fields) > 1 {
		minor, _ = strconv.Atoi(leadingDigits(fields[1]))
	}
	return major, minor, true
}

func leadingDigits(s string) string {
	for i := range len(s) {
		if s[i] < '0' || s[i] > '9' {
			return s[:i]
		}
	}
	return s
}
