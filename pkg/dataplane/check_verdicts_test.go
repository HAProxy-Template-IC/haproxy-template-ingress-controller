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

package dataplane

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/haproxy-haptic/HAPTIC/pkg/dataplane/auxiliaryfiles"
)

// fileJudgingExecutor refuses a check when the config or any file beside it
// on disk contains "invalid", so a reused verdict that is stale shows up as a
// wrong answer, not only as a missing process.
type fileJudgingExecutor struct {
	checks atomic.Int32
	err    error
}

func (*fileJudgingExecutor) Version(context.Context) (string, error) {
	return fakeHAProxyVersionLine, nil
}

func (e *fileJudgingExecutor) Check(_ context.Context, workDir string, _ ...string) ([]byte, error) {
	e.checks.Add(1)
	if e.err != nil {
		return nil, e.err
	}
	refused := false
	walkErr := filepath.WalkDir(workDir, func(path string, entry os.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		content, err := os.ReadFile(path)
		if err == nil && strings.Contains(string(content), "invalid") {
			refused = true
		}
		return err
	})
	if walkErr != nil {
		return nil, walkErr
	}
	if refused {
		return []byte("[ALERT]    (1) : config : invalid content\n"), errors.New("exit status 1")
	}
	return nil, nil
}

func installJudgingExecutor(t *testing.T) *fileJudgingExecutor {
	t.Helper()
	executor := &fileJudgingExecutor{}
	t.Cleanup(SetHAProxyExecutor(executor))
	return executor
}

type checkInput struct {
	config   string
	mapPath  string
	mapValue string
	strict   bool
}

func baseCheckInput() checkInput {
	return checkInput{config: "global\n    daemon\n", mapPath: "maps/hosts.map", mapValue: "example.com be_a\n"}
}

// freshValidationPaths mirrors ValidationService: a new tree per check, and a
// config that names it in default-path.
func freshValidationPaths(t *testing.T) *ValidationPaths {
	t.Helper()
	root := filepath.Join(t.TempDir(), "haproxy-validation")
	require.NoError(t, os.Mkdir(root, 0o750))
	return &ValidationPaths{
		TempDir:           root,
		MapsDir:           filepath.Join(root, "maps"),
		SSLCertsDir:       filepath.Join(root, "ssl"),
		CRTListDir:        filepath.Join(root, "general"),
		GeneralStorageDir: filepath.Join(root, "general"),
		ConfigFile:        filepath.Join(root, "haproxy.cfg"),
	}
}

func runCheck(t *testing.T, verdicts *CheckVerdicts, input checkInput) (bool, error) {
	t.Helper()
	return runCheckAt(t.Context(), freshValidationPaths(t), verdicts, input)
}

func runCheckAt(ctx context.Context, paths *ValidationPaths, verdicts *CheckVerdicts, input checkInput) (bool, error) {
	config := input.config + "defaults\n    default-path origin " + paths.TempDir + "\n"
	aux := &AuxiliaryFiles{MapFiles: []auxiliaryfiles.MapFile{{Path: input.mapPath, Content: input.mapValue}}}
	return ValidateSemanticsWithVerdicts(ctx, config, aux, paths, !input.strict, nil, verdicts)
}

func TestCheckVerdictsAnswerIdenticalInputWithoutRunningHAProxy(t *testing.T) {
	executor := installJudgingExecutor(t)
	verdicts := NewCheckVerdicts(4)

	reused, err := runCheck(t, verdicts, baseCheckInput())
	require.NoError(t, err)
	assert.False(t, reused)

	reused, err = runCheck(t, verdicts, baseCheckInput())
	require.NoError(t, err)
	assert.True(t, reused, "the validation tree's own name is not an input")
	assert.Equal(t, int32(1), executor.checks.Load())
}

