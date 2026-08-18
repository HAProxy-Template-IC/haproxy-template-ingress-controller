//go:build integration

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

package integration

import (
	"context"
	"testing"

	"github.com/rekby/fixenv"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/agent/api"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/deployplan"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/renderplan"
)

// TestMain sets up package-scoped fixtures and runs tests
func TestMain(m *testing.M) {
	fixenv.RunTests(m)
}

// syncTestCase is one transition a real HAProxy has to make: apply an initial
// file set, then apply a second one and assert what the pod ends up with.
//
// It states what the controller declares (the two file sets) and what an
// operator can observe (the pod's tree, HAProxy's runtime state), never how
// the change was computed.
type syncTestCase struct {
	name              string
	initialConfigFile string // Config the pod starts from
	desiredConfigFile string // Target config to reach

	// expectedVerdict, when set, asserts what deployplan.Diff decided for this
	// transition. Set it only where the case declares enough structure to
	// pin the verdict — a change confined to auxiliary files is reload-free,
	// while any configuration change reloads.
	expectedVerdict deployplan.Verdict

	// Auxiliary files for the INITIAL file set, keyed by manifest path
	// (relative to the pod's base directory, which is also the name HAProxy
	// knows the file by at runtime).
	// Example: map[string]string{"general/400.http": "error-files/400.http"}
	initialGeneralFiles    map[string]string
	initialSSLCertificates map[string]string
	initialMapFiles        map[string]string

	// Auxiliary files for the DESIRED file set. The manifest is the complete
	// desired state, so a file the initial set had and this one omits is
	// deleted from the pod.
	generalFiles    map[string]string
	sslCertificates map[string]string
	mapFiles        map[string]string

	// Optional: verify auxiliary file content on the pod after the apply.
	// Map: manifest path → testdata file to compare against.
	verifyMapFiles        map[string]string
	verifyGeneralFiles    map[string]string
	verifySSLCertificates map[string]string

	// verifyRuntimeMap additionally asserts that the live (in-memory) runtime
	// map matches verifyMapFiles — proving a runtime-applied map change
	// reached the worker's memory, not just the on-disk file.
	verifyRuntimeMap bool

	// Skip reason for unsupported features (test-first approach)
	// If set, test will be skipped with this message
	skipReason string

	// minHAProxy skips the case below an HAProxy release, for a directive the
	// older bracket cannot parse.
	minHAProxy string
}

// runSyncTest applies the case's two file sets to a real HAProxy pod through
// its agent and verifies the pod converged.
func runSyncTest(t *testing.T, tc syncTestCase) {
	if tc.skipReason != "" {
		t.Skip(tc.skipReason)
	}
	if tc.minHAProxy != "" {
		skipBelowHAProxy(t, tc.minHAProxy)
	}

	env := fixenv.New(t)
	ctx := context.Background()
	session := NewSession(t, env)

	// Step 1: the pod's starting point. The agent has no baseline of ours yet,
	// so this is full state plus a reload — a fresh pod's first apply.
	session.SetConfig(LoadTestConfig(t, tc.initialConfigFile))
	declareFiles(t, session, tc.initialGeneralFiles, tc.initialSSLCertificates, tc.initialMapFiles)
	initial := session.MustApply(ctx)
	require.Equal(t, deployplan.VerdictReload, initial.Verdict,
		"a pod with no baseline gets full state and a reload")

	// Step 2: the transition under test.
	session.SetConfig(LoadTestConfig(t, tc.desiredConfigFile))
	session.RemoveDir(GeneralDir)
	session.RemoveDir(SSLDir)
	session.RemoveDir(MapsDir)
	declareFiles(t, session, tc.generalFiles, tc.sslCertificates, tc.mapFiles)

	before := workerPID(t, ctx, session)
	decision := session.MustApply(ctx)
	t.Logf("transition verdict=%s ops=%d reasons=%v", decision.Verdict, len(decision.Ops), decision.Reasons)

	if tc.expectedVerdict != "" {
		assert.Equal(t, tc.expectedVerdict, decision.Verdict, "unexpected verdict for this transition")
	}
	if decision.Verdict == deployplan.VerdictRuntime {
		assert.Equal(t, before, workerPID(t, ctx, session),
			"a runtime apply must reach the running worker, not replace it")
	}

	// Step 3: the pod's tree is the desired set, byte for byte, and nothing else.
	assertTree(t, ctx, session)

	// Step 4: the content assertions the case asked for.
	verifyFiles(t, ctx, session, tc.verifyGeneralFiles, tc.verifySSLCertificates, tc.verifyMapFiles)
	if tc.verifyRuntimeMap {
		verifyRuntimeMaps(t, ctx, session, tc.verifyMapFiles)
	}

	// Step 5: applying the same desired state again changes nothing. This is
	// the idempotency the reconcile loop depends on: a re-render that produced
	// the same bytes must not reload HAProxy.
	settled := workerPID(t, ctx, session)
	repeat, result := session.ApplyDesired(ctx)
	require.True(t, result.OK, "the repeated apply was rejected: %s", applyError(result))
	assert.Equal(t, deployplan.VerdictFileOnly, repeat.Verdict, "an unchanged render must not reload")
	assert.Equal(t, api.ResultNoop, result.Mode, "an unchanged render must write nothing")
	assert.Equal(t, settled, workerPID(t, ctx, session), "an unchanged render must not replace the worker")
}

