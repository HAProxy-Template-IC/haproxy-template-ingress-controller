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

package renderoutput

import (
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/renderartifact"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/renderplan"
)

type outputArtifactSpec struct {
	descriptor renderartifact.Descriptor
	content    string
}

type outputFixture struct {
	config            string
	plan              *renderplan.Plan
	specs             []outputArtifactSpec
	planAuthority     *renderplan.Authority
	artifactAuthority *renderartifact.Authority
	authority         *Authority
	artifacts         *renderartifact.Snapshot
}

func TestAuthorityRejectsCopiesAndInvalidLineages(t *testing.T) {
	plans := renderplan.NewAuthority()
	artifacts := renderartifact.NewAuthority()
	authority, err := NewAuthority(plans, artifacts)
	require.NoError(t, err)
	require.NoError(t, authority.ValidateAuthentication())

	copyAuthority := *authority
	require.ErrorIs(t, copyAuthority.ValidateAuthentication(), errInvalidAuthority)
	var zero Authority
	require.ErrorIs(t, zero.ValidateAuthentication(), errInvalidAuthority)
	_, err = NewAuthority(nil, artifacts)
	require.ErrorIs(t, err, errInvalidAuthority)
	_, err = NewAuthority(plans, nil)
	require.ErrorIs(t, err, errInvalidAuthority)
	require.ErrorIs(t, (*Authority)(nil).ValidateSnapshot(nil), errInvalidAuthority)
}

func TestSnapshotAcceptsEmptyArtifactSet(t *testing.T) {
	config := "global\n"
	plan := &renderplan.Plan{
		SchemaVersion: renderplan.SchemaVersion,
		Sections:      []renderplan.Section{exactSection(renderplan.SectionKindCore, "core#0", config)},
		Files:         []renderplan.File{exactPlanFile(renderplan.ConfigFilePath, renderplan.FileKindConfig, true, config)},
	}
	plan.ComputeID()
	plans := renderplan.NewAuthority()
	artifacts := renderartifact.NewAuthority()
	authority, err := NewAuthority(plans, artifacts)
	require.NoError(t, err)
	artifactSnapshot := buildArtifactSnapshot(t, artifacts, nil, nil)
	snapshot := mustOutputSnapshot(t, authority, config, plan, artifactSnapshot, nil)
	counts, err := snapshot.Counts()
	require.NoError(t, err)
	assert.Equal(t, Counts{Sections: 1, Files: 1}, counts)
}

func TestSnapshotBindsEveryOutputAndReusesExactPrevious(t *testing.T) {
	fixture := newOutputFixture(t)
	originalPlan := fixture.plan.Clone()
	snapshot := mustOutputSnapshot(t, fixture.authority, fixture.config, fixture.plan, fixture.artifacts, nil)
	require.NoError(t, snapshot.ValidateAuthentication())

	config, err := snapshot.Config()
	require.NoError(t, err)
	assert.Equal(t, fixture.config, config)
	planSnapshot, err := snapshot.PlanSnapshot()
	require.NoError(t, err)
	require.NoError(t, fixture.planAuthority.ValidateSnapshot(planSnapshot))
	artifactSnapshot, err := snapshot.ArtifactSnapshot()
	require.NoError(t, err)
	assert.Same(t, fixture.artifacts, artifactSnapshot)
	planID, err := snapshot.PlanID()
	require.NoError(t, err)
	assert.Equal(t, fixture.plan.ID, planID)
	checksum, err := snapshot.ContentChecksum()
	require.NoError(t, err)
	wantChecksum, err := dataplane.ComputeSnapshotContentChecksum(fixture.config, fixture.artifacts)
	require.NoError(t, err)
	assert.Equal(t, wantChecksum, checksum)
	counts, err := snapshot.Counts()
	require.NoError(t, err)
	assert.Equal(t, Counts{
		Sections: 3, Backends: 1, Profiles: 1, Maps: 1,
		Files: 7, Artifacts: 6,
	}, counts)

	freshArtifacts := buildArtifactSnapshot(t, fixture.artifactAuthority, nil, fixture.specs)
	sameRoot, err := freshArtifacts.SameRoot(fixture.artifacts)
	require.NoError(t, err)
	assert.False(t, sameRoot)
	reused := mustOutputSnapshot(
		t, fixture.authority, fixture.config, fixture.plan.Clone(), freshArtifacts, snapshot,
	)
	assert.Same(t, snapshot, reused)

	fixture.plan.Sections[0].Text = "poison"
	fixture.plan.Files[0].Content = "poison"
	fixture.plan.Maps[fixture.specs[0].descriptor.RuntimePath] = renderplan.Map{Path: "poison"}
	detached, err := planSnapshot.LegacyCopy()
	require.NoError(t, err)
	assert.Equal(t, originalPlan, detached)
	detached.Sections[0].Text = "caller poison"
	detached.Files[0].Content = "caller poison"
	again, err := planSnapshot.LegacyCopy()
	require.NoError(t, err)
	assert.Equal(t, originalPlan, again)

	require.NoError(t, artifactSnapshot.Walk(func(artifact *renderartifact.Artifact) error {
		descriptor, descriptorErr := artifact.Descriptor()
		if descriptorErr != nil {
			return descriptorErr
		}
		originalPath := descriptor.RuntimePath
		descriptor.RuntimePath = "caller poison"
		againDescriptor, descriptorErr := artifact.Descriptor()
		if descriptorErr != nil {
			return descriptorErr
		}
		assert.Equal(t, originalPath, againDescriptor.RuntimePath)
		return nil
	}))
	counts.Files++
	againCounts, err := snapshot.Counts()
	require.NoError(t, err)
	assert.Equal(t, 7, againCounts.Files)
}

