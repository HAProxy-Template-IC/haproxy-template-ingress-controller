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
	"slices"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/dataplanetest"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/renderartifact"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/renderoutput"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/renderplan"
)

func TestValidationServiceOutputSnapshotRevalidatesIdenticalBytesWhenVerdictChanges(t *testing.T) {
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
	fixture := newValidationOutputFixture(t, 1)
	svc := NewValidationService(&ValidationServiceConfig{SkipDNSValidation: true})
	require.True(t, svc.ValidateOutputSnapshotWithChecksum(
		t.Context(), fixture.output, fixture.checksum,
	).Valid)
	reject.Store(true)
	result := svc.ValidateOutputSnapshotWithChecksum(t.Context(), fixture.output, fixture.checksum)
	require.False(t, result.Valid)
	assert.ErrorContains(t, result.Error, "runtime state changed")
	assert.Equal(t, int64(2), checks.Load(), "the same authenticated output must be revalidated")
}

func TestValidationServiceOutputSnapshotAcceptsExactForeignRoot(t *testing.T) {
	var checks atomic.Int64
	t.Cleanup(dataplanetest.InstallFakeHAProxy(dataplanetest.WithCheck(
		func(string, []string) ([]byte, error) {
			checks.Add(1)
			return nil, nil
		},
	)))
	left := newValidationOutputFixture(t, 8)
	right := newValidationOutputFixture(t, 8)
	require.Equal(t, left.checksum, right.checksum)

	svc := NewValidationService(&ValidationServiceConfig{SkipDNSValidation: true})
	require.True(t, svc.ValidateOutputSnapshotWithChecksum(t.Context(), left.output, left.checksum).Valid)
	require.True(t, svc.ValidateOutputSnapshotWithChecksum(t.Context(), right.output, right.checksum).Valid)
	assert.Equal(t, int64(2), checks.Load())
}

func TestValidationServiceOutputSnapshotChecksumCollisionDoesNotAuthorize(t *testing.T) {
	var checks atomic.Int64
	t.Cleanup(dataplanetest.InstallFakeHAProxy(dataplanetest.WithCheck(
		func(string, []string) ([]byte, error) {
			checks.Add(1)
			return nil, nil
		},
	)))
	fixture := newValidationOutputFixture(t, 1)
	changed := fixture.withArtifactContent(t, 0, "changed.example changed-backend\n")
	const forcedCollision = "forced-collision"

	svc := NewValidationService(&ValidationServiceConfig{SkipDNSValidation: true})
	require.True(t, svc.ValidateOutputSnapshotWithChecksum(t.Context(), fixture.output, forcedCollision).Valid)
	require.True(t, svc.ValidateOutputSnapshotWithChecksum(t.Context(), changed, forcedCollision).Valid)
	assert.Equal(t, int64(2), checks.Load())
}

func TestValidationServiceOutputSnapshotRevalidatesSubstitutedRoots(t *testing.T) {
	fixture := newValidationOutputFixture(t, 1)
	candidates := map[string]struct {
		output *renderoutput.Snapshot
	}{
		"config": {
			output: fixture.withConfig(t, validConfig+"\n# changed config"),
		},
		"plan": {
			output: fixture.withPlanMetadata(t),
		},
		"artifact": {
			output: fixture.withArtifactContent(t, 0, "other.example other-backend\n"),
		},
	}
	for name, candidate := range candidates {
		t.Run(name, func(t *testing.T) {
			var checks atomic.Int64
			t.Cleanup(dataplanetest.InstallFakeHAProxy(dataplanetest.WithCheck(
				func(string, []string) ([]byte, error) {
					checks.Add(1)
					return nil, nil
				},
			)))
			svc := NewValidationService(&ValidationServiceConfig{SkipDNSValidation: true})
			require.True(t, svc.ValidateOutputSnapshotWithChecksum(
				t.Context(), fixture.output, fixture.checksum,
			).Valid)
			require.True(t, svc.ValidateOutputSnapshotWithChecksum(
				t.Context(), candidate.output, fixture.checksum,
			).Valid)
			assert.Equal(t, int64(2), checks.Load())
		})
	}
}

