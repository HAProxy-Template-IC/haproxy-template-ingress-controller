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

package rendercontext

import (
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/names"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/renderplan"
)

// PlanFiles lists every file of the render with its digest, in the kinds the
// deploy side reasons about. ReloadOnChange is true only where a content change
// cannot be applied over the runtime API.
func PlanFiles(config string, aux *dataplane.AuxiliaryFiles) []renderplan.File {
	files := []renderplan.File{{
		Path:           names.MainTemplateName,
		Kind:           renderplan.FileKindConfig,
		ReloadOnChange: true,
		Digest:         renderplan.DigestString(config),
		Size:           int64(len(config)),
	}}
	if aux == nil {
		return files
	}

	for _, file := range aux.MapFiles {
		files = append(files, planFile(file.Path, renderplan.FileKindMap, false, file.Content))
	}
	for _, file := range aux.SSLCertificates {
		files = append(files, planFile(file.Path, renderplan.FileKindCert, false, file.Content))
	}
	for _, file := range aux.SSLCaFiles {
		files = append(files, planFile(file.Path, renderplan.FileKindCA, false, file.Content))
	}
	for _, file := range aux.CRTListFiles {
		files = append(files, planFile(file.Path, renderplan.FileKindCRTList, false, file.Content))
	}
	for _, file := range aux.GeneralFiles {
		// A ca-file is delivered as a general file but rotates over the runtime
		// API, so it is a CA file to the deploy side (file_registry.GetFiles).
		if file.IsCaFile {
			files = append(files, planFile(file.Path, renderplan.FileKindCA, false, file.Content))
			continue
		}
		reload := file.ReloadOnPush == nil || *file.ReloadOnPush
		files = append(files, planFile(file.Path, renderplan.FileKindGeneral, reload, file.Content))
	}
	return files
}

func planFile(path, kind string, reloadOnChange bool, content string) renderplan.File {
	return renderplan.File{
		Path:           path,
		Kind:           kind,
		ReloadOnChange: reloadOnChange,
		Digest:         renderplan.DigestString(content),
		Size:           int64(len(content)),
	}
}

// MapContents keys the rendered map content by the path the plan lists it under.
func MapContents(aux *dataplane.AuxiliaryFiles) map[string]string {
	if aux == nil {
		return nil
	}
	contents := make(map[string]string, len(aux.MapFiles))
	for _, file := range aux.MapFiles {
		contents[file.Path] = file.Content
	}
	return contents
}
