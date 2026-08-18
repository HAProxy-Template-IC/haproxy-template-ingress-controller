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

// Package api is the wire contract between the controller and the HAPTIC
// agent (the container that owns an HAProxy pod's file tree and runtime
// socket). Both ends compile this one package: the controller composes an
// Apply, the agent executes it verbatim and reports. Additive changes only
// within a major version; a breaking change is a new major with an overlap.
package api

// Version is the API major version an agent reports and a client compares.
const Version = 1

// Paths of the two calls plus health.
const (
	PathState   = "/v1/state"
	PathApply   = "/v1/apply"
	PathHealthz = "/healthz"
	PathReadyz  = "/readyz"
)

// Multipart part names of an Apply request. Every other part is a file whose
// filename is its manifest path.
const (
	PartManifest = "manifest"
	PartPlan     = "plan"
)

// File kinds, as the render declares them (renderplan.File.Kind).
const (
	FileKindConfig  = "config"
	FileKindMap     = "map"
	FileKindCert    = "cert"
	FileKindCA      = "ca"
	FileKindCRTList = "crtlist"
	FileKindGeneral = "general"
)

// Apply modes.
const (
	ModeAuto      = "auto"       // run ops when the baseline matches, else reload
	ModeReload    = "reload"     // write and reload, ops ignored
	ModeRevertLKG = "revert_lkg" // restore the last-known-good set and reload
)

// Manifest is the JSON part of an Apply: the complete desired file set at
// digest granularity, the ops composed for this pod, and the fencing state.
type Manifest struct {
	PlanID            string `json:"plan_id"`
	PlanSchemaVersion int    `json:"plan_schema_version"`
	Token             Token  `json:"token"`
	// ExpectedPrevPlanID and ExpectedPrevToken are the baseline the ops were
	// composed against; a mismatch is a 409, never a write.
	ExpectedPrevPlanID string `json:"expected_prev_plan_id"`
	ExpectedPrevToken  Token  `json:"expected_prev_token"`
	// ExpectedWorkerOpsPlanID guards InPlaceOps; WorkerOpsPlanID is what the
	// pod records once they ran: the id of the worker's plan with exactly those
	// ops applied, which the controller can reproduce. It is not PlanID — the
	// in-place subset never brings the worker all the way to the render.
	ExpectedWorkerOpsPlanID string `json:"expected_worker_ops_plan_id,omitempty"`
	WorkerOpsPlanID         string `json:"worker_ops_plan_id,omitempty"`
	// ValidatedPlanID is the newest plan the controller's haproxy -c passed;
	// the agent promotes its rollback baseline when it equals the applied plan.
	ValidatedPlanID string `json:"validated_plan_id,omitempty"`
	Files           []File `json:"files"`
	Ops             []Op   `json:"ops,omitempty"`
	InPlaceOps      []Op   `json:"in_place_ops,omitempty"`
	Mode            string `json:"mode"`
}

// Token is the fencing token: a leader epoch (CAS-incremented on the Lease
// at leadership start) and a per-epoch render sequence.
type Token struct {
	LeaderEpoch uint64 `json:"leader_epoch"`
	RenderSeq   uint64 `json:"render_seq"`
}

// File is one desired file. Content travels as a multipart part only when
// the agent does not hold this digest.
type File struct {
	Path           string `json:"path"` // relative to the agent's base dir, no "..", no leading "/"
	Digest         string `json:"digest"`
	Size           int64  `json:"size"`
	Kind           string `json:"kind"`
	ReloadOnChange bool   `json:"reload_on_change"`
}