func TestSnapshotReusesConfigStorageAcrossChangedPlan(t *testing.T) {
	fixture := newOutputFixture(t)
	previous := mustOutputSnapshot(t, fixture.authority, fixture.config, fixture.plan, fixture.artifacts, nil)
	changed := fixture.plan.Clone()
	mapPath := fixture.specs[0].descriptor.RuntimePath
	declared := changed.Maps[mapPath]
	declared.Ordered = !declared.Ordered
	changed.Maps[mapPath] = declared
	changed.ComputeID()

	next := mustOutputSnapshot(t, fixture.authority, fixture.config, changed, fixture.artifacts, previous)
	assert.NotSame(t, previous, next)
	assert.Same(t, previous.root.config, next.root.config)
}

func TestReusePreviousRequiresBothExactChildRoots(t *testing.T) {
	fixture := newOutputFixture(t)
	previous := mustOutputSnapshot(t, fixture.authority, fixture.config, fixture.plan, fixture.artifacts, nil)
	planSnapshot, err := previous.PlanSnapshot()
	require.NoError(t, err)

	reused, hit, err := ReusePrevious(fixture.authority, previous, planSnapshot, fixture.artifacts)
	require.NoError(t, err)
	assert.True(t, hit)
	assert.Same(t, previous, reused)

	changedPlan := fixture.plan.Clone()
	mapPath := fixture.specs[0].descriptor.RuntimePath
	declared := changedPlan.Maps[mapPath]
	declared.Ordered = !declared.Ordered
	changedPlan.Maps[mapPath] = declared
	changedPlan.ComputeID()
	changedPlanSnapshot, err := renderplan.NewSnapshot(fixture.planAuthority, changedPlan, planSnapshot)
	require.NoError(t, err)
	reused, hit, err = ReusePrevious(fixture.authority, previous, changedPlanSnapshot, fixture.artifacts)
	require.NoError(t, err)
	assert.False(t, hit)
	assert.Nil(t, reused)

	freshArtifacts := buildArtifactSnapshot(t, fixture.artifactAuthority, nil, fixture.specs)
	reused, hit, err = ReusePrevious(fixture.authority, previous, planSnapshot, freshArtifacts)
	require.NoError(t, err)
	assert.False(t, hit, "exact bytes are insufficient for the O(1) root path")
	assert.Nil(t, reused)
	_, _, err = ReusePrevious(fixture.authority, nil, planSnapshot, fixture.artifacts)
	require.Error(t, err)

	shallow := *previous
	_, _, err = ReusePrevious(fixture.authority, &shallow, planSnapshot, fixture.artifacts)
	require.ErrorIs(t, err, errInvalidSnapshot)
	planCopy := *planSnapshot
	_, _, err = ReusePrevious(fixture.authority, previous, &planCopy, fixture.artifacts)
	require.Error(t, err)
	artifactCopy := *fixture.artifacts
	_, _, err = ReusePrevious(fixture.authority, previous, planSnapshot, &artifactCopy)
	require.Error(t, err)
	foreign := newOutputFixture(t)
	_, _, err = ReusePrevious(fixture.authority, previous, planSnapshot, foreign.artifacts)
	require.Error(t, err)
}

