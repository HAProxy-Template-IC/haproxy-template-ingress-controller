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

// Package pluggablevalidator implements the controller-side client for the
// pluggable-validator-sidecar wire protocol. The authoritative wire-protocol
// specification lives at docs/development/validator-protocol.md.
package pluggablevalidator

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"math"
)

// ProtocolVersion is the current wire-protocol major version. Bumped only on
// breaking changes per docs/development/validator-protocol.md "Versioning".
const ProtocolVersion = 1

// MaxFrameSize is the largest frame (length + payload) the client will encode
// or accept on the wire. Mirrors the hub-side default
// (haproxy-spoa-hub --validate-max-frame-bytes).
const MaxFrameSize = 1 << 20 // 1 MiB

// Severity is the diagnostic severity in the wire format.
type Severity string

const (
	// SeverityWarning is a non-blocking finding.
	SeverityWarning Severity = "warning"
	// SeverityError blocks admission.
	SeverityError Severity = "error"
)

// Result is the aggregate outcome string returned in a Response.
type Result string

const (
	// ResultValid is "no errors and no warnings".
	ResultValid Result = "valid"
	// ResultWarning is "warnings but no errors".
	ResultWarning Result = "warning"
	// ResultError is "at least one error".
	ResultError Result = "error"
)

// File is one entry in a Request's files array.
//
// Path is an opaque identifier echoed back in diagnostics — the validator
// MUST NOT open it from disk. Content is the file's UTF-8 bytes (TOML text
// for hub configs).
type File struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

// Request is the JSON payload the controller writes onto a validator socket.
type Request struct {
	ProtocolVersion int    `json:"protocol_version"`
	Files           []File `json:"files"`
}

// Diagnostic is one finding in a Response.
//
// Line and Column are 1-based; 0 means "unknown / file-level". For
// protocol-level diagnostics (frame errors, version mismatch, missing
// fields), Path is the empty string.
type Diagnostic struct {
	Path    string `json:"path"`
	Line    uint32 `json:"line"`
	Column  uint32 `json:"column"`
	Message string `json:"message"`
}

// Response is the JSON payload a validator returns. Result is computed from
// the warning/error counts per the wire-protocol contract.
//
// The unexported `synthetic` field marks responses produced by
// ProtocolError as transport-level failures. Real validator responses
// (parsed via DecodeResponse) leave it false. The cache uses this
// marker to decide whether to memoise the response — synthetic ones
// represent transient sidecar outages and must NOT be cached, while
// real validator responses (including those with `path: ""`
// diagnostics, e.g. plugin panics or file-level errors) are
// deterministic functions of the input and SHOULD be cached.
//
// `synthetic` is unexported so JSON encoders skip it — the wire format
// is unchanged.
type Response struct {
	ProtocolVersion int          `json:"protocol_version"`
	Result          Result       `json:"result"`
	Warnings        []Diagnostic `json:"warnings"`
	Errors          []Diagnostic `json:"errors"`

	synthetic bool
}

// IsSynthetic reports whether the response was produced by ProtocolError
// (transport-level failure surfaced as a Response). The cache layer
// uses this to avoid memoising transient outages.
func (r *Response) IsSynthetic() bool {
	return r != nil && r.synthetic
}

// marshalRequest returns the JSON body the wire protocol carries (without
// the 4-byte length prefix). Same invariants as EncodeRequest minus the
// io.Writer step. Used by the cache to derive content-hash keys without
// double-marshaling.
func marshalRequest(req *Request) ([]byte, error) {
	if req == nil {
		return nil, fmt.Errorf("marshal request: nil")
	}
	if req.ProtocolVersion != ProtocolVersion {
		return nil, fmt.Errorf("marshal request: unsupported protocol_version %d (want %d)", req.ProtocolVersion, ProtocolVersion)
	}
	if len(req.Files) == 0 {
		return nil, fmt.Errorf("marshal request: files array must be non-empty")
	}
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}
	return body, nil
}

