// Copyright 2026 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package validation

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/auxiliaryfiles"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/dataplanetest"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/renderartifact"
)

func TestValidationServiceSnapshotRevalidatesIdenticalBytesWhenVerdictChanges(t *testing.T) {
	var reject atomic.Bool
	var checks atomic.Int64
	t.Cleanup(dataplanetest.InstallFakeHAProxy(dataplanetest.WithCheck(
		func(string, []string) ([]byte, error) {
			checks.Add(1)
			if reject.Load() {
				return []byte("[ALERT] config : runtime state changed\n"), errors.New("exit status 1")
			}
			return nil, nil
		},
	)))

	snapshot := buildValidationSnapshot(t, renderartifact.NewAuthority(), nil)
	checksum := snapshotChecksum(t, validConfig, snapshot)
	svc := NewValidationService(&ValidationServiceConfig{SkipDNSValidation: true})
	require.True(t, svc.ValidateSnapshotWithChecksum(t.Context(), validConfig, snapshot, checksum).Valid)
	reject.Store(true)
	result := svc.ValidateSnapshotWithChecksum(t.Context(), validConfig, snapshot, checksum)
	require.False(t, result.Valid)
	assert.ErrorContains(t, result.Error, "runtime state changed")
	assert.Equal(t, int64(2), checks.Load(), "the same authenticated root must be revalidated")
}

func TestValidationServiceSnapshotAcceptsExactForeignRoot(t *testing.T) {
	var checks atomic.Int64
	t.Cleanup(dataplanetest.InstallFakeHAProxy(dataplanetest.WithCheck(
		func(string, []string) ([]byte, error) {
			checks.Add(1)
			return nil, nil
		},
	)))
	files := &dataplane.AuxiliaryFiles{
		GeneralFiles: []auxiliaryfiles.GeneralFile{{
			Filename: "error.http", Path: "general/error.http", Content: "safe",
		}},
	}
	left := buildValidationSnapshot(t, renderartifact.NewAuthority(), files)
	right := buildValidationSnapshot(t, renderartifact.NewAuthority(), files)
	checksum := snapshotChecksum(t, validConfig, left)
	require.Equal(t, checksum, snapshotChecksum(t, validConfig, right))

	svc := NewValidationService(&ValidationServiceConfig{SkipDNSValidation: true})
	require.True(t, svc.ValidateSnapshotWithChecksum(t.Context(), validConfig, left, checksum).Valid)
	require.True(t, svc.ValidateSnapshotWithChecksum(t.Context(), validConfig, left, "stale-metadata").Valid)
	require.True(t, svc.ValidateSnapshotWithChecksum(t.Context(), validConfig, right, checksum).Valid)
	assert.Equal(t, int64(3), checks.Load())
}

func TestValidationServiceSnapshotRejectsNilAndInvalidRoots(t *testing.T) {
	var checks atomic.Int64
	t.Cleanup(dataplanetest.InstallFakeHAProxy(dataplanetest.WithCheck(
		func(string, []string) ([]byte, error) {
			checks.Add(1)
			return nil, nil
		},
	)))
	svc := NewValidationService(&ValidationServiceConfig{SkipDNSValidation: true})

	for name, snapshot := range map[string]*renderartifact.Snapshot{
		"nil":  nil,
		"zero": {},
	} {
		t.Run(name, func(t *testing.T) {
			result := svc.ValidateSnapshotWithChecksum(t.Context(), validConfig, snapshot, "checksum")
			require.False(t, result.Valid)
			assert.Equal(t, "setup", result.Phase)
			assert.ErrorContains(t, result.Error, "snapshot")
		})
	}
	assert.Zero(t, checks.Load())
}

func TestValidationServiceSnapshotRevalidatesAfterFailureAndCancellation(t *testing.T) {
	var reject atomic.Bool
	var checks atomic.Int64
	reject.Store(true)
	t.Cleanup(dataplanetest.InstallFakeHAProxy(dataplanetest.WithCheck(
		func(string, []string) ([]byte, error) {
			checks.Add(1)
			if reject.Load() {
				return []byte("[ALERT] config : refused\n"), errors.New("exit status 1")
			}
			return nil, nil
		},
	)))
	snapshot := buildValidationSnapshot(t, renderartifact.NewAuthority(), nil)
	checksum := snapshotChecksum(t, validConfig, snapshot)
	svc := NewValidationService(&ValidationServiceConfig{SkipDNSValidation: true})

	result := svc.ValidateSnapshotWithChecksum(t.Context(), validConfig, snapshot, checksum)
	require.False(t, result.Valid)
	reject.Store(false)
	require.True(t, svc.ValidateSnapshotWithChecksum(t.Context(), validConfig, snapshot, checksum).Valid)
	assert.Equal(t, int64(2), checks.Load())

	second := buildValidationSnapshot(t, renderartifact.NewAuthority(), nil)
	cause := errors.New("render retired")
	ctx, cancel := context.WithCancelCause(t.Context())
	cancel(cause)
	result = svc.ValidateSnapshotWithChecksum(ctx, validConfig+"\n", second, snapshotChecksum(t, validConfig+"\n", second))
	require.False(t, result.Valid)
	require.ErrorIs(t, result.Error, cause)
	assert.Equal(t, int64(2), checks.Load())

	require.True(t, svc.ValidateSnapshotWithChecksum(
		t.Context(), validConfig+"\n", second, snapshotChecksum(t, validConfig+"\n", second),
	).Valid)
	assert.Equal(t, int64(3), checks.Load())
}