func TestSnapshotAccessorsAreNilSafe(t *testing.T) {
	var snapshot *Snapshot
	_, err := snapshot.Config()
	require.ErrorIs(t, err, errInvalidSnapshot)
	_, err = snapshot.ConfigDocument()
	require.ErrorIs(t, err, errInvalidSnapshot)
	_, err = snapshot.PlanSnapshot()
	require.ErrorIs(t, err, errInvalidSnapshot)
	_, err = snapshot.ArtifactSnapshot()
	require.ErrorIs(t, err, errInvalidSnapshot)
	_, err = snapshot.PlanID()
	require.ErrorIs(t, err, errInvalidSnapshot)
	_, err = snapshot.ContentChecksum()
	require.ErrorIs(t, err, errInvalidSnapshot)
	_, err = snapshot.Counts()
	require.ErrorIs(t, err, errInvalidSnapshot)
	_, err = snapshot.SameRoot(nil)
	require.ErrorIs(t, err, errInvalidSnapshot)
	_, err = snapshot.ExactEqual(nil)
	require.ErrorIs(t, err, errInvalidSnapshot)
}

func TestNewSnapshotRejectsInexactPlan(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*renderplan.Plan)
	}{
		{name: "schema", mutate: func(plan *renderplan.Plan) { plan.SchemaVersion++; plan.ComputeID() }},
		{name: "ID", mutate: func(plan *renderplan.Plan) { plan.ID = "forged" }},
		{name: "section availability", mutate: func(plan *renderplan.Plan) { plan.Sections[0].TextKnown = false; plan.ComputeID() }},
		{name: "section length", mutate: func(plan *renderplan.Plan) { plan.Sections[0].Length++; plan.ComputeID() }},
		{name: "section bytes", mutate: func(plan *renderplan.Plan) { plan.Sections[0].Text = "GLOBAL\n" }},
		{name: "section kind", mutate: func(plan *renderplan.Plan) { plan.Sections[0].Kind = "foreign"; plan.ComputeID() }},
		{name: "backend body", mutate: func(plan *renderplan.Plan) {
			backend := plan.Backends["be_app"]
			backend.Body[0] = "server forged 192.0.2.1:80"
			plan.Backends["be_app"] = backend
		}},
		{name: "backend comments", mutate: func(plan *renderplan.Plan) {
			backend := plan.Backends["be_app"]
			backend.Comments[0] = "forged"
			plan.Backends["be_app"] = backend
		}},
		{name: "backend record digest", mutate: func(plan *renderplan.Plan) {
			backend := plan.Backends["be_app"]
			backend.RecordDigest = "forged"
			plan.Backends["be_app"] = backend
			plan.ComputeID()
		}},
		{name: "backend section", mutate: func(plan *renderplan.Plan) {
			backend := plan.Backends["be_app"]
			backend.TextDigest = plan.Sections[0].TextDigest
			plan.Backends["be_app"] = backend
			plan.ComputeID()
		}},
		{name: "profile body", mutate: func(plan *renderplan.Plan) {
			profile := plan.Profiles["profile-a"]
			profile.BodyDigest = "forged"
			plan.Profiles["profile-a"] = profile
			plan.ComputeID()
		}},
		{name: "file availability", mutate: func(plan *renderplan.Plan) { plan.Files[0].ContentKnown = false; plan.ComputeID() }},
		{name: "file size", mutate: func(plan *renderplan.Plan) { plan.Files[0].Size++; plan.ComputeID() }},
		{name: "file bytes", mutate: func(plan *renderplan.Plan) { plan.Files[0].Content = strings.Repeat("x", len(plan.Files[0].Content)) }},
		{name: "file kind", mutate: func(plan *renderplan.Plan) { plan.Files[1].Kind = "foreign"; plan.ComputeID() }},
		{name: "file path collision", mutate: func(plan *renderplan.Plan) { plan.Files[1].Path = plan.Files[0].Path; plan.ComputeID() }},
		{name: "map entries", mutate: func(plan *renderplan.Plan) {
			for path, declared := range plan.Maps {
				declared.Entries[0].Value = "forged"
				plan.Maps[path] = declared
				break
			}
			plan.ComputeID()
		}},
		{name: "map key", mutate: func(plan *renderplan.Plan) {
			for path, declared := range plan.Maps {
				delete(plan.Maps, path)
				plan.Maps["forged"] = declared
				break
			}
			plan.ComputeID()
		}},
		{name: "CRT-list key", mutate: func(plan *renderplan.Plan) {
			plan.CRTLists = map[string]renderplan.CRTList{"forged": {Path: "actual"}}
			plan.ComputeID()
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newOutputFixture(t)
			plan := fixture.plan.Clone()
			test.mutate(plan)
			_, err := NewSnapshot(fixture.authority, fixture.config, plan, fixture.artifacts, nil)
			require.Error(t, err)
		})
	}

	fixture := newOutputFixture(t)
	_, err := NewSnapshot(fixture.authority, fixture.config, nil, fixture.artifacts, nil)
	require.Error(t, err)
}

