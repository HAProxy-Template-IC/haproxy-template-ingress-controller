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

import (
	"strings"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/comparator/sections"
)

// Section/directive keywords the bypass-body patch needs to recognize.
const (
	sectionKeywordBackend  = "backend"
	sectionKeywordListen   = "listen"
	directiveKeywordServer = "server"
)

// configSectionKeywords are the HAProxy section-opening keywords. A config
// line whose first token is one of these starts a new section; every other
// line is a directive inside the current section.
var configSectionKeywords = map[string]struct{}{
	"global": {}, "defaults": {}, "frontend": {}, sectionKeywordBackend: {},
	sectionKeywordListen: {}, "peers": {}, "resolvers": {}, "userlist": {},
	"mailers": {}, "cache": {}, "program": {}, "http-errors": {}, "ring": {},
	"log-forward": {}, "fcgi-app": {}, "crt-store": {}, "traces": {},
	"dynamic-update": {}, "aggregations": {},
}

// BuildRuntimeBypassBody builds the config body a runtime-bypass push
// (SyncRuntimeFast, skip_reload+skip_version) must carry: the last
// reload-ACTIVATED baseline with each runtime-updated server's line replaced
// by that server's line from the desired render.
//
// The bypass must never push the pending render itself (issue #84): the
// dataplane writes the pushed body to disk VERBATIM without a reload — even
// when the accompanying runtime actions fail — so a pending render's body
// either clobbers a concurrent force_reload deploy's write between the write
// and the master's re-exec read (activating a pre-route config, mode A), or
// parks un-activated structural content on disk that a later sync's empty
// diff then "successfully" skips the reload for (mode B). Patching ONLY the
// runtime-eligible server lines onto the baseline keeps disk structurally
// identical to the last activated config while still carrying the fresh pod
// addresses across an unexpected worker restart.
//
// Both inputs are plain text; the patch is two O(lines) scans with no config
// parse, so it is cheap even for 10k+ line configs and is computed once per
// apply (shared across pods). Non-server runtime ops (e.g. a frontend
// maxconn change) are not patched — their runtime action still reaches the
// live worker and the authoritative deploy converges the file. Best-effort by
// construction: a server line that cannot be located is left at the baseline
// value (the safe direction — disk stays at activated content).
//
// The diff u was computed baseline→desired, so for every runtime-eligible
// ServerUpdateOp the two renders' server lines differ ONLY in runtime-
// supported fields — copying the desired line verbatim IS the minimal patch.
func (u *RuntimeServerUpdates) BuildRuntimeBypassBody(baselineConfig, desiredConfig string) string {
	targets := u.runtimeServerTargets()
	if len(targets) == 0 {
		return baselineConfig
	}

	// Pass 1: collect the desired render's line for each targeted server,
	// keyed by section (backend/listen) name + server name.
	desiredLines := make(map[serverLineKey]string)
	forEachTargetServerLine(desiredConfig, targets, func(key serverLineKey, _ int, line string) {
		if _, seen := desiredLines[key]; !seen {
			desiredLines[key] = line
		}
	})
	if len(desiredLines) == 0 {
		return baselineConfig
	}

	// Pass 2: rewrite the baseline, swapping in the desired server lines.
	lines := strings.Split(baselineConfig, "\n")
	replaced := false
	forEachTargetServerLine(baselineConfig, targets, func(key serverLineKey, i int, _ string) {
		if desired, ok := desiredLines[key]; ok {
			lines[i] = desired
			replaced = true
		}
	})
	if !replaced {
		return baselineConfig
	}
	return strings.Join(lines, "\n")
}

// runtimeServerTargets returns section-name → set of server names for the
// runtime-eligible server updates in the diff. Nil-safe.
func (u *RuntimeServerUpdates) runtimeServerTargets() map[string]map[string]struct{} {
	if u == nil {
		return nil
	}
	var targets map[string]map[string]struct{}
	for _, op := range u.runtimeOps {
		serverOp, ok := op.(*sections.ServerUpdateOp)
		if !ok {
			continue
		}
		if targets == nil {
			targets = make(map[string]map[string]struct{})
		}
		backend := serverOp.BackendName()
		if targets[backend] == nil {
			targets[backend] = make(map[string]struct{})
		}
		targets[backend][serverOp.ServerName()] = struct{}{}
	}
	return targets
}

// serverLineKey identifies one server line: the enclosing backend/listen
// section name plus the server name.
type serverLineKey struct {
	section string
	server  string
}

// forEachTargetServerLine walks config line by line, tracking the enclosing
// section, and invokes visit for every `server <name> …` directive inside a
// targeted backend/listen section whose name is in that section's target set.
func forEachTargetServerLine(config string, targets map[string]map[string]struct{}, visit func(key serverLineKey, lineIdx int, line string)) {
	var sectionName string
	var sectionServers map[string]struct{} // nil while not inside a targeted section
	rest := config
	for i := 0; ; i++ {
		line, tail, more := strings.Cut(rest, "\n")
		rest = tail

		first, second := firstTwoFields(line)
		if _, isSection := configSectionKeywords[first]; isSection {
			sectionName = ""
			sectionServers = nil
			if (first == sectionKeywordBackend || first == sectionKeywordListen) && second != "" {
				sectionName = second
				sectionServers = targets[second]
			}
		} else if sectionServers != nil && first == directiveKeywordServer && second != "" {
			if _, ok := sectionServers[second]; ok {
				visit(serverLineKey{section: sectionName, server: second}, i, line)
			}
		}

		if !more {
			return
		}
	}
}

// firstTwoFields returns the first two whitespace-separated tokens of line
// without allocating a full field slice.
func firstTwoFields(line string) (first, second string) {
	line = strings.TrimLeft(line, " \t")
	i := strings.IndexAny(line, " \t")
	if i < 0 {
		return line, ""
	}
	first = line[:i]
	rest := strings.TrimLeft(line[i:], " \t")
	if j := strings.IndexAny(rest, " \t"); j >= 0 {
		rest = rest[:j]
	}
	return first, rest
}
