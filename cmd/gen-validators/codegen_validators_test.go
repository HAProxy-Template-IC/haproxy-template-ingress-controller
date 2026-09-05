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

package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGenerateVersionFileEmitsOnlyPlaygroundValidators(t *testing.T) {
	outputDir := t.TempDir()
	require.NoError(t, generateVersionFile(outputDir, "v30", map[string]*ResolvedSchema{
		"server": {},
	}))

	generated, err := os.ReadFile(filepath.Join(outputDir, "v30_generated.go"))
	require.NoError(t, err)
	assert.Contains(t, string(generated), "//go:build playground")
	assert.Contains(t, string(generated), "func ValidateServerV30")
	assert.NotContains(t, string(generated), "func Hash")
}
