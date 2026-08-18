// Copyright 2026 Philipp Hossner
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

// Package planblob is the encoding of the opaque plan a pod stores and hands
// back on /v1/state: zstd over the plan's JSON, raw bytes on the wire.
// pkg/compression base64s its output, which the multipart part has no use for.
//
// Both ends of the blob compile this package — the controller writes it, the
// controller and `haptic diff` read it back — so a pod's baseline is decoded by
// exactly one implementation.
package planblob

import (
	"encoding/json"
	"fmt"

	"github.com/klauspost/compress/zstd"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/agent/api"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/renderplan"
)

var codec = struct {
	encoder *zstd.Encoder
	decoder *zstd.Decoder
}{}

func init() {
	encoder, err := zstd.NewWriter(nil,
		zstd.WithEncoderLevel(zstd.SpeedDefault),
		zstd.WithEncoderConcurrency(1))
	if err != nil {
		panic("planblob: creating the encoder: " + err.Error())
	}
	decoder, err := zstd.NewReader(nil, zstd.WithDecoderMaxMemory(api.MaxApplyBodyBytes))
	if err != nil {
		panic("planblob: creating the decoder: " + err.Error())
	}
	codec.encoder = encoder
	codec.decoder = decoder
}

// Encode produces the blob for one plan. It carries the plan id, so a decode
// can prove what it decoded.
func Encode(plan *renderplan.Plan) ([]byte, error) {
	if plan == nil {
		return nil, fmt.Errorf("planblob: no plan to encode")
	}
	encoded, err := json.Marshal(plan)
	if err != nil {
		return nil, fmt.Errorf("encoding plan %s: %w", plan.ID, err)
	}
	blob := codec.encoder.EncodeAll(encoded, nil)
	if len(blob) > api.MaxPlanBlobBytes {
		return nil, fmt.Errorf("plan %s compresses to %d bytes, over the %d-byte limit", plan.ID, len(blob), api.MaxPlanBlobBytes)
	}
	return blob, nil
}

// Decode reads a blob back. The caller checks the plan id and schema version
// against what the pod reports: a plan that decodes is not yet a plan that
// describes this pod.
func Decode(blob []byte) (*renderplan.Plan, error) {
	decoded, err := codec.decoder.DecodeAll(blob, nil)
	if err != nil {
		return nil, fmt.Errorf("decompressing plan blob: %w", err)
	}
	var plan renderplan.Plan
	if err := json.Unmarshal(decoded, &plan); err != nil {
		return nil, fmt.Errorf("decoding plan blob: %w", err)
	}
	return &plan, nil
}
