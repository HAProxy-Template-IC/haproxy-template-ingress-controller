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

package client

import (
	"fmt"
	"io"
	"path"
	"strings"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/agent/api"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/deployplan"
)

// validateApply asserts the contract's limits before a byte is sent. The
// agent asserts the same ones; a violation is a controller bug, and failing
// here names it at its source instead of as a remote 4xx.
func validateApply(m *api.Manifest, parts map[string]io.Reader, manifestBytes int) error {
	if m.PlanID == "" {
		return fmt.Errorf("agent client: manifest has no plan_id")
	}
	if err := validateMode(m.Mode); err != nil {
		return err
	}
	if len(m.Files) > api.MaxFiles {
		return fmt.Errorf("agent client: %d files exceeds the limit of %d", len(m.Files), api.MaxFiles)
	}
	if ops := len(m.Ops) + len(m.InPlaceOps); ops > api.MaxOpsPerApply {
		return fmt.Errorf("agent client: %d ops exceeds the limit of %d", ops, api.MaxOpsPerApply)
	}

	declared := make(map[string]int64, len(m.Files))
	for i := range m.Files {
		f := &m.Files[i]
		if err := validatePath(f.Path); err != nil {
			return err
		}
		if _, dup := declared[f.Path]; dup {
			return fmt.Errorf("agent client: file %q is declared twice", f.Path)
		}
		if f.Size < 0 {
			return fmt.Errorf("agent client: file %q has a negative size", f.Path)
		}
		if f.Digest == "" {
			return fmt.Errorf("agent client: file %q has no digest", f.Path)
		}
		declared[f.Path] = f.Size
	}

	body := int64(manifestBytes)
	for p := range parts {
		size, ok := declared[p]
		if !ok {
			return fmt.Errorf("agent client: part %q is not a file of this manifest", p)
		}
		body += size
	}
	if body > api.MaxApplyBodyBytes {
		return fmt.Errorf("agent client: apply body of %d bytes exceeds the limit of %d", body, api.MaxApplyBodyBytes)
	}
	return nil
}

func validateMode(mode string) error {
	switch mode {
	case api.ModeAuto, api.ModeReload, api.ModeRevertLKG:
		return nil
	default:
		return fmt.Errorf("agent client: unknown apply mode %q", mode)
	}
}

// validatePath enforces the contract's path grammar: relative to the agent's
// base dir, no escape, no NUL, bounded.
func validatePath(p string) error {
	switch {
	case p == "":
		return fmt.Errorf("agent client: manifest has an empty file path")
	case len(p) > api.MaxPathBytes:
		return fmt.Errorf("agent client: file path %q exceeds %d bytes", p, api.MaxPathBytes)
	case strings.HasPrefix(p, "/"):
		return fmt.Errorf("agent client: file path %q must be relative to the base dir", p)
	case strings.Contains(p, "\x00"):
		return fmt.Errorf("agent client: file path %q contains a NUL byte", p)
	case path.Clean(p) != p:
		return fmt.Errorf("agent client: file path %q is not in cleaned form", p)
	case p == ".." || strings.HasPrefix(p, "../"):
		return fmt.Errorf("agent client: file path %q escapes the base dir", p)
	}
	return nil
}

// ComposableOps returns the op kinds this controller composes — the set
// CheckSkew measures an agent against. It is deployplan's own list, so the
// skew check cannot drift from what the decision layer emits.
func ComposableOps() []string {
	return deployplan.ComposedOps()
}

// CheckSkew compares an agent's reported contract with this controller's.
// Either finding means that pod gets full state and mode reload — never a
// refusal, because a fleet-correlated refusal would fence the repair path.
func CheckSkew(state *api.State) (majorMismatch bool, missingOps []string) {
	if state == nil {
		return true, nil
	}
	executes := make(map[string]struct{}, len(state.AgentOps))
	for _, op := range state.AgentOps {
		executes[op] = struct{}{}
	}
	composable := deployplan.ComposedOps()
	missing := make([]string, 0, len(composable))
	for _, op := range composable {
		if _, ok := executes[op]; !ok {
			missing = append(missing, op)
		}
	}
	if len(missing) == 0 {
		missing = nil
	}
	return state.APIVersion != api.Version, missing
}
