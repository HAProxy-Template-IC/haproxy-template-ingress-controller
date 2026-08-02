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

package v1alpha1

// ValidatorConfig declares one pluggable validator sidecar that the
// controller will consult during admission webhook processing.
//
// The wire protocol between the controller and a validator sidecar is
// documented in `docs/development/validator-protocol.md` and is owned
// by HAPTIC. Reference implementation: `haproxy-spoa-hub
// --validate-socket <path>` shipped alongside HAPTIC; any conforming
// implementation may be substituted. The validator program is opaque
// to HAPTIC — its internal architecture (whether it has plugins, how
// it dispatches files, how it parses content) is its own concern.
//
// Operators declare zero, one, or many validators. Each one runs in
// its own sidecar container in the controller pod and shares a Unix
// domain socket via an emptyDir volume; the chart wires this up
// automatically when `spec.validators` is non-empty.
//
// Routing: HAPTIC matches each rendered file's path against every
// validator's `files` glob list. Files that match are sent to that
// validator over its socket. A file that matches multiple validators'
// globs is sent to each of them. Files that match no validator's
// globs are not validated by any sidecar (they still flow through the
// existing template + HAProxy syntax dry-run).
type ValidatorConfig struct {
	// Name is the operator-facing identifier for this validator.
	//
	// MUST be a valid RFC 1123 label (lowercase alphanumeric and `-`,
	// 1-63 characters). MUST be unique across the validators array.
	// Surfaces in admission denial messages so operators can identify
	// which validator rejected a change.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=63
	// +kubebuilder:validation:Pattern=`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`
	Name string `json:"name"`

	// SocketPath is the absolute filesystem path to the validator's
	// Unix domain socket inside the controller pod.
	//
	// The chart-rendered shared `emptyDir` volume mounts at
	// `/var/run/haptic-validators/`; the conventional path is
	// `/var/run/haptic-validators/<name>.sock`. Custom paths must
	// still resolve inside the controller pod's filesystem.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:Pattern=`^/`
	SocketPath string `json:"socketPath"`

	// Files is a list of glob patterns matched against rendered file
	// paths to decide which files to send to this validator.
	//
	// Glob syntax follows Go's `path/filepath.Match` rules:
	//   - `*` matches any run of non-`/` characters.
	//   - `?` matches any single non-`/` character.
	//   - `[a-z]` matches any character in the range.
	//   - `**` is NOT supported; use multiple entries to cover
	//     directory hierarchies.
	//
	// At least one glob MUST be specified. Each glob MUST be an
	// absolute path (start with `/`) so it matches the rendered file
	// paths that the controller produces (e.g. `/etc/haproxy/maps/host.map`,
	// `/etc/haproxy-spoa-hub/config.toml`). Operators get the matching
	// behaviour they expect from a path-style glob.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinItems=1
	// +listType=set
	Files []string `json:"files"`

	// DataFiles lists glob patterns for files this validator needs in order
	// to check the files it validates, but must not validate on their own.
	//
	// Every matching file is attached to every request sent to this
	// validator, marked as data. The validator does not parse them; it
	// resolves references from the validated file into them.
	//
	// A WAF ruleset is the motivating case: the validator sidecar runs in
	// the controller pod and cannot read the HAProxy pod's filesystem, so a
	// hub config that `Include`s a ruleset by path can only be checked if
	// the ruleset's content travels with the request.
	//
	// A file matching both `files` and `dataFiles` is treated as data —
	// validating a reference target standalone reports on the wrong thing.
	// +optional
	// +listType=set
	DataFiles []string `json:"dataFiles,omitempty"`

	// TimeoutMs is the per-call deadline, in milliseconds, covering
	// the request-response cycle for one file (connect-or-acquire +
	// write + read).
	//
	// Defaults to 5000 (5 seconds) when omitted. Validator calls
	// exceeding this deadline are surfaced as `result: "error"` so a
	// wedged sidecar does not stall the webhook path indefinitely.
	// +optional
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=60000
	TimeoutMs *int32 `json:"timeoutMs,omitempty"`

	// MaxConnections caps the size of the controller's connection
	// pool to this validator's socket. The pool starts small (one
	// idle connection) and grows on contention up to this cap;
	// connections idle past the validator's idle timeout are reaped
	// transparently and reopened on next use.
	//
	// Defaults to 4 when omitted. Setting 1 reproduces a serial
	// client (one validation in flight at a time). Higher values
	// allow more concurrent validations during reconciliation bursts
	// at the cost of file descriptors and validator-side resource
	// pressure.
	// +optional
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=32
	MaxConnections *int32 `json:"maxConnections,omitempty"`
}
