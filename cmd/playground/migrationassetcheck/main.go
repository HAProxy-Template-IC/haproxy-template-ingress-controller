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

package main

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"regexp"
	"sort"
	"strings"

	"gitlab.com/haproxy-haptic/haptic/cmd/playground/internal/migratecheck"
)

var safeSourceID = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]*[a-z0-9])?$`)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: migrationassetcheck <migration-asset-directory>")
		os.Exit(2)
	}
	if err := validateDirectory(os.Args[1]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func validateDirectory(dir string) error {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return fmt.Errorf("opening migration asset directory: %w", err)
	}
	defer root.Close()

	manifest, err := readManifest(root)
	if err != nil {
		return err
	}
	referenced, err := collectSources(manifest)
	if err != nil {
		return err
	}
	if err := validateAssets(root, referenced); err != nil {
		return err
	}
	return rejectUnreferencedAssets(root, referenced)
}

func readManifest(root *os.Root) (map[string][]string, error) {
	manifestData, err := root.ReadFile("presets.json")
	if err != nil {
		return nil, fmt.Errorf("reading migration preset manifest: %w", err)
	}
	var manifest map[string][]string
	if err := json.Unmarshal(manifestData, &manifest); err != nil {
		return nil, fmt.Errorf("parsing migration preset manifest: %w", err)
	}
	if len(manifest) == 0 {
		return nil, fmt.Errorf("migration preset manifest is empty")
	}
	return manifest, nil
}

func collectSources(manifest map[string][]string) (map[string]struct{}, error) {
	referenced := map[string]struct{}{}
	for preset, sources := range manifest {
		if strings.TrimSpace(preset) == "" {
			return nil, fmt.Errorf("migration preset manifest has an empty preset name")
		}
		seen := map[string]struct{}{}
		for _, source := range sources {
			if !safeSourceID.MatchString(source) {
				return nil, fmt.Errorf("migration preset %q has unsafe source id %q", preset, source)
			}
			if _, exists := seen[source]; exists {
				return nil, fmt.Errorf("migration preset %q repeats source %q", preset, source)
			}
			seen[source] = struct{}{}
			referenced[source] = struct{}{}
		}
	}
	return referenced, nil
}

func validateAssets(root *os.Root, referenced map[string]struct{}) error {
	for source := range referenced {
		data, err := root.ReadFile(source + ".json")
		if err != nil {
			return fmt.Errorf("reading migration asset %q: %w", source, err)
		}
		aggregate := make([]byte, 0, len(data)+2)
		aggregate = append(aggregate, '[')
		aggregate = append(aggregate, data...)
		aggregate = append(aggregate, ']')
		coverage, err := migratecheck.ParseCoverage(aggregate)
		if err != nil {
			return fmt.Errorf("validating migration asset %q: %w", source, err)
		}
		if len(coverage) != 1 || coverage[0].Source != source {
			return fmt.Errorf("migration asset %q must declare exactly that source", source)
		}
	}
	return nil
}

func rejectUnreferencedAssets(root *os.Root, referenced map[string]struct{}) error {
	files, err := fs.ReadDir(root.FS(), ".")
	if err != nil {
		return fmt.Errorf("listing migration assets: %w", err)
	}
	var unreferenced []string
	for _, file := range files {
		if file.IsDir() || !strings.HasSuffix(file.Name(), ".json") {
			continue
		}
		source := strings.TrimSuffix(file.Name(), ".json")
		if source == "presets" {
			continue
		}
		if _, exists := referenced[source]; !exists {
			unreferenced = append(unreferenced, source)
		}
	}
	if len(unreferenced) > 0 {
		sort.Strings(unreferenced)
		return fmt.Errorf("unreferenced migration assets: %s", strings.Join(unreferenced, ", "))
	}
	return nil
}