// declareFiles adds one case's auxiliary files to the desired set. The keys are
// manifest paths, the values testdata files.
func declareFiles(t *testing.T, session *Session, general, certificates, maps map[string]string) {
	t.Helper()
	for _, group := range []map[string]string{general, certificates, maps} {
		for path, testdataFile := range group {
			session.Set(path, LoadTestFileContent(t, testdataFile))
		}
	}
}

// assertTree compares the pod's tree with the desired set: every declared file
// is present with its exact content, and every file this session owns and no
// longer declares is gone. Files the agent never put there — its own dot
// directories, and anything HAProxy writes itself — are not its to delete.
func assertTree(t *testing.T, ctx context.Context, session *Session) {
	t.Helper()
	for _, path := range session.Paths() {
		actual, err := session.haproxy.ReadFile(ctx, path)
		require.NoError(t, err, "reading %s from the pod", path)
		assert.Equal(t, session.Content(path), actual, "%s on the pod differs from the desired content", path)
	}
	for _, path := range session.Dropped() {
		assert.False(t, session.haproxy.FileExists(ctx, path),
			"%s is still on the pod though the manifest dropped it — absence means delete", path)
	}
}

// verifyFiles checks the content the case pinned, read from the pod's tree.
func verifyFiles(t *testing.T, ctx context.Context, session *Session, groups ...map[string]string) {
	t.Helper()
	for _, group := range groups {
		for path, testdataFile := range group {
			actual, err := session.haproxy.ReadFile(ctx, path)
			require.NoError(t, err, "reading %s from the pod", path)
			assert.Equal(t, LoadTestFileContent(t, testdataFile), actual, "%s content mismatch", path)
		}
	}
}

// verifyRuntimeMaps asserts the worker's in-memory map matches the file. The
// on-disk file alone does not prove a runtime-applied change reached the
// running process.
func verifyRuntimeMaps(t *testing.T, ctx context.Context, session *Session, expected map[string]string) {
	t.Helper()
	for path, testdataFile := range expected {
		entries, err := session.haproxy.RuntimeMapEntries(ctx, path)
		require.NoError(t, err, "reading the runtime map %s", path)
		assert.Equal(t, mapEntriesOf(LoadTestFileContent(t, testdataFile)), entries,
			"the runtime (in-memory) map %s differs from the file", path)
	}
}

// mapEntriesOf projects map-file content onto the key → value shape a runtime
// map has, where a repeated key keeps its last value.
func mapEntriesOf(content string) map[string]string {
	entries := map[string]string{}
	for _, entry := range renderplan.ParseMapEntries(content) {
		entries[entry.Key] = entry.Value
	}
	return entries
}

func workerPID(t *testing.T, ctx context.Context, session *Session) int {
	t.Helper()
	pid, err := session.haproxy.WorkerPID(ctx)
	require.NoError(t, err, "reading the worker PID")
	return pid
}