func TestNewSnapshotRejectsOutputSubstitution(t *testing.T) {
	tests := []struct {
		name       string
		changePlan func(*renderplan.Plan)
		changeSpec func([]outputArtifactSpec) []outputArtifactSpec
		config     func(string) string
	}{
		{name: "section differs", changePlan: func(plan *renderplan.Plan) {
			plan.Sections[0].Text = "GLOBAL\n"
			plan.Sections[0].Length = len(plan.Sections[0].Text)
			plan.Sections[0].TextDigest = renderplan.DigestString(plan.Sections[0].Text)
			plan.ComputeID()
		}},
		{name: "config argument differs", config: func(value string) string { return "x" + value[1:] }},
		{name: "config path", changePlan: func(plan *renderplan.Plan) {
			file := planFileByKind(plan, renderplan.FileKindConfig)
			file.Path = "redirected.cfg"
			replacePlanFile(plan, renderplan.FileKindConfig, &file)
			plan.ComputeID()
		}},
		{name: "no config", changePlan: func(plan *renderplan.Plan) {
			file := planFileByKind(plan, renderplan.FileKindConfig)
			file.Kind = renderplan.FileKindGeneral
			replacePlanFile(plan, renderplan.FileKindConfig, &file)
			plan.ComputeID()
		}},
		{name: "two configs", changePlan: func(plan *renderplan.Plan) {
			plan.Files = append(plan.Files, exactPlanFile("other.cfg", renderplan.FileKindConfig, true, plan.Sections[0].Text+plan.Sections[1].Text+plan.Sections[2].Text))
			plan.ComputeID()
		}},
		{name: "missing artifact", changeSpec: func(specs []outputArtifactSpec) []outputArtifactSpec { return specs[1:] }},
		{name: "extra artifact", changeSpec: func(specs []outputArtifactSpec) []outputArtifactSpec {
			return append(specs, outputArtifactSpec{
				descriptor: renderartifact.Descriptor{Family: renderartifact.General, Name: "extra", Path: "files/extra", RuntimePath: "files/extra"},
				content:    "extra",
			})
		}},
		{name: "family", changeSpec: func(specs []outputArtifactSpec) []outputArtifactSpec {
			specs[0].descriptor = renderartifact.Descriptor{
				Family: renderartifact.General, Name: "routes.map", Path: "files/routes.map",
				RuntimePath: specs[0].descriptor.RuntimePath,
			}
			return specs
		}},
		{name: "runtime path", changeSpec: func(specs []outputArtifactSpec) []outputArtifactSpec {
			specs[0].descriptor.RuntimePath = "maps/redirected.map"
			return specs
		}},
		{name: "content", changeSpec: func(specs []outputArtifactSpec) []outputArtifactSpec {
			specs[0].content = "example.evil be_app\n"
			return specs
		}},
		{name: "size", changeSpec: func(specs []outputArtifactSpec) []outputArtifactSpec {
			specs[0].content += "x"
			return specs
		}},
		{name: "reload metadata", changePlan: func(plan *renderplan.Plan) {
			file := planFileByKind(plan, renderplan.FileKindGeneral)
			file.ReloadOnChange = false
			replacePlanFile(plan, renderplan.FileKindGeneral, &file)
			plan.ComputeID()
		}},
		{name: "duplicate runtime path", changeSpec: func(specs []outputArtifactSpec) []outputArtifactSpec {
			return append(specs, outputArtifactSpec{
				descriptor: renderartifact.Descriptor{
					Family: renderartifact.GeneralCA, Name: "duplicate.pem", Path: "files/duplicate.pem",
					RuntimePath: specs[1].descriptor.RuntimePath,
				},
				content: specs[1].content,
			})
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newOutputFixture(t)
			plan := fixture.plan.Clone()
			if test.changePlan != nil {
				test.changePlan(plan)
			}
			specs := cloneArtifactSpecs(fixture.specs)
			if test.changeSpec != nil {
				specs = test.changeSpec(specs)
			}
			artifacts := buildArtifactSnapshot(t, fixture.artifactAuthority, nil, specs)
			config := fixture.config
			if test.config != nil {
				config = test.config(config)
			}
			_, err := NewSnapshot(fixture.authority, config, plan, artifacts, nil)
			require.Error(t, err)
		})
	}
}