func TestValidationServiceOutputSnapshotRevalidatesAfterFailureAndCancellation(t *testing.T) {
	t.Run("failure", func(t *testing.T) {
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
		fixture := newValidationOutputFixture(t, 1)
		svc := NewValidationService(&ValidationServiceConfig{SkipDNSValidation: true})
		require.False(t, svc.ValidateOutputSnapshotWithChecksum(
			t.Context(), fixture.output, fixture.checksum,
		).Valid)
		reject.Store(false)
		require.True(t, svc.ValidateOutputSnapshotWithChecksum(
			t.Context(), fixture.output, fixture.checksum,
		).Valid)
		assert.Equal(t, int64(2), checks.Load())
	})

	t.Run("cancellation", func(t *testing.T) {
		started := make(chan struct{})
		restore := dataplanetest.InstallFakeHAProxy(dataplanetest.WithCheckContext(
			func(ctx context.Context, _ string, _ []string) ([]byte, error) {
				close(started)
				<-ctx.Done()
				return nil, context.Cause(ctx)
			},
		))
		fixture := newValidationOutputFixture(t, 1)
		svc := NewValidationService(&ValidationServiceConfig{SkipDNSValidation: true})
		cause := errors.New("render retired")
		ctx, cancel := context.WithCancelCause(t.Context())
		done := make(chan *ValidationResult, 1)
		go func() {
			done <- svc.ValidateOutputSnapshotWithChecksum(ctx, fixture.output, fixture.checksum)
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
		require.True(t, svc.ValidateOutputSnapshotWithChecksum(
			t.Context(), fixture.output, fixture.checksum,
		).Valid)
		assert.Equal(t, int64(1), checks.Load())
	})
}

func TestValidationServiceOutputSnapshotOwnsSourcesAndRejectsCopies(t *testing.T) {
	var checks atomic.Int64
	t.Cleanup(dataplanetest.InstallFakeHAProxy(dataplanetest.WithCheck(
		func(workDir string, _ []string) ([]byte, error) {
			checks.Add(1)
			content, err := os.ReadFile(filepath.Join(workDir, "maps", "route-000000.map"))
			if err != nil {
				return nil, err
			}
			if string(content) != "host-000000.example backend-000000\n" {
				return nil, fmt.Errorf("validated map content %q", content)
			}
			return nil, nil
		},
	)))
	fixture := newValidationOutputFixture(t, 1)
	fixture.plan.Sections[0].Text = "poison"
	fixture.plan.Files[0].Content = "poison"
	fixture.specs[0].content = "poison"

	svc := NewValidationService(&ValidationServiceConfig{SkipDNSValidation: true})
	result := svc.ValidateOutputSnapshotWithChecksum(t.Context(), fixture.output, fixture.checksum)
	require.True(t, result.Valid, "validation failed: %v", result.Error)
	config, err := fixture.output.Config()
	require.NoError(t, err)
	assert.Equal(t, validConfig, config)
	artifacts, err := fixture.output.ArtifactSnapshot()
	require.NoError(t, err)
	materialized, err := dataplane.MaterializeAuxiliaryFileSnapshot(artifacts)
	require.NoError(t, err)
	require.Len(t, materialized.MapFiles, 1)
	assert.Equal(t, "host-000000.example backend-000000\n", materialized.MapFiles[0].Content)

	copied := *fixture.output
	result = svc.ValidateOutputSnapshotWithChecksum(t.Context(), &copied, fixture.checksum)
	require.False(t, result.Valid)
	assert.Equal(t, "setup", result.Phase)
	assert.Equal(t, int64(1), checks.Load())
}

func TestValidationServiceOutputSnapshotConcurrentValidation(t *testing.T) {
	fixture := newValidationOutputFixture(t, 8)
	foreign := newValidationOutputFixture(t, 8)
	svc := NewValidationService(&ValidationServiceConfig{SkipDNSValidation: true})
	require.True(t, svc.ValidateOutputSnapshotWithChecksum(
		t.Context(), fixture.output, fixture.checksum,
	).Valid)

	const goroutines = 8
	results := make(chan *ValidationResult, goroutines)
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for index := range goroutines {
		go func() {
			defer wg.Done()
			candidate := fixture.output
			if index%2 == 1 {
				candidate = foreign.output
			}
			results <- svc.ValidateOutputSnapshotWithChecksum(
				context.Background(), candidate, fixture.checksum,
			)
		}()
	}
	wg.Wait()
	close(results)
	for result := range results {
		require.True(t, result.Valid, "validation failed: %v", result.Error)
	}
}

type validationOutputSpec struct {
	descriptor renderartifact.Descriptor
	content    string
}

type validationOutputFixture struct {
	config            string
	plan              *renderplan.Plan
	specs             []validationOutputSpec
	artifactAuthority *renderartifact.Authority
	authority         *renderoutput.Authority
	artifacts         *renderartifact.Snapshot
	output            *renderoutput.Snapshot
	checksum          string
}

func newValidationOutputFixture(tb testing.TB, count int) *validationOutputFixture {
	tb.Helper()
	config := validConfig
	plan := &renderplan.Plan{
		SchemaVersion: renderplan.SchemaVersion,
		Sections: []renderplan.Section{{
			Kind: renderplan.SectionKindCore, Name: "core#0",
			TextDigest: renderplan.DigestString(config), Length: len(config),
			Text: config, TextKnown: true,
		}},
		Maps:  make(map[string]renderplan.Map, count),
		Files: make([]renderplan.File, 0, count+1),
	}
	plan.Files = append(plan.Files, exactValidationOutputFile(
		renderplan.ConfigFilePath, renderplan.FileKindConfig, true, config,
	))
	specs := make([]validationOutputSpec, count)
	for index := range count {
		path := fmt.Sprintf("maps/route-%06d.map", index)
		content := fmt.Sprintf("host-%06d.example backend-%06d\n", index, index)
		specs[index] = validationOutputSpec{
			descriptor: renderartifact.Descriptor{
				Family: renderartifact.Map, Path: path, RuntimePath: path,
			},
			content: content,
		}
		plan.Maps[path] = renderplan.Map{
			Path: path, Ordered: true, Entries: renderplan.ParseMapEntries(content),
		}
		plan.Files = append(plan.Files, exactValidationOutputFile(
			path, renderplan.FileKindMap, false, content,
		))
	}
	plan.ComputeID()
	planAuthority := renderplan.NewAuthority()
	artifactAuthority := renderartifact.NewAuthority()
	authority, err := renderoutput.NewAuthority(planAuthority, artifactAuthority)
	require.NoError(tb, err)
	artifacts := buildValidationOutputArtifacts(tb, artifactAuthority, nil, specs)
	output, err := renderoutput.NewSnapshot(authority, config, plan, artifacts, nil)
	require.NoError(tb, err)
	checksum, err := dataplane.ComputeSnapshotContentChecksum(config, artifacts)
	require.NoError(tb, err)
	return &validationOutputFixture{
		config: config, plan: plan, specs: specs, artifactAuthority: artifactAuthority,
		authority: authority, artifacts: artifacts, output: output, checksum: checksum,
	}
}

func (f *validationOutputFixture) withConfig(tb testing.TB, config string) *renderoutput.Snapshot {
	tb.Helper()
	plan := f.plan.Clone()
	plan.Sections = []renderplan.Section{{
		Kind: renderplan.SectionKindCore, Name: "core#0",
		TextDigest: renderplan.DigestString(config), Length: len(config),
		Text: config, TextKnown: true,
	}}
	plan.Files[0] = exactValidationOutputFile(
		renderplan.ConfigFilePath, renderplan.FileKindConfig, true, config,
	)
	plan.ComputeID()
	output, err := renderoutput.NewSnapshot(f.authority, config, plan, f.artifacts, f.output)
	require.NoError(tb, err)
	return output
}

func (f *validationOutputFixture) withPlanMetadata(tb testing.TB) *renderoutput.Snapshot {
	tb.Helper()
	plan := f.plan.Clone()
	for path, declared := range plan.Maps {
		declared.Ordered = !declared.Ordered
		plan.Maps[path] = declared
		break
	}
	plan.ComputeID()
	output, err := renderoutput.NewSnapshot(f.authority, f.config, plan, f.artifacts, f.output)
	require.NoError(tb, err)
	return output
}

func (f *validationOutputFixture) withArtifactContent(
	tb testing.TB,
	index int,
	content string,
) *renderoutput.Snapshot {
	tb.Helper()
	specs := slices.Clone(f.specs)
	specs[index].content = content
	path := specs[index].descriptor.RuntimePath
	plan := f.plan.Clone()
	for fileIndex := range plan.Files {
		if plan.Files[fileIndex].Path == path {
			plan.Files[fileIndex] = exactValidationOutputFile(
				path, renderplan.FileKindMap, false, content,
			)
			break
		}
	}
	plan.Maps[path] = renderplan.Map{
		Path: path, Ordered: true, Entries: renderplan.ParseMapEntries(content),
	}
	plan.ComputeID()
	artifacts := buildValidationOutputArtifacts(tb, f.artifactAuthority, f.artifacts, specs)
	output, err := renderoutput.NewSnapshot(f.authority, f.config, plan, artifacts, f.output)
	require.NoError(tb, err)
	return output
}

func buildValidationOutputArtifacts(
	tb testing.TB,
	authority *renderartifact.Authority,
	previous *renderartifact.Snapshot,
	specs []validationOutputSpec,
) *renderartifact.Snapshot {
	tb.Helper()
	builder, err := renderartifact.NewBuilder(authority, previous)
	require.NoError(tb, err)
	for _, spec := range specs {
		require.NoError(tb, builder.Add(spec.descriptor, renderartifact.NewLiteralContent(spec.content)))
	}
	snapshot, err := builder.Build()
	require.NoError(tb, err)
	return snapshot
}

func exactValidationOutputFile(path, kind string, reload bool, content string) renderplan.File {
	return renderplan.File{
		Path: path, Kind: kind, ReloadOnChange: reload,
		Digest: renderplan.DigestString(content), Size: int64(len(content)),
		Content: content, ContentKnown: true,
	}
}
