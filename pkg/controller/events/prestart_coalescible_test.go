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

package events

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/k8s/types"
)

func TestResourceIndexUpdatedEvent_PreStartCoalesceKeyIsPerKind(t *testing.T) {
	a := NewResourceIndexUpdatedEvent("services", types.ChangeStats{})
	b := NewResourceIndexUpdatedEvent("endpoints", types.ChangeStats{})

	assert.NotEqual(t, a.PreStartCoalesceKey(), b.PreStartCoalesceKey(),
		"distinct watched kinds must never merge")
	assert.Equal(t, a.PreStartCoalesceKey(),
		NewResourceIndexUpdatedEvent("services", types.ChangeStats{Created: 7}).PreStartCoalesceKey(),
		"the key must not depend on the payload")
}

func TestResourceIndexUpdatedEvent_CoalesceWithSumsStats(t *testing.T) {
	prev := NewResourceIndexUpdatedEvent("services", types.ChangeStats{
		Created: 100, Modified: 10, Deleted: 1, IsInitialSync: true,
	})
	next := NewResourceIndexUpdatedEvent("services", types.ChangeStats{
		Created: 5, Modified: 2, Deleted: 3,
	})

	merged, ok := next.CoalesceWith(prev).(*ResourceIndexUpdatedEvent)
	require.True(t, ok)
	assert.Equal(t, "services", merged.ResourceTypeName)
	assert.Equal(t, 105, merged.ChangeStats.Created)
	assert.Equal(t, 12, merged.ChangeStats.Modified)
	assert.Equal(t, 4, merged.ChangeStats.Deleted)
	assert.True(t, merged.ChangeStats.IsInitialSync,
		"a merged event that includes the bulk-load flush still reports it")

	assert.Equal(t, 100, prev.ChangeStats.Created, "merge must not mutate the buffered event")
	assert.Equal(t, 5, next.ChangeStats.Created, "merge must not mutate the newer event")
}

func TestResourceIndexUpdatedEvent_CoalesceWithForeignEventReturnsReceiver(t *testing.T) {
	next := NewResourceIndexUpdatedEvent("services", types.ChangeStats{Created: 5})

	assert.Same(t, next, next.CoalesceWith(NewResourceSyncCompleteEvent("services", 1)),
		"a foreign event type must not be absorbed")
	assert.Same(t, next, next.CoalesceWith(NewResourceIndexUpdatedEvent("secrets", types.ChangeStats{Created: 9})),
		"a different kind must not be absorbed even though the type matches")
}

// prestartCoalesceReason mirrors coalescibleReason: implementing
// PreStartCoalesceKey arms a type for merging in the bus's pre-start/pause
// buffer, and a lossy CoalesceWith is the change that can start losing data.
type prestartCoalesceReason struct {
	// why must state what makes the merge LOSSLESS (delta payloads sum, absolute
	// payloads keep the newest) and which subject dimensions the key carries.
	why string
}

var armedForPreStartMerging = map[string]prestartCoalesceReason{
	"ResourceIndexUpdatedEvent": {
		why: "keyed per watched kind so subjects never collapse; ChangeStats counters " +
			"are additive and CoalesceWith sums them, so the merge is lossless for " +
			"every consumer, including ones that accumulate deltas",
	},
}

func TestPreStartCoalescibleInventory_CoversEveryArmedType(t *testing.T) {
	armed := scanPreStartCoalescibleTypes(t)
	require.NotEmpty(t, armed, "the scan must find armed types; a broken scan would pass vacuously")

	for _, typeName := range armed {
		reason, ok := armedForPreStartMerging[typeName]
		assert.True(t, ok,
			"%s implements PreStartCoalesceKey(), which lets the bus merge buffered "+
				"instances. Add a row to armedForPreStartMerging stating why CoalesceWith "+
				"is lossless and which subject dimensions the key carries — or drop the "+
				"method if either is untrue.", typeName)
		if ok {
			assert.NotEmpty(t, reason.why, "%s needs a reason, not just a row", typeName)
		}
	}
}

func TestPreStartCoalescibleInventory_HasNoStaleRows(t *testing.T) {
	armed := scanPreStartCoalescibleTypes(t)
	for typeName := range armedForPreStartMerging {
		assert.True(t, slices.Contains(armed, typeName),
			"armedForPreStartMerging has a row for %s, which no longer implements "+
				"PreStartCoalesceKey(); remove the stale row", typeName)
	}
}

func scanPreStartCoalescibleTypes(t *testing.T) []string {
	t.Helper()

	root, err := os.Getwd()
	require.NoError(t, err)

	entries, err := os.ReadDir(root)
	require.NoError(t, err)

	fset := token.NewFileSet()
	var armed []string
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}

		file, parseErr := parser.ParseFile(fset, filepath.Join(root, name), nil, 0)
		require.NoError(t, parseErr)

		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Recv == nil || fn.Name.Name != "PreStartCoalesceKey" {
				continue
			}
			if receiver := receiverTypeName(fn); receiver != "" && !slices.Contains(armed, receiver) {
				armed = append(armed, receiver)
			}
		}
	}
	return armed
}