func TestSnapshotRejectsForeignLineageAndDetectsExactForeignOutput(t *testing.T) {
	leftFixture := newOutputFixture(t)
	left := mustOutputSnapshot(
		t, leftFixture.authority, leftFixture.config, leftFixture.plan, leftFixture.artifacts, nil,
	)
	rightFixture := newOutputFixture(t)
	right := mustOutputSnapshot(
		t, rightFixture.authority, rightFixture.config, rightFixture.plan, rightFixture.artifacts, nil,
	)

	same, err := left.SameRoot(right)
	require.NoError(t, err)
	assert.False(t, same)
	equal, err := left.ExactEqual(right)
	require.NoError(t, err)
	assert.True(t, equal)

	_, err = NewSnapshot(
		leftFixture.authority, leftFixture.config, leftFixture.plan,
		rightFixture.artifacts, nil,
	)
	require.Error(t, err)
	_, err = NewSnapshot(
		leftFixture.authority, leftFixture.config, leftFixture.plan,
		leftFixture.artifacts, right,
	)
	require.Error(t, err)

	foreignPlan, err := right.PlanSnapshot()
	require.NoError(t, err)
	_, _, err = ReusePrevious(leftFixture.authority, left, foreignPlan, leftFixture.artifacts)
	require.Error(t, err)
}

func TestExactEqualNeverTrustsConfigDigest(t *testing.T) {
	leftFixture := newOutputFixture(t)
	left := mustOutputSnapshot(t, leftFixture.authority, leftFixture.config, leftFixture.plan, leftFixture.artifacts, nil)
	rightFixture := newOutputFixture(t)
	right := mustOutputSnapshot(t, rightFixture.authority, rightFixture.config, rightFixture.plan, rightFixture.artifacts, nil)

	rightConfig, err := right.Config()
	require.NoError(t, err)
	forged := "X" + rightConfig[1:]
	forgedDocument, err := configDocumentFromString(forged)
	require.NoError(t, err)
	forgedConfig := sealConfig(forgedDocument, configDocumentMeasurement{
		bytes: right.root.config.bytes, digest: right.root.config.digest,
	}, nil)
	right.root.config = forgedConfig
	right.root.auth.config = forgedConfig
	require.NoError(t, right.ValidateAuthentication())
	equal, err := left.ExactEqual(right)
	require.NoError(t, err)
	assert.False(t, equal)
}

