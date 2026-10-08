// Copyright 2026 Philipp Hossner
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"os"
	"path/filepath"
)

func artifactDirectory(root, requested, scenario string) (string, error) {
	if requested != "" {
		path, err := filepath.Abs(requested)
		if err != nil {
			return "", err
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			return "", err
		}
		return path, os.Mkdir(path, 0o750)
	}
	parent := filepath.Join(root, "debug-logs", scenario)
	if err := os.MkdirAll(parent, 0o750); err != nil {
		return "", err
	}
	return os.MkdirTemp(parent, "run-")
}

func scenarioArtifacts(root string, options *options) (string, error) {
	if options.name != upgradeScenario {
		return artifactDirectory(root, options.artifacts, options.name)
	}
	parent := options.artifacts
	if parent == "" {
		parent = filepath.Join(root, "debug-logs", "upgrade")
	}
	parent, err := filepath.Abs(filepath.Join(parent, options.baseline))
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(parent, 0o750); err != nil {
		return "", err
	}
	return os.MkdirTemp(parent, "run-")
}
