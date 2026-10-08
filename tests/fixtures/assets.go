// Copyright 2026 Philipp Hossner
// SPDX-License-Identifier: Apache-2.0

// Package fixtures owns reusable test assets and factories that return fresh data.
package fixtures

import (
	"embed"
	"fmt"
	"io/fs"
	"os"
)

//go:embed chart-upgrade/*.yaml
var assets embed.FS

func ChartUpgrade(name string) ([]byte, error) {
	if !fs.ValidPath(name) {
		return nil, fmt.Errorf("invalid fixture name %q", name)
	}
	return assets.ReadFile("chart-upgrade/" + name)
}

func WriteChartUpgrade(directory string) error {
	root, err := os.OpenRoot(directory)
	if err != nil {
		return err
	}
	defer root.Close()
	for _, name := range []string{"values.yaml", "values-0.1.0.yaml", "routes.yaml"} {
		content, err := ChartUpgrade(name)
		if err != nil {
			return err
		}
		if err := root.WriteFile(name, content, 0o600); err != nil {
			return err
		}
	}
	return nil
}
