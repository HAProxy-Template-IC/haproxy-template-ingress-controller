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

package templating

import (
	"reflect"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestImmutableCertificateIndexMatchesLinearScan(t *testing.T) {
	views := make([]*incrementalImmutableCertificateView, 97)
	index := newImmutableCertificateIndex(0)
	for position := range views {
		start := uintptr((position * 7919) % 4000)
		ranges := []immutableRange{
			{start: start, end: start + uintptr(1+(position*37)%96)},
			{start: start + 200, end: start + 201},
		}
		identities := []immutableIdentity{
			{kind: reflect.Map, ptr: uintptr(position % 31)},
			{kind: reflect.Pointer, ptr: uintptr(5000 + position)},
		}
		certificate := newIncrementalImmutableCertificate(identities, false, ranges, nil)
		views[position] = certificate.view
		index.add(certificate.view)
	}
	targets := make([]immutableTarget, 0, 4500)
	for pointer := range uintptr(4400) {
		targets = append(targets, immutableTarget{
			identity: immutableIdentity{kind: reflect.Pointer, ptr: pointer},
			pointer:  pointer,
		})
	}
	for ptr := range uintptr(40) {
		targets = append(targets, immutableTarget{identity: immutableIdentity{kind: reflect.Map, ptr: ptr}, pointer: ptr})
	}
	for position := range views {
		ptr := uintptr(5000 + position)
		targets = append(targets, immutableTarget{identity: immutableIdentity{kind: reflect.Pointer, ptr: ptr}, pointer: ptr})
	}
	assertImmutableCertificateIndexMatchesLinearScan(t, index, views, targets)

	for position := 0; position < len(views); position += 3 {
		views[position].seal = nil
	}
	assertImmutableCertificateIndexMatchesLinearScan(t, index, views, targets)
}

func assertImmutableCertificateIndexMatchesLinearScan(
	t *testing.T,
	index *immutableCertificateIndex,
	views []*incrementalImmutableCertificateView,
	targets []immutableTarget,
) {
	t.Helper()
	for _, target := range targets {
		want := false
		for _, view := range views {
			if view.containsRegisteredTarget(target) {
				want = true
				break
			}
		}
		require.Equal(t, want, index.contains(target), "target %+v", target)
	}
}

func TestPromotedIncrementalImmutableCertificatesRejectPoisonedView(t *testing.T) {
	ctx := WithImmutableResourceInputs(t.Context())
	anchors := make([]map[string]any, 32)
	certificates := make([]*IncrementalImmutableCertificate, len(anchors))
	for position := range anchors {
		anchors[position] = map[string]any{"value": position}
		certificates[position] = CertifyIncrementalImmutableInputs(anchors[position])
		require.NoError(t, RegisterIncrementalImmutableCertificate(ctx, certificates[position]))
	}
	storage := ctx.Value(immutableStorageContextKey{}).(*immutableStorage)
	require.Equal(t, len(anchors), storage.certified.len())
	require.NoError(t, RegisterIncrementalImmutableCertificate(ctx, certificates[3]))
	require.Equal(t, len(anchors), storage.certified.len())

	certificates[20].view.seal = nil

	for position, anchor := range anchors {
		assert.Equal(t, position != 20, storage.contains(reflect.ValueOf(anchor)), "anchor %d", position)
	}
}