func TestExactEqualNeverTrustsContentChecksum(t *testing.T) {
	fixture := newOutputBenchmark(t, 8)
	changed := mustOutputSnapshot(
		t,
		fixture.fixture.authority,
		fixture.fixture.config,
		fixture.changedPlan,
		fixture.changedArtifacts,
		fixture.previous,
	)
	require.NotEqual(t, fixture.previous.root.checksum, changed.root.checksum)

	changed.root.checksum = fixture.previous.root.checksum
	changed.root.auth.checksum = fixture.previous.root.checksum
	require.NoError(t, changed.ValidateAuthentication())
	equal, err := fixture.previous.ExactEqual(changed)
	require.NoError(t, err)
	assert.False(t, equal)
}

func TestSnapshotAuthenticationRejectsSubstitution(t *testing.T) {
	fixture := newOutputFixture(t)
	left := mustOutputSnapshot(t, fixture.authority, fixture.config, fixture.plan, fixture.artifacts, nil)
	changedPlan := fixture.plan.Clone()
	mapPath := fixture.specs[0].descriptor.RuntimePath
	declared := changedPlan.Maps[mapPath]
	declared.Ordered = !declared.Ordered
	changedPlan.Maps[mapPath] = declared
	changedPlan.ComputeID()
	right := mustOutputSnapshot(t, fixture.authority, fixture.config, changedPlan, fixture.artifacts, left)

	originalRoot := left.root
	left.root = right.root
	require.ErrorIs(t, left.ValidateAuthentication(), errInvalidSnapshot)
	left.root = originalRoot
	require.NoError(t, left.ValidateAuthentication())

	originalPlan := left.root.plan
	left.root.plan = right.root.plan
	require.ErrorIs(t, left.ValidateAuthentication(), errInvalidSnapshot)
	left.root.plan = originalPlan
	require.NoError(t, left.ValidateAuthentication())

	originalID := left.root.planID
	left.root.planID = "forged"
	require.ErrorIs(t, left.ValidateAuthentication(), errInvalidSnapshot)
	left.root.planID = originalID
	require.NoError(t, left.ValidateAuthentication())

	originalChecksum := left.root.checksum
	left.root.checksum = "forged"
	require.ErrorIs(t, left.ValidateAuthentication(), errInvalidSnapshot)
	left.root.checksum = originalChecksum
	require.NoError(t, left.ValidateAuthentication())

	originalCounts := left.root.counts
	left.root.counts.Files++
	require.ErrorIs(t, left.ValidateAuthentication(), errInvalidSnapshot)
	left.root.counts = originalCounts
	require.NoError(t, left.ValidateAuthentication())

	shallow := *left
	require.ErrorIs(t, shallow.ValidateAuthentication(), errInvalidSnapshot)
	require.ErrorIs(t, (*Snapshot)(nil).ValidateAuthentication(), errInvalidSnapshot)
	_, err := (*Snapshot)(nil).Config()
	require.ErrorIs(t, err, errInvalidSnapshot)
}

func TestSnapshotConcurrentConstructionAndReads(t *testing.T) {
	fixture := newScaleOutputFixture(t, 64)
	previous := mustOutputSnapshot(t, fixture.authority, fixture.config, fixture.plan, fixture.artifacts, nil)
	planSnapshot, err := previous.PlanSnapshot()
	require.NoError(t, err)
	const readers = 32
	const iterations = 20
	start := make(chan struct{})
	errorsChannel := make(chan error, readers)
	var wait sync.WaitGroup
	for range readers {
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			for range iterations {
				reused, hit, reuseErr := ReusePrevious(
					fixture.authority, previous, planSnapshot, fixture.artifacts,
				)
				if reuseErr != nil || !hit || reused != previous {
					errorsChannel <- fmt.Errorf("reuse: hit=%t output=%p error=%w", hit, reused, reuseErr)
					return
				}
				built, buildErr := NewSnapshot(
					fixture.authority, fixture.config, fixture.plan.Clone(), fixture.artifacts, previous,
				)
				if buildErr != nil || built != previous {
					errorsChannel <- fmt.Errorf("build: output=%p error=%w", built, buildErr)
					return
				}
				if _, configErr := built.Config(); configErr != nil {
					errorsChannel <- configErr
					return
				}
			}
		}()
	}
	close(start)
	wait.Wait()
	close(errorsChannel)
	for err := range errorsChannel {
		require.NoError(t, err)
	}
}