// Op kinds the agent executes. Unknown kinds are refused and the apply falls
// back to a reload (fail closed).
const (
	OpBackendAdd           = "backend_add"        // Backend, Profile, Mode, GUID
	OpBackendPublish       = "backend_publish"    // Backend
	OpBackendUnpublish     = "backend_unpublish"  // Backend
	OpBackendDel           = "backend_del"        // Backend
	OpBackendWaitRemovable = "wait_be_removable"  // Backend, TimeoutMs
	OpServerAdd            = "server_add"         // Backend, Server, Address, Port, Keywords
	OpServerEnable         = "server_enable"      // Backend, Server, Health
	OpServerDisable        = "server_disable"     // Backend, Server
	OpServerSetAddr        = "server_set_addr"    // Backend, Server, Address, Port
	OpServerSetWeight      = "server_set_weight"  // Backend, Server, Weight
	OpServerSetState       = "server_set_state"   // Backend, Server, State (ready|maint|drain)
	OpServerWaitRemovable  = "wait_srv_removable" // Backend, Server, TimeoutMs
	OpShutdownSessions     = "shutdown_sessions"  // Backend, Server
	OpServerDel            = "server_del"         // Backend, Server
	OpMapAdd               = "map_add"            // Path, Key, Value (payload form)
	OpMapSet               = "map_set"            // Path, Key, Value (line form; value must be line-safe)
	OpMapDel               = "map_del"            // Path, Key (every value of the key)
	OpMapReplace           = "map_replace"        // Path (versioned atomic replace from the file part)
	OpCertSet              = "cert_set"           // Path (set + commit from the file part)
	OpCertNew              = "cert_new"           // Path (new + set + commit)
	OpCASet                = "ca_set"             // Path
	OpCANew                = "ca_new"             // Path
	OpCRTListAdd           = "crtlist_add"        // Path (list), Cert (the crt-list line token: bare under crt-base), Options, SNIFilters (payload form)
	OpCRTListDel           = "crtlist_del"        // Path (list), Cert (as in crtlist_add)
)

// Op is one typed runtime command. Fields not used by a kind are empty. Every
// string is validated by the agent against its keyword grammar; tokens never
// carry ';', a newline, "<<" or a backslash.
type Op struct {
	Kind       string       `json:"kind"`
	Backend    string       `json:"backend,omitempty"`
	Server     string       `json:"server,omitempty"`
	Profile    string       `json:"profile,omitempty"`
	Mode       string       `json:"mode,omitempty"`
	GUID       string       `json:"guid,omitempty"`
	Address    string       `json:"address,omitempty"`
	Port       int          `json:"port,omitempty"`
	Weight     *int         `json:"weight,omitempty"`
	State      string       `json:"state,omitempty"`
	Health     bool         `json:"health,omitempty"`
	Keywords   []KeywordArg `json:"keywords,omitempty"`
	TimeoutMs  int          `json:"timeout_ms,omitempty"`
	Path       string       `json:"path,omitempty"`
	Key        string       `json:"key,omitempty"`
	Value      string       `json:"value,omitempty"`
	Cert       string       `json:"cert,omitempty"`
	Options    []KeywordArg `json:"options,omitempty"`
	SNIFilters []string     `json:"sni_filters,omitempty"`
}

// KeywordArg is one HAProxy keyword with its arguments, as data.
type KeywordArg struct {
	Name string   `json:"name"`
	Args []string `json:"args,omitempty"`
}

// State is the response of GET /v1/state.
type State struct {
	APIVersion        int               `json:"api_version"`
	AgentVersion      string            `json:"agent_version"`
	PlanSchemaVersion int               `json:"plan_schema_version"`
	AgentOps          []string          `json:"agent_ops"` // op kinds this agent executes
	HAProxy           HAProxyInfo       `json:"haproxy"`
	Generation        uint64            `json:"generation"`
	AppliedPlanID     string            `json:"applied_plan_id"`
	RunningPlanID     string            `json:"running_plan_id"`
	WorkerOpsPlanID   string            `json:"worker_ops_plan_id"`
	AppliedToken      Token             `json:"applied_token"`
	LKGPlanID         string            `json:"lkg_plan_id"`
	AppliedPlan       []byte            `json:"applied_plan,omitempty"` // opaque, what the controller sent
	Files             map[string]FileAt `json:"files"`
	Inventory         Inventory         `json:"runtime_inventory"`
	ReloadPendingAt   string            `json:"reload_pending_at,omitempty"` // RFC 3339
	PendingDeletes    PendingDeletes    `json:"pending_deletes"`
	LastApply         *ApplyResult      `json:"last_apply,omitempty"`
}

