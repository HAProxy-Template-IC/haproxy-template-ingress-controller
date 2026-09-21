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

package diagnostics

import (
	"archive/zip"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

const bundleReadme = `HAPTIC diagnostic snapshot

report.json contains resource names, UIDs, image versions and digests, status
conditions without messages, checksums, plan IDs, counts, and fixed findings.
It excludes Secret data, configuration contents, rendered files, logs, arbitrary
error messages, event payloads, environment variables, and kubeconfig credentials.
Names and identifiers can still reveal information about your infrastructure.

Collection is a sequence of observations, not an atomic cluster snapshot.
Rerun after reconciliation if plan IDs or deployment status differ.
Healthy covers configuration validation, controller phases, and fleet deployment.
Watched-resource conditions are evidence; their polarity is resource-defined.
Complete=false means at least one required observation could not be collected.
`

func WriteJSON(writer io.Writer, report *Report) error {
	encoder := json.NewEncoder(writer)
	encoder.SetIndent("", "  ")
	return encoder.Encode(report)
}

func WriteText(writer io.Writer, report *Report) error {
	if _, err := fmt.Fprintf(writer, "HAPTIC %s/%s: healthy=%t complete=%t\nControllers: %d; agents: %d; resources with conditions: %d\n", report.Namespace, report.Release, report.Healthy, report.Complete, len(report.Controllers), len(report.Agents), len(report.Resources)); err != nil {
		return err
	}
	for _, finding := range report.Findings {
		if _, err := fmt.Fprintf(writer, "%s %s [%s]: %s\n", finding.Severity, finding.Code, finding.Resource, finding.Action); err != nil {
			return err
		}
	}
	return nil
}

// WriteBundle creates a private, exclusive archive from the same allowlisted report.
func WriteBundle(path string, report *Report) (result error) {
	directory, err := os.OpenRoot(filepath.Dir(path))
	if err != nil {
		return fmt.Errorf("open bundle directory: %w", err)
	}
	defer directory.Close()
	name := filepath.Base(path)
	file, err := directory.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("create diagnostic bundle: %w", err)
	}
	defer func() {
		if err := file.Close(); result == nil {
			result = err
		}
		if result != nil {
			_ = directory.Remove(name)
		}
	}()
	archive := zip.NewWriter(file)
	if err := writeBundleEntry(archive, "README.txt", func(writer io.Writer) error { _, err := io.WriteString(writer, bundleReadme); return err }); err != nil {
		return err
	}
	if err := writeBundleEntry(archive, "report.json", func(writer io.Writer) error { return WriteJSON(writer, report) }); err != nil {
		return err
	}
	return archive.Close()
}

func writeBundleEntry(archive *zip.Writer, name string, write func(io.Writer) error) error {
	header := &zip.FileHeader{Name: name, Method: zip.Deflate}
	header.SetMode(0o600)
	writer, err := archive.CreateHeader(header)
	if err != nil {
		return err
	}
	return write(writer)
}
