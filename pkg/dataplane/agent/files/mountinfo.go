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

// Package files owns the manifest-managed file tree of an HAProxy pod: the
// mount probe, tree hashing, part verification, the backup journal and the
// transactional write. Disk is the authority; nothing here parses HAProxy
// syntax.
package files

import (
	"bufio"
	"io"
	"strconv"
	"strings"
)

// mountPointsIn parses mountinfo lines (mount point is the fifth field, with
// spaces and other specials octal-escaped) and keeps those below root.
func mountPointsIn(r io.Reader, root string) ([]string, error) {
	var points []string
	prefix := root + "/"
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 5 {
			continue
		}
		point := unescapeMountField(fields[4])
		if strings.HasPrefix(point, prefix) {
			points = append(points, point)
		}
	}
	return points, scanner.Err()
}

func unescapeMountField(s string) string {
	if !strings.Contains(s, "\\") {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+3 < len(s) {
			if v, err := strconv.ParseUint(s[i+1:i+4], 8, 8); err == nil {
				b.WriteByte(byte(v))
				i += 3
				continue
			}
		}
		b.WriteByte(s[i])
	}
	return b.String()
}