func TestValidationServiceSnapshotCancellationDuringCheckAllowsFreshValidation(t *testing.T) {
	started := make(chan struct{})
	restore := dataplanetest.InstallFakeHAProxy(dataplanetest.WithCheckContext(
		func(ctx context.Context, _ string, _ []string) ([]byte, error) {
			close(started)
			<-ctx.Done()
			return nil, context.Cause(ctx)
		},
	))
	snapshot := buildValidationSnapshot(t, renderartifact.NewAuthority(), nil)
	checksum := snapshotChecksum(t, validConfig, snapshot)
	svc := NewValidationService(&ValidationServiceConfig{SkipDNSValidation: true})
	cause := errors.New("render replaced")
	ctx, cancel := context.WithCancelCause(t.Context())
	done := make(chan *ValidationResult, 1)
	go func() {
		done <- svc.ValidateSnapshotWithChecksum(ctx, validConfig, snapshot, checksum)
	}()
	<-started
	cancel(cause)
	result := <-done
	restore()
	require.False(t, result.Valid)
	require.ErrorIs(t, result.Error, cause)

	var checks atomic.Int64
	t.Cleanup(dataplanetest.InstallFakeHAProxy(dataplanetest.WithCheck(
		func(string, []string) ([]byte, error) {
			checks.Add(1)
			return nil, nil
		},
	)))
	require.True(t, svc.ValidateSnapshotWithChecksum(t.Context(), validConfig, snapshot, checksum).Valid)
	assert.Equal(t, int64(1), checks.Load())
}

func TestValidationServiceSnapshotOwnsSourceBytes(t *testing.T) {
	files := &dataplane.AuxiliaryFiles{
		MapFiles: []auxiliaryfiles.MapFile{{Path: "routes.map", Content: "safe"}},
	}
	snapshot := buildValidationSnapshot(t, renderartifact.NewAuthority(), files)
	files.MapFiles[0].Content = "poison"
	t.Cleanup(dataplanetest.InstallFakeHAProxy(dataplanetest.WithCheck(
		func(workDir string, _ []string) ([]byte, error) {
			content, err := os.ReadFile(filepath.Join(workDir, "maps", "routes.map"))
			if err != nil {
				return nil, err
			}
			if string(content) != "safe" {
				return nil, fmt.Errorf("validated map content %q", content)
			}
			return nil, nil
		},
	)))
	svc := NewValidationService(&ValidationServiceConfig{SkipDNSValidation: true})
	result := svc.ValidateSnapshotWithChecksum(t.Context(), validConfig, snapshot, snapshotChecksum(t, validConfig, snapshot))
	require.True(t, result.Valid, "validation failed: %v", result.Error)
}

func TestValidationServiceSnapshotConcurrentValidation(t *testing.T) {
	files := makeValidationMapFiles(8)
	authority := renderartifact.NewAuthority()
	snapshot := buildValidationSnapshot(t, authority, files)
	foreign := buildValidationSnapshot(t, renderartifact.NewAuthority(), files)
	checksum := snapshotChecksum(t, validConfig, snapshot)
	svc := NewValidationService(&ValidationServiceConfig{SkipDNSValidation: true})
	require.True(t, svc.ValidateSnapshotWithChecksum(t.Context(), validConfig, snapshot, checksum).Valid)

	const goroutines = 8
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for index := range goroutines {
		go func() {
			defer wg.Done()
			candidate := snapshot
			if index%2 == 1 {
				candidate = foreign
			}
			result := svc.ValidateSnapshotWithChecksum(context.Background(), validConfig, candidate, checksum)
			assert.True(t, result.Valid, "validation failed: %v", result.Error)
		}()
	}
	wg.Wait()
}

func buildValidationSnapshot(
	tb testing.TB,
	authority *renderartifact.Authority,
	files *dataplane.AuxiliaryFiles,
) *renderartifact.Snapshot {
	tb.Helper()
	snapshot, err := dataplane.BuildAuxiliaryFileSnapshot(authority, nil, files)
	require.NoError(tb, err)
	return snapshot
}

func snapshotChecksum(tb testing.TB, config string, snapshot *renderartifact.Snapshot) string {
	tb.Helper()
	checksum, err := dataplane.ComputeSnapshotContentChecksum(config, snapshot)
	require.NoError(tb, err)
	return checksum
}

func makeValidationMapFiles(count int) *dataplane.AuxiliaryFiles {
	files := &dataplane.AuxiliaryFiles{MapFiles: make([]auxiliaryfiles.MapFile, count)}
	for index := range files.MapFiles {
		files.MapFiles[index] = auxiliaryfiles.MapFile{
			Path:    fmt.Sprintf("route-%04d.map", index),
			Content: fmt.Sprintf("host-%04d.example.test backend-%04d\n", index, index),
		}
	}
	return files
}
