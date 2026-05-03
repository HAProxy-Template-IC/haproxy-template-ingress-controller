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
// documented in `docs/development/validator-protocol.md`. The reference
// implementation is `haproxy-spoa-hub --validate-socket <path>` shipped
// alongside HAPTIC; any conforming implementation may be substituted.
//
// Operators declare zero, one, or many validators. Each one runs in its
// own sidecar container in the controller pod and shares a Unix domain
// socket via an emptyDir volume; the chart wires this up automatically
// when `spec.validators` is non-empty.
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

	// Plugins lists the `[plugins.params.<name>]` subtree names this
	// validator handles in the rendered hub TOML.
	//
	// An empty list (the default) means "validate the whole hub TOML";
	// the validator decides what to do with the full config (typically
	// it dispatches to every loaded plugin's `validate()`). Listing
	// specific plugins is a forward-compatible hook for the case where
	// multiple validators each handle a disjoint subset; it is unused
	// by the current single-validator-per-hub deployment shape but
	// kept in the schema so a future change does not require a CRD
	// version bump.
	// +optional
	Plugins []string `json:"plugins,omitempty"`

	// TimeoutMs is the per-call deadline, in milliseconds, covering
	// connect + write + read.
	//
	// Defaults to 5000 (5 seconds) when omitted. Validator calls
	// exceeding this deadline yield a synthetic admission denial with
	// `result: "error"` so a wedged sidecar does not stall the webhook
	// path indefinitely.
	// +optional
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=60000
	TimeoutMs *int32 `json:"timeoutMs,omitempty"`
}
