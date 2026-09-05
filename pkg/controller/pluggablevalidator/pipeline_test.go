package pluggablevalidator

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/pipeline"
	controllertestutil "gitlab.com/haproxy-haptic/haptic/pkg/controller/testutil"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/auxiliaryfiles"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/renderplan"
)

func TestBuildFiles(t *testing.T) {
	result := exactValidatorPipelineResult(t, "global\n", &dataplane.AuxiliaryFiles{
		GeneralFiles:    []auxiliaryfiles.GeneralFile{{Path: "/etc/haproxy/general/500.http", Content: "response"}},
		SSLCertificates: []auxiliaryfiles.SSLCertificate{{Path: "/etc/haproxy/ssl/example.pem", Content: "certificate"}},
		SSLCaFiles:      []auxiliaryfiles.SSLCaFile{{Path: "/etc/haproxy/ssl/ca.pem", Content: "ca"}},
		MapFiles:        []auxiliaryfiles.MapFile{{Path: "/etc/haproxy/maps/host.map", Content: "map"}},
		CRTListFiles:    []auxiliaryfiles.CRTListFile{{Path: "/etc/haproxy/crt-list.txt", Content: "list"}},
	})

	files, err := buildFiles(result)
	require.NoError(t, err)

	require.Len(t, files, 6)
	assert.Equal(t, "/etc/haproxy/haproxy.cfg", files[0].Path)
	assert.Equal(t, "global\n", files[0].Content)
	paths := make([]string, 0, len(files))
	for _, file := range files {
		paths = append(paths, file.Path)
	}
	assert.Contains(t, paths, "/etc/haproxy/general/500.http")
	assert.Contains(t, paths, "/etc/haproxy/ssl/example.pem")
	assert.Contains(t, paths, "/etc/haproxy/ssl/ca.pem")
	assert.Contains(t, paths, "/etc/haproxy/maps/host.map")
	assert.Contains(t, paths, "/etc/haproxy/crt-list.txt")
}

func TestBuildFiles_NilAuxiliaryFiles(t *testing.T) {
	files, err := buildFiles(exactValidatorPipelineResult(t, "global\n", nil))
	require.NoError(t, err)

	require.Len(t, files, 1)
	assert.Equal(t, "/etc/haproxy/haproxy.cfg", files[0].Path)
}

func TestBuildFilesRequiresCycleAndIgnoresPublicShadows(t *testing.T) {
	trusted := exactValidatorPipelineResult(t, "global\n", nil)
	poisoned := exactValidatorPipelineResult(t, "defaults\n", nil)
	poisonedOutput, err := poisoned.CycleSnapshot.OutputSnapshot()
	require.NoError(t, err)
	trusted.OutputSnapshot = poisonedOutput
	trusted.HAProxyConfig = "poisoned\n"
	trusted.AuxiliaryFiles = &dataplane.AuxiliaryFiles{
		GeneralFiles: []auxiliaryfiles.GeneralFile{{Path: "/poison", Content: "poison"}},
	}

	files, err := buildFiles(trusted)
	require.NoError(t, err)
	require.Len(t, files, 1)
	assert.Equal(t, "global\n", files[0].Content)

	_, err = buildFiles(&pipeline.PipelineResult{OutputSnapshot: poisonedOutput})
	require.ErrorContains(t, err, "no authenticated render cycle")
}

func exactValidatorPipelineResult(
	t *testing.T,
	config string,
	auxFiles *dataplane.AuxiliaryFiles,
) *pipeline.PipelineResult {
	t.Helper()
	fixture := controllertestutil.NewRenderCycleFixture(t)
	artifacts := fixture.Artifacts(t, auxFiles, nil)
	plan := validatorRenderPlan(config, auxFiles)
	cycle := fixture.SnapshotWithEffects(t, config, plan, artifacts, nil, nil, nil, nil)
	output, err := cycle.OutputSnapshot()
	require.NoError(t, err)
	checksum, err := cycle.ContentChecksum()
	require.NoError(t, err)
	return &pipeline.PipelineResult{
		CycleSnapshot:         cycle,
		OutputSnapshot:        output,
		HAProxyConfig:         config,
		AuxiliaryFileSnapshot: artifacts,
		ContentChecksum:       checksum,
	}
}