// HAProxyInfo is what the agent learned from the worker (`show info`).
type HAProxyInfo struct {
	Version     string `json:"version"`
	FullVersion string `json:"full_version"`
	WorkerPID   int    `json:"worker_pid"`
}

// FileAt is a file the agent holds.
type FileAt struct {
	Digest string `json:"digest"`
	Size   int64  `json:"size"`
}

// Inventory is what the running worker has loaded, refreshed after reloads
// and on a worker identity change.
type Inventory struct {
	Generation uint64   `json:"generation"`
	Maps       []string `json:"maps"`
	Certs      []string `json:"certs"`
	CAFiles    []string `json:"ca_files"`
	CRLFiles   []string `json:"crl_files"`
	CRTLists   []string `json:"crt_lists"`
}

// PendingDeletes are deferred runtime deletes still to complete.
type PendingDeletes struct {
	Servers  []string `json:"servers"`  // backend/server
	Backends []string `json:"backends"` // unpublished, awaiting del
}

// Apply outcome modes.
const (
	ResultRuntime   = "runtime"
	ResultFileOnly  = "file_only"
	ResultReload    = "reload"
	ResultScheduled = "scheduled"
	ResultNoop      = "noop"
	ResultRejected  = "rejected"
)

// ApplyResult is the response of POST /v1/apply: the ACK or NACK.
type ApplyResult struct {
	PlanID          string        `json:"plan_id"`
	OK              bool          `json:"ok"`
	Mode            string        `json:"mode"`
	AppliedPlanID   string        `json:"applied_plan_id"`
	RunningPlanID   string        `json:"running_plan_id"`
	WorkerOpsPlanID string        `json:"worker_ops_plan_id"`
	AppliedToken    Token         `json:"applied_token"`
	LKGPlanID       string        `json:"lkg_plan_id"`
	OpResults       []OpResult    `json:"op_results,omitempty"`
	Reload          *ReloadInfo   `json:"reload,omitempty"`
	Rollback        *RollbackInfo `json:"rollback,omitempty"`
	Error           *ApplyError   `json:"error,omitempty"`
	HAProxy         HAProxyInfo   `json:"haproxy"`
	Inventory       *Inventory    `json:"runtime_inventory,omitempty"` // present when its generation advanced
	At              string        `json:"at"`                          // RFC 3339
}

// OpResult is one executed op's verdict.
type OpResult struct {
	Kind   string `json:"kind"`
	OK     bool   `json:"ok"`
	Output string `json:"output,omitempty"`
}

// ReloadInfo describes the reload an apply performed or scheduled.
type ReloadInfo struct {
	Performed   bool   `json:"performed"`
	OK          bool   `json:"ok"`
	WorkerPID   int    `json:"worker_pid,omitempty"`
	TookMs      int64  `json:"took_ms,omitempty"`
	Output      string `json:"output,omitempty"`
	ScheduledAt string `json:"scheduled_at,omitempty"`
}

// RollbackInfo describes an abort's restore.
type RollbackInfo struct {
	Performed bool `json:"performed"`
	Reloaded  bool `json:"reloaded"`
}

// ApplyError names the stage that failed and HAProxy's own words.
type ApplyError struct {
	Stage   string `json:"stage"`
	Message string `json:"message"`
}

// Conflict is the body of a 409 baseline mismatch: the agent's actual
// baseline, so the caller can re-diff.
type Conflict struct {
	AppliedPlanID   string `json:"applied_plan_id"`
	AppliedToken    Token  `json:"applied_token"`
	RunningPlanID   string `json:"running_plan_id"`
	WorkerOpsPlanID string `json:"worker_ops_plan_id"`
	LKGPlanID       string `json:"lkg_plan_id"`
	Reason          string `json:"reason"` // prev_mismatch|stale_epoch|unknown_baseline|worker_ops_mismatch
}

// Missing is the body of a 409 when file parts are missing: resend these.
type Missing struct {
	Missing []string `json:"missing"`
}