// EncodeRequest serialises a Request into a length-prefixed JSON frame and
// writes it to w. Returns the number of bytes written (including the 4-byte
// length prefix) or an error.
//
// The caller-supplied request MUST already have a non-empty files array and
// the expected ProtocolVersion. EncodeRequest validates these invariants
// before writing — a partial write of a malformed request would leave the
// socket in an unusable state.
func EncodeRequest(w io.Writer, req *Request) (int, error) {
	if req == nil {
		return 0, fmt.Errorf("encode request: nil request")
	}
	if req.ProtocolVersion != ProtocolVersion {
		return 0, fmt.Errorf("encode request: unsupported protocol_version %d (want %d)", req.ProtocolVersion, ProtocolVersion)
	}
	if len(req.Files) == 0 {
		return 0, fmt.Errorf("encode request: files array must be non-empty")
	}

	body, err := json.Marshal(req)
	if err != nil {
		return 0, fmt.Errorf("encode request: marshal JSON: %w", err)
	}
	if len(body) > MaxFrameSize {
		return 0, fmt.Errorf("encode request: payload size %d exceeds MaxFrameSize %d", len(body), MaxFrameSize)
	}

	header := make([]byte, 4)
	length, err := NarrowToUint32(len(body))
	if err != nil {
		return 0, fmt.Errorf("encode request: %w", err)
	}
	binary.BigEndian.PutUint32(header, length)

	n, err := w.Write(header)
	if err != nil {
		return n, fmt.Errorf("encode request: write length prefix: %w", err)
	}
	bn, err := w.Write(body)
	if err != nil {
		return n + bn, fmt.Errorf("encode request: write payload: %w", err)
	}
	return n + bn, nil
}

// DecodeResponse reads one length-prefixed JSON response frame from r and
// returns the parsed Response. The frame size MUST NOT exceed MaxFrameSize;
// oversized frames return an error without reading the body so the client
// can drop the connection.
func DecodeResponse(r io.Reader) (*Response, error) {
	header := make([]byte, 4)
	if _, err := io.ReadFull(r, header); err != nil {
		return nil, fmt.Errorf("decode response: read length prefix: %w", err)
	}
	length := binary.BigEndian.Uint32(header)
	if length == 0 {
		return nil, fmt.Errorf("decode response: zero-length frame")
	}
	if length > MaxFrameSize {
		return nil, fmt.Errorf("decode response: frame size %d exceeds MaxFrameSize %d", length, MaxFrameSize)
	}

	body := make([]byte, length)
	if _, err := io.ReadFull(r, body); err != nil {
		return nil, fmt.Errorf("decode response: read payload (%d bytes): %w", length, err)
	}

	var resp Response
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("decode response: unmarshal JSON: %w", err)
	}
	if resp.ProtocolVersion != ProtocolVersion {
		return nil, fmt.Errorf("decode response: unsupported protocol_version %d (want %d)", resp.ProtocolVersion, ProtocolVersion)
	}
	// Normalise nil slices so callers can iterate without nil checks. The
	// wire format guarantees the fields are always present (per spec) but
	// JSON decoders may leave nil for missing/null arrays.
	if resp.Warnings == nil {
		resp.Warnings = []Diagnostic{}
	}
	if resp.Errors == nil {
		resp.Errors = []Diagnostic{}
	}
	return &resp, nil
}

// NarrowToUint32 converts a non-negative int to uint32, returning an error
// if the value would overflow. The explicit bounds check lets static
// analyzers (gosec G115) prove the cast is safe — relying on caller-side
// invariants would silence the warning at the cost of correctness.
//
// Exported for test helpers in subpackages.
func NarrowToUint32(n int) (uint32, error) {
	if n < 0 {
		return 0, fmt.Errorf("negative length %d", n)
	}
	if uint64(n) > math.MaxUint32 {
		return 0, fmt.Errorf("length %d exceeds uint32 max", n)
	}
	return uint32(n), nil
}

// ProtocolError builds a synthetic error-severity Response carrying a single
// protocol-level Diagnostic with the given message. Used when the client
// needs to surface a frame-level failure (oversized frame, malformed JSON,
// connection refused) as a Response so callers don't have to switch on a
// separate error type.
//
// The returned Response is marked synthetic so the cache layer can skip
// it. Real validator responses with a `path: ""` diagnostic (file-level
// errors, plugin panics surfaced by the sidecar) are NOT synthetic and
// will be cached normally.
func ProtocolError(message string) *Response {
	return &Response{
		ProtocolVersion: ProtocolVersion,
		Result:          ResultError,
		Warnings:        []Diagnostic{},
		Errors: []Diagnostic{
			{Path: "", Line: 0, Column: 0, Message: message},
		},
		synthetic: true,
	}
}
