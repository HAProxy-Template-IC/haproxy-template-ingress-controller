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

package incremental_test

import (
	"context"
	"testing"

	"gitlab.com/haproxy-haptic/haptic/pkg/incremental"
)

// An empty batch succeeds and does NOT run the callback. A caller that only
// looks at what the callback produced sees nothing and can mistake it for a
// malformed result — which is exactly how a cold render stage holding a group
// with no instances (a watched kind with no objects in the cluster) turned into
// "incremental cold component vector returned an invalid result set" and left
// the controller unable to render at all.
func TestAnEmptyColdExactBatchSucceedsWithoutRunningTheCallback(t *testing.T) {
	queryKey := incremental.NewQueryKey("query")
	graph, err := incremental.New(incremental.Definition{
		Key: queryKey,
		Run: func(context.Context, incremental.Reader) ([]byte, error) {
			return nil, nil
		},
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	session, err := graph.BeginColdReset(incremental.Input{
		Key:      incremental.NewInputKey("input"),
		Revision: incremental.NewRevision("revision"),
		Found:    true,
		Value:    []byte("value"),
	})
	if err != nil {
		t.Fatalf("BeginColdReset() error = %v", err)
	}

	called := false
	results, err := session.EvaluateAllColdExactBatch(t.Context(), func(
		_ context.Context,
		_ incremental.ColdExactBatch,
	) error {
		called = true
		return nil
	})

	if err != nil {
		t.Fatalf("EvaluateAllColdExactBatch() with no keys error = %v, want nil", err)
	}
	if called {
		t.Fatal("the batch callback ran for an empty key set")
	}
	if len(results) != 0 {
		t.Fatalf("results = %#v, want empty", results)
	}
}
