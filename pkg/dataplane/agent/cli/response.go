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

package cli

import (
	"errors"
	"regexp"
	"strings"
)

// Typed outcomes the caller acts on rather than just reports.
var (
	// ErrNameCollision means a backend of that name already exists. The shape
	// behind the name is unreadable at runtime, so the apply stops and reloads.
	ErrNameCollision = errors.New("backend name already in use")
	// ErrWaitExpired means the object still has attached connections.
	ErrWaitExpired = errors.New("wait delay expired")
	// ErrRejected is HAProxy refusing a command.
	ErrRejected = errors.New("haproxy rejected the command")
	// ErrUnreadableResponse means the batch answered something the matcher
	// cannot attribute. The baseline is unknown from here on.
	ErrUnreadableResponse = errors.New("unreadable runtime response")
)

// severityPrefix is what `set severity-output number` puts in front of every
// message. HAProxy uses syslog levels: 0..3 are refusals, 5..7 are chatter, and
// 4 (WARNING) is both — `set server addr` and its check/agent siblings answer
// at that level whether they applied the change or refused it.
var severityPrefix = regexp.MustCompile(`^\[([0-7])\]:`)

// errorPhrases are HAProxy's own refusals, matched lowercased. They cover the
// commands whose failures carry no severity prefix, and the WARNING-level
// answers whose severity does not decide.
var errorPhrases = []string{
	"unknown command",
	"no such",
	"not found",
	"doesn't exist",
	"does not exist",
	"already used by other proxy",
	"already exists",
	"permission denied",
	"unable to",
	"cannot",
	"can't",
	"invalid",
	"unsupported",
	"failed",
	"error",
	"problem converting",
	"through configuration file",
}

// CommandResult is one command's verdict, with HAProxy's own words.
type CommandResult struct {
	Output string
	Err    error
}

// splitSegments cuts a batched response into one segment per command that
// answered. HAProxy separates consecutive responses with a blank line, so the
// separator is never a line count.
func splitSegments(raw string) []string {
	var segments []string
	for _, seg := range strings.Split(strings.ReplaceAll(raw, "\r\n", "\n"), "\n\n") {
		if trimmed := strings.TrimRight(strings.TrimLeft(seg, "\n"), " \n"); trimmed != "" {
			segments = append(segments, trimmed)
		}
	}
	return segments
}

// classify turns one response segment into a verdict. A severity prefix is
// HAProxy's own verdict and outranks the phrase list, which only covers the
// commands that answer without one.
func classify(seg string) error {
	trimmed := strings.TrimSpace(seg)
	severity, tagged := "", false
	if m := severityPrefix.FindStringSubmatch(trimmed); m != nil {
		severity, tagged = m[1], true
		trimmed = strings.TrimSpace(trimmed[len(m[0]):])
	}
	lower := strings.ToLower(trimmed)
	switch {
	case strings.Contains(lower, "already used by other proxy"):
		return ErrNameCollision
	case strings.Contains(lower, "wait delay expired"):
		return ErrWaitExpired
	case tagged && severity[0] <= '3':
		return ErrRejected
	case tagged && severity[0] >= '5':
		return nil
	}
	for _, p := range errorPhrases {
		if strings.Contains(lower, p) {
			return ErrRejected
		}
	}
	return nil
}

// matchBatch attributes a batched response to its commands. A command with a
// pinned success text is an anchor; a segment before the next anchor belongs to
// the silent commands in between, where any message at all is a failure. The
// walk stops at the first failure: the ops after it may or may not have run,
// which is exactly why a rejected op falls back to reloading the desired set.
func matchBatch(raw string, cmds []Command) []CommandResult {
	segments := splitSegments(raw)
	results := make([]CommandResult, len(cmds))
	seg := 0
	for i, cmd := range cmds {
		switch {
		case cmd.Optional:
			results[i] = matchOptional(segments, &seg, cmds[i+1:])
		case cmd.Expect == "":
			results[i] = matchQuiet(segments, &seg, cmds[i+1:])
		default:
			results[i] = matchAnchor(segments, &seg, cmd.Expect)
		}
		if results[i].Err == nil {
			continue
		}
		for j := i + 1; j < len(cmds); j++ {
			results[j] = CommandResult{Err: ErrUnreadableResponse}
		}
		break
	}
	return results
}

func matchAnchor(segments []string, seg *int, expect string) CommandResult {
	if *seg >= len(segments) {
		return CommandResult{Err: ErrUnreadableResponse}
	}
	out := segments[*seg]
	*seg++
	if err := classify(out); err != nil {
		return CommandResult{Output: out, Err: err}
	}
	if !strings.Contains(strings.ToLower(out), strings.ToLower(expect)) {
		return CommandResult{Output: out, Err: ErrUnreadableResponse}
	}
	return CommandResult{Output: out}
}

// matchOptional consumes a segment only when it is plainly the prefix's own
// chatter. An error or the next command's success message is left where it is,
// so the ops keep their verdicts.
func matchOptional(segments []string, seg *int, rest []Command) CommandResult {
	if *seg >= len(segments) {
		return CommandResult{}
	}
	out := segments[*seg]
	if classify(out) != nil || isNextAnchor(out, rest) {
		return CommandResult{}
	}
	*seg++
	return CommandResult{Output: out}
}

func matchQuiet(segments []string, seg *int, rest []Command) CommandResult {
	if *seg >= len(segments) {
		return CommandResult{}
	}
	out := segments[*seg]
	if isNextAnchor(out, rest) {
		return CommandResult{}
	}
	*seg++
	return CommandResult{Output: out, Err: classify(out)}
}

// isNextAnchor reports whether the segment is the success message of the next
// command that has one, which means the silent commands before it said nothing.
func isNextAnchor(seg string, rest []Command) bool {
	for _, c := range rest {
		if c.Expect == "" {
			continue
		}
		return strings.Contains(strings.ToLower(seg), strings.ToLower(c.Expect))
	}
	return false
}
