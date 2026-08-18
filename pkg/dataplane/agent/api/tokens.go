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

package api

import "strings"

// unsafeRunes are the characters that change the meaning of a CLI line: the
// command separator, the payload introducer's parts, the line terminators and
// the escape HAProxy's line form consumes.
const unsafeRunes = ";<>\\\n\r\t\x00"

// SafeToken reports whether s can travel as one word of a runtime command — a
// name, a path, a map key, a keyword or a keyword argument. Both ends compile
// it: the controller never composes an op whose tokens fail it, and the agent
// refuses to execute one, so the two cannot disagree on what is sendable.
// Whitespace is rejected because HAProxy splits the line on it, which would
// silently turn one word into two.
func SafeToken(s string) bool {
	if s == "" || strings.ContainsAny(s, unsafeRunes) || strings.ContainsRune(s, ' ') {
		return false
	}
	for _, r := range s {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	return true
}

// SafePayloadValue reports whether s can travel inside a payload block, where
// only the line framing is significant.
func SafePayloadValue(s string) bool {
	return !strings.ContainsAny(s, "\n\r\x00")
}
