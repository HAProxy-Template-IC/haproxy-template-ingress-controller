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

package pluggablevalidator

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"strings"
	"testing"
)

func TestEncodeRequest_Roundtrip(t *testing.T) {
	req := &Request{
		ProtocolVersion: ProtocolVersion,
		Files: []File{
			{Path: "hub-config.toml", Content: "[hub]\nlisten = \"0.0.0.0:9000\"\n"},
		},
	}

	var buf bytes.Buffer
	n, err := EncodeRequest(&buf, req)
	if err != nil {
		t.Fatalf("EncodeRequest: %v", err)
	}
	if n != buf.Len() {
		t.Fatalf("EncodeRequest returned n=%d but wrote %d bytes", n, buf.Len())
	}
	if buf.Len() < 4 {
		t.Fatalf("frame too short: %d bytes (need at least 4 for length prefix)", buf.Len())
	}

	header := buf.Next(4)
	bodyLen := binary.BigEndian.Uint32(header)
	if int(bodyLen) != buf.Len() {
		t.Fatalf("length prefix says %d bytes, body has %d", bodyLen, buf.Len())
	}

	var got Request
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal request body: %v", err)
	}
	if got.ProtocolVersion != ProtocolVersion {
		t.Fatalf("protocol_version=%d want %d", got.ProtocolVersion, ProtocolVersion)
	}
	if len(got.Files) != 1 || got.Files[0].Path != "hub-config.toml" {
		t.Fatalf("files mismatch: %+v", got.Files)
	}
}

func TestEncodeRequest_RejectsEmptyFiles(t *testing.T) {
	req := &Request{ProtocolVersion: ProtocolVersion, Files: nil}

	var buf bytes.Buffer
	n, err := EncodeRequest(&buf, req)
	if err == nil {
		t.Fatalf("expected error for empty files array, wrote %d bytes", n)
	}
	if buf.Len() != 0 {
		t.Fatalf("encoder wrote %d bytes despite rejecting input — must be all-or-nothing", buf.Len())
	}
}

func TestEncodeRequest_RejectsWrongProtocolVersion(t *testing.T) {
	req := &Request{
		ProtocolVersion: 2,
		Files:           []File{{Path: "x", Content: "y"}},
	}

	var buf bytes.Buffer
	if _, err := EncodeRequest(&buf, req); err == nil {
		t.Fatal("expected error for protocol_version=2")
	}
	if buf.Len() != 0 {
		t.Fatalf("encoder wrote %d bytes despite rejecting input", buf.Len())
	}
}

func TestEncodeRequest_RejectsOversizedPayload(t *testing.T) {
	// Build a Files entry whose content alone exceeds MaxFrameSize so the
	// JSON encoding cannot fit. The encoder must reject before writing
	// anything.
	huge := strings.Repeat("a", MaxFrameSize+1)
	req := &Request{
		ProtocolVersion: ProtocolVersion,
		Files:           []File{{Path: "x", Content: huge}},
	}

	var buf bytes.Buffer
	if _, err := EncodeRequest(&buf, req); err == nil {
		t.Fatal("expected error for oversized payload")
	}
	if buf.Len() != 0 {
		t.Fatalf("encoder wrote %d bytes despite oversized payload", buf.Len())
	}
}

func TestDecodeResponse_HappyPath(t *testing.T) {
	resp := &Response{
		ProtocolVersion: ProtocolVersion,
		Result:          ResultError,
		Warnings:        []Diagnostic{},
		Errors: []Diagnostic{
			{Path: "hub-config.toml", Line: 6, Column: 0, Message: "unknown directive 'secresquestbodyaccess'"},
		},
	}
	frame := encodeFrameForTest(t, resp)

	got, err := DecodeResponse(bytes.NewReader(frame))
	if err != nil {
		t.Fatalf("DecodeResponse: %v", err)
	}
	if got.Result != ResultError {
		t.Fatalf("result=%q want %q", got.Result, ResultError)
	}
	if len(got.Errors) != 1 || got.Errors[0].Line != 6 {
		t.Fatalf("errors mismatch: %+v", got.Errors)
	}
	// Field-presence guarantees: warnings/errors slices must be
	// non-nil even if the wire JSON happened to omit them.
	if got.Warnings == nil {
		t.Fatal("Warnings slice is nil; decoder must normalise to []Diagnostic{}")
	}
}

func TestDecodeResponse_RejectsZeroLength(t *testing.T) {
	frame := []byte{0, 0, 0, 0}
	if _, err := DecodeResponse(bytes.NewReader(frame)); err == nil {
		t.Fatal("expected error for zero-length frame")
	}
}

func TestDecodeResponse_RejectsOversizedFrame(t *testing.T) {
	// Length prefix says MaxFrameSize+1 but no body provided; we expect
	// the decoder to reject on the length check before reading.
	frame := make([]byte, 4)
	binary.BigEndian.PutUint32(frame, uint32(MaxFrameSize+1))
	if _, err := DecodeResponse(bytes.NewReader(frame)); err == nil {
		t.Fatal("expected error for oversized frame")
	}
}

func TestDecodeResponse_RejectsMalformedJSON(t *testing.T) {
	body := []byte("{this is not json")
	length, err := NarrowToUint32(len(body))
	if err != nil {
		t.Fatalf("encode body length: %v", err)
	}
	frame := make([]byte, 4+len(body))
	binary.BigEndian.PutUint32(frame[:4], length)
	copy(frame[4:], body)

	if _, err := DecodeResponse(bytes.NewReader(frame)); err == nil {
		t.Fatal("expected error for malformed JSON")
	}
}

func TestDecodeResponse_RejectsWrongProtocolVersion(t *testing.T) {
	resp := &Response{ProtocolVersion: 2, Result: ResultValid}
	frame := encodeFrameForTest(t, resp)
	if _, err := DecodeResponse(bytes.NewReader(frame)); err == nil {
		t.Fatal("expected error for protocol_version=2")
	}
}

func TestProtocolError(t *testing.T) {
	got := ProtocolError("something broke")
	if got.Result != ResultError {
		t.Fatalf("result=%q want %q", got.Result, ResultError)
	}
	if len(got.Errors) != 1 {
		t.Fatalf("errors len=%d want 1", len(got.Errors))
	}
	if got.Errors[0].Path != "" {
		t.Fatalf("protocol-level diagnostic must have Path=\"\", got %q", got.Errors[0].Path)
	}
	if got.Errors[0].Message != "something broke" {
		t.Fatalf("message mismatch: %q", got.Errors[0].Message)
	}
}

// encodeFrameForTest builds a length-prefixed JSON frame from any value.
// Test-only helper: skips the encoder's invariant checks so we can stage
// frames with deliberately-bad fields (wrong protocol_version, malformed
// JSON, etc.).
func encodeFrameForTest(t *testing.T, v any) []byte {
	t.Helper()
	body, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	length, err := NarrowToUint32(len(body))
	if err != nil {
		t.Fatalf("encode body length: %v", err)
	}
	frame := make([]byte, 4+len(body))
	binary.BigEndian.PutUint32(frame[:4], length)
	copy(frame[4:], body)
	return frame
}