func validatorRenderPlan(config string, auxFiles *dataplane.AuxiliaryFiles) *renderplan.Plan {
	plan := &renderplan.Plan{
		SchemaVersion: renderplan.SchemaVersion,
		Sections: []renderplan.Section{{
			Kind: renderplan.SectionKindCore, Name: "core#0", Text: config,
			TextKnown: true, TextDigest: renderplan.DigestString(config), Length: len(config),
		}},
		Maps:     make(map[string]renderplan.Map),
		CRTLists: make(map[string]renderplan.CRTList),
		Files: []renderplan.File{{
			Path: renderplan.ConfigFilePath, Kind: renderplan.FileKindConfig,
			ReloadOnChange: true, Content: config, ContentKnown: true,
			Digest: renderplan.DigestString(config), Size: int64(len(config)),
		}},
	}
	if auxFiles == nil {
		plan.ComputeID()
		return plan
	}
	for index := range auxFiles.GeneralFiles {
		file := auxFiles.GeneralFiles[index]
		kind := renderplan.FileKindGeneral
		if file.IsCaFile {
			kind = renderplan.FileKindCA
		}
		appendValidatorPlanFile(plan, file.Path, kind, file.Content, file.ReloadsOnPush())
	}
	for index := range auxFiles.SSLCertificates {
		file := auxFiles.SSLCertificates[index]
		appendValidatorPlanFile(plan, file.Path, renderplan.FileKindCert, file.Content, false)
	}
	for index := range auxFiles.SSLCaFiles {
		file := auxFiles.SSLCaFiles[index]
		appendValidatorPlanFile(plan, file.Path, renderplan.FileKindCA, file.Content, false)
	}
	for index := range auxFiles.MapFiles {
		file := auxFiles.MapFiles[index]
		plan.Maps[file.Path] = renderplan.Map{
			Path: file.Path, Ordered: true, Entries: renderplan.ParseMapEntries(file.Content),
		}
		appendValidatorPlanFile(plan, file.Path, renderplan.FileKindMap, file.Content, false)
	}
	for index := range auxFiles.CRTListFiles {
		file := auxFiles.CRTListFiles[index]
		plan.CRTLists[file.Path] = renderplan.CRTList{Path: file.Path}
		appendValidatorPlanFile(plan, file.Path, renderplan.FileKindCRTList, file.Content, false)
	}
	plan.ComputeID()
	return plan
}

func appendValidatorPlanFile(
	plan *renderplan.Plan,
	path, kind, content string,
	reload bool,
) {
	plan.Files = append(plan.Files, renderplan.File{
		Path: path, Kind: kind, ReloadOnChange: reload,
		Content: content, ContentKnown: true,
		Digest: renderplan.DigestString(content), Size: int64(len(content)),
	})
}

func TestFormatDiagnostic(t *testing.T) {
	tests := []struct {
		name string
		in   Diagnostic
		want string
	}{
		{name: "full location", in: Diagnostic{Path: "/file", Line: 4, Column: 2, Message: "bad token"}, want: "/file:4:2: bad token"},
		{name: "line only", in: Diagnostic{Path: "/file", Line: 4, Message: "bad token"}, want: "/file:4: bad token"},
		{name: "file only", in: Diagnostic{Path: "/file", Message: "bad token"}, want: "/file: bad token"},
		{name: "message only", in: Diagnostic{Message: "timeout"}, want: "timeout"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assert.Equal(t, test.want, formatDiagnostic(test.in))
		})
	}
}

func TestFormatDiagnostics(t *testing.T) {
	assert.Nil(t, formatDiagnostics(nil))
	assert.Equal(t, []string{"/file: first", "/file:2:3: second"}, formatDiagnostics([]Diagnostic{
		{Path: "/file", Message: "first"},
		{Path: "/file", Line: 2, Column: 3, Message: "second"},
	}))
	assert.Equal(t, "/file: first\n/file:2:3: second", formatErrorReason([]Diagnostic{
		{Path: "/file", Message: "first"},
		{Path: "/file", Line: 2, Column: 3, Message: "second"},
	}))
}