func newOutputFixture(tb testing.TB) outputFixture {
	tb.Helper()
	coreText := "global\n"
	profileText := "defaults profile-a from defaults\n    mode http\n"
	backendText := "backend be_app\n    server s1 10.0.0.1:80\n"
	config := coreText + profileText + backendText
	sections := []renderplan.Section{
		exactSection(renderplan.SectionKindCore, "core#0", coreText),
		exactSection(renderplan.SectionKindProfile, "profile-a", profileText),
		exactSection(renderplan.SectionKindBackend, "be_app", backendText),
	}
	backend := renderplan.Backend{
		Name: "be_app", Mode: "http", Shape: renderplan.ShapeDynamic,
		Servers: []renderplan.Server{{Name: "s1", Address: "10.0.0.1", Port: 80}},
		Body:    []string{"server s1 10.0.0.1:80"}, Comments: []string{"route default/app"},
		ContentKnown: true, TextDigest: sections[2].TextDigest,
	}
	backend.BodyDigest = renderplan.DigestString(strings.Join(backend.Body, "\n"))
	backend.CommentsDigest = renderplan.DigestString(strings.Join(backend.Comments, "\n"))
	backend.RecordDigest = backendRecordDigest(&backend)
	_, profileBody, _ := strings.Cut(profileText, "\n")

	specs := []outputArtifactSpec{
		{descriptor: renderartifact.Descriptor{Family: renderartifact.Map, Path: "routes.map", RuntimePath: "maps/routes.map"}, content: "example.test be_app\n"},
		{descriptor: renderartifact.Descriptor{Family: renderartifact.General, Name: "errors.http", Path: "files/errors.http", RuntimePath: "general/errors.http", ReloadOnChange: true}, content: "HTTP/1.1 503 Unavailable\n"},
		{descriptor: renderartifact.Descriptor{Family: renderartifact.Certificate, Path: "certs/tls.pem", RuntimePath: "ssl/tls.pem"}, content: "certificate\n"},
		{descriptor: renderartifact.Descriptor{Family: renderartifact.CA, Path: "ca/trust.pem", RuntimePath: "ssl/ca/trust.pem"}, content: "ca\n"},
		{descriptor: renderartifact.Descriptor{Family: renderartifact.CRTList, Path: "lists/frontend.list", RuntimePath: "ssl/frontend.list"}, content: "tls.pem [alpn h2] example.test\n"},
		{descriptor: renderartifact.Descriptor{Family: renderartifact.GeneralCA, Name: "dynamic-ca.pem", Path: "files/dynamic-ca.pem", RuntimePath: "general/dynamic-ca.pem"}, content: "dynamic ca\n"},
	}
	files := make([]renderplan.File, 0, len(specs)+1)
	files = append(files, exactPlanFile(renderplan.ConfigFilePath, renderplan.FileKindConfig, true, config))
	for _, spec := range specs {
		kind, reload := artifactPlanMetadata(spec.descriptor)
		files = append(files, exactPlanFile(spec.descriptor.RuntimePath, kind, reload, spec.content))
	}
	slices.SortFunc(files, func(left, right renderplan.File) int {
		return strings.Compare(left.Path, right.Path)
	})
	plan := &renderplan.Plan{
		SchemaVersion: renderplan.SchemaVersion,
		Sections:      sections,
		Backends:      map[string]renderplan.Backend{backend.Name: backend},
		Profiles: map[string]renderplan.Profile{
			"profile-a": {Name: "profile-a", BodyDigest: renderplan.DigestString(profileBody)},
		},
		Maps: map[string]renderplan.Map{
			specs[0].descriptor.RuntimePath: {
				Path: specs[0].descriptor.RuntimePath, Ordered: true,
				Entries: renderplan.ParseMapEntries(specs[0].content),
			},
		},
		Files: files,
	}
	plan.ComputeID()
	planAuthority := renderplan.NewAuthority()
	artifactAuthority := renderartifact.NewAuthority()
	authority, err := NewAuthority(planAuthority, artifactAuthority)
	require.NoError(tb, err)
	return outputFixture{
		config: config, plan: plan, specs: specs, planAuthority: planAuthority,
		artifactAuthority: artifactAuthority, authority: authority,
		artifacts: buildArtifactSnapshot(tb, artifactAuthority, nil, specs),
	}
}

