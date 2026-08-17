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
	"fmt"
	"path"
	"strings"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/agent/api"
)

// ErrInvalidPath is the class every path rejection wraps, so a caller can
// report one refusal reason for the whole safety check.
var ErrInvalidPath = fmt.Errorf("invalid manifest path")

// ValidatePath accepts only paths the agent may own: relative, canonical, and
// with no dot-prefixed component, which keeps the agent's own state file, its
// temp directories and its LKG directories out of every manifest.
func ValidatePath(rel string) error {
	switch {
	case rel == "":
		return fmt.Errorf("%w: empty", ErrInvalidPath)
	case len(rel) > api.MaxPathBytes:
		return fmt.Errorf("%w: %d bytes exceeds the %d-byte limit", ErrInvalidPath, len(rel), api.MaxPathBytes)
	case path.IsAbs(rel):
		return fmt.Errorf("%w: %q is absolute", ErrInvalidPath, rel)
	case path.Clean(rel) != rel:
		return fmt.Errorf("%w: %q is not canonical", ErrInvalidPath, rel)
	case strings.ContainsRune(rel, 0):
		return fmt.Errorf("%w: %q contains NUL", ErrInvalidPath, rel)
	}
	for _, part := range strings.Split(rel, "/") {
		if strings.HasPrefix(part, ".") {
			return fmt.Errorf("%w: %q has a dot-prefixed component", ErrInvalidPath, rel)
		}
	}
	return nil
}