func TestCheckVerdictsNeverAnswerADifferentInput(t *testing.T) {
	tests := []struct {
		name        string
		change      func(*checkInput)
		wantRefused bool
	}{
		{name: "config byte", change: func(in *checkInput) { in.config += "# invalid\n" }, wantRefused: true},
		{name: "aux content", change: func(in *checkInput) { in.mapValue = "invalid.example.com be_a\n" }, wantRefused: true},
		{name: "aux path", change: func(in *checkInput) { in.mapPath = "maps/other.map" }},
		{name: "strict DNS", change: func(in *checkInput) { in.strict = true }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			executor := installJudgingExecutor(t)
			verdicts := NewCheckVerdicts(4)
			_, err := runCheck(t, verdicts, baseCheckInput())
			require.NoError(t, err)

			changed := baseCheckInput()
			tt.change(&changed)
			reused, err := runCheck(t, verdicts, changed)
			assert.False(t, reused)
			assert.Equal(t, int32(2), executor.checks.Load())
			if tt.wantRefused {
				require.ErrorIs(t, err, ErrHAProxyRefused)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestCheckVerdictsNeverReuseStrictDNSChecks(t *testing.T) {
	executor := installJudgingExecutor(t)
	verdicts := NewCheckVerdicts(4)
	strict := baseCheckInput()
	strict.strict = true
	for range 2 {
		reused, err := runCheck(t, verdicts, strict)
		require.NoError(t, err)
		assert.False(t, reused)
	}
	assert.Equal(t, int32(2), executor.checks.Load())
}

func TestCheckVerdictsAreScopedToTheEnvironmentAndExecutor(t *testing.T) {
	executor := installJudgingExecutor(t)
	verdicts := NewCheckVerdicts(4)
	_, err := runCheck(t, verdicts, baseCheckInput())
	require.NoError(t, err)

	t.Setenv("HAPTIC_CHECK_VERDICT_TEST", "changed")
	reused, err := runCheck(t, verdicts, baseCheckInput())
	require.NoError(t, err)
	assert.False(t, reused, "haproxy expands environment variables")
	assert.Equal(t, int32(2), executor.checks.Load())

	replacement := installJudgingExecutor(t)
	reused, err = runCheck(t, verdicts, baseCheckInput())
	require.NoError(t, err)
	assert.False(t, reused)
	assert.Equal(t, int32(1), replacement.checks.Load())
}

func TestCheckVerdictsRememberRefusals(t *testing.T) {
	executor := installJudgingExecutor(t)
	verdicts := NewCheckVerdicts(4)
	invalid := baseCheckInput()
	invalid.config += "# invalid\n"
	for _, wantReused := range []bool{false, true} {
		reused, err := runCheck(t, verdicts, invalid)
		require.ErrorIs(t, err, ErrHAProxyRefused)
		assert.Equal(t, wantReused, reused)
	}
	assert.Equal(t, int32(1), executor.checks.Load())
}

func TestCheckVerdictsRetryChecksThatCouldNotRun(t *testing.T) {
	executor := installJudgingExecutor(t)
	executor.err = errors.New("signal: killed")
	verdicts := NewCheckVerdicts(4)
	for range 2 {
		reused, err := runCheck(t, verdicts, baseCheckInput())
		require.Error(t, err)
		require.NotErrorIs(t, err, ErrHAProxyRefused)
		assert.False(t, reused)
	}
	assert.Equal(t, int32(2), executor.checks.Load())

	executor.err = nil
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err := runCheckAt(ctx, freshValidationPaths(t), verdicts, baseCheckInput())
	require.ErrorIs(t, err, context.Canceled)
	reused, err := runCheck(t, verdicts, baseCheckInput())
	require.NoError(t, err)
	assert.False(t, reused, "a cancelled check is not a verdict")
}

func TestCheckVerdictsRequireAFreshValidationTree(t *testing.T) {
	executor := installJudgingExecutor(t)
	verdicts := NewCheckVerdicts(4)
	_, err := runCheck(t, verdicts, baseCheckInput())
	require.NoError(t, err)

	used := freshValidationPaths(t)
	require.NoError(t, os.WriteFile(filepath.Join(used.TempDir, "leftover.pem"), []byte("invalid"), 0o600))
	reused, err := runCheckAt(t.Context(), used, verdicts, baseCheckInput())
	assert.False(t, reused)
	require.ErrorIs(t, err, ErrHAProxyRefused, "a file left in the tree is read by haproxy")
	assert.Equal(t, int32(2), executor.checks.Load())
}

func TestCheckVerdictsEvictTheLeastRecentlyUsed(t *testing.T) {
	executor := installJudgingExecutor(t)
	verdicts := NewCheckVerdicts(1)
	other := baseCheckInput()
	other.mapValue = "other.example.com be_b\n"
	for _, input := range []checkInput{baseCheckInput(), other, baseCheckInput()} {
		reused, err := runCheck(t, verdicts, input)
		require.NoError(t, err)
		assert.False(t, reused)
	}
	assert.Equal(t, int32(3), executor.checks.Load())
}