func newScaleOutputFixture(tb testing.TB, count int) outputFixture {
	tb.Helper()
	config := "global\n"
	plan := &renderplan.Plan{
		SchemaVersion: renderplan.SchemaVersion,
		Sections:      []renderplan.Section{exactSection(renderplan.SectionKindCore, "core#0", config)},
		Maps:          make(map[string]renderplan.Map, count),
		Files:         make([]renderplan.File, 0, count+1),
	}
	plan.Files = append(plan.Files, exactPlanFile(renderplan.ConfigFilePath, renderplan.FileKindConfig, true, config))
	specs := make([]outputArtifactSpec, count)
	for index := range count {
		path := fmt.Sprintf("maps/route-%06d.map", index)
		content := fmt.Sprintf("host-%06d.example backend-%06d\n", index, index)
		specs[index] = outputArtifactSpec{
			descriptor: renderartifact.Descriptor{Family: renderartifact.Map, Path: path, RuntimePath: path},
			content:    content,
		}
		plan.Maps[path] = renderplan.Map{Path: path, Ordered: true, Entries: renderplan.ParseMapEntries(content)}
		plan.Files = append(plan.Files, exactPlanFile(path, renderplan.FileKindMap, false, content))
	}
	plan.ComputeID()
	planAuthority := renderplan.NewAuthority()
	artifactAuthority := renderartifact.NewAuthority()
	authority, err := NewAuthority(planAuthority, artifactAuthority)
	require.NoError(tb, err)
	return outputFixture{
		config: config, plan: plan, specs: specs, planAuthority: planAuthority,
		artifactAuthority: artifactAuthority, authority: authority,
		artifacts: buildArtifactSnapshot(tb, artifactAuthority, nil, specs),
	}
}

func exactSection(kind, name, text string) renderplan.Section {
	return renderplan.Section{
		Kind: kind, Name: name, TextDigest: renderplan.DigestString(text),
		Length: len(text), Text: text, TextKnown: true,
	}
}

func exactPlanFile(path, kind string, reload bool, content string) renderplan.File {
	return renderplan.File{
		Path: path, Kind: kind, ReloadOnChange: reload,
		Digest: renderplan.DigestString(content), Size: int64(len(content)),
		Content: content, ContentKnown: true,
	}
}

func buildArtifactSnapshot(
	tb testing.TB,
	authority *renderartifact.Authority,
	previous *renderartifact.Snapshot,
	specs []outputArtifactSpec,
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

func mustOutputSnapshot(
	tb testing.TB,
	authority *Authority,
	config string,
	plan *renderplan.Plan,
	artifacts *renderartifact.Snapshot,
	previous *Snapshot,
) *Snapshot {
	tb.Helper()
	snapshot, err := NewSnapshot(authority, config, plan, artifacts, previous)
	require.NoError(tb, err)
	return snapshot
}

func cloneArtifactSpecs(source []outputArtifactSpec) []outputArtifactSpec {
	return slices.Clone(source)
}

func planFileByKind(plan *renderplan.Plan, kind string) renderplan.File {
	for _, file := range plan.Files {
		if file.Kind == kind {
			return file
		}
	}
	panic("plan file kind not found: " + kind)
}

func replacePlanFile(plan *renderplan.Plan, oldKind string, replacement *renderplan.File) {
	for index := range plan.Files {
		if plan.Files[index].Kind == oldKind {
			plan.Files[index] = *replacement
			return
		}
	}
	panic("plan file kind not found: " + oldKind)
}
