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

// gen-validators generates zero-allocation OpenAPI validators for HAProxy models.
//
// It reads the pinned Data Plane API OpenAPI specs under cmd/gen-validators/spec
// and produces Go validation functions that work directly on client-native
// structs. The output is the browser playground's schema check: the playground
// has no haproxy binary, so `haproxy_valid` falls back to a syntax + schema
// parse there. Nothing in a production binary uses it.
//
// Usage:
//
//	go run ./cmd/gen-validators
//
// Or via make:
//
//	make generate-playground-validators
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"go/format"
	"hash/fnv"
	"os"
	"path/filepath"
)

// Schema type constants.
const (
	schemaTypeString  = "string"
	schemaTypeInteger = "integer"
	schemaTypeNumber  = "number"
	schemaTypeBoolean = "boolean"
)

// targetSchemas lists the schema names we need to generate validators for.
// These are the high-volume models that cause allocation pressure.
var targetSchemas = []string{
	"server",
	"server_template",
	"bind",
	"http_request_rule",
	"http_response_rule",
	"tcp_request_rule",
	"tcp_response_rule",
	"http_after_response_rule",
	"http_error_rule",
	"server_switching_rule",
	"backend_switching_rule",
	"stick_rule",
	"acl",
	"filter",
	"log_target",
	"http_check",
	"tcp_check",
	"capture",
}

// specFiles maps version strings to their pinned OpenAPI spec.
var specFiles = map[string]string{
	"v30": "cmd/gen-validators/spec/v30.json",
	"v31": "cmd/gen-validators/spec/v31.json",
	"v32": "cmd/gen-validators/spec/v32.json",
	"v33": "cmd/gen-validators/spec/v33.json",
}

func main() {
	flag.Parse()

	// Find project root (go.mod location)
	projectRoot, err := findProjectRoot()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error finding project root: %v\n", err)
		os.Exit(1)
	}

	// Process each version
	allPatterns := make(map[string]bool)
	versionSchemas := make(map[string]map[string]*ResolvedSchema)

	for version, file := range specFiles {
		specPath := filepath.Join(projectRoot, file)
		spec, err := loadSpec(specPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error loading spec %s: %v\n", specPath, err)
			os.Exit(1)
		}

		schemas := make(map[string]*ResolvedSchema)
		for _, schemaName := range targetSchemas {
			resolved, err := resolveSchema(spec, schemaName)
			if err != nil {
				// Schema might not exist in older versions
				fmt.Fprintf(os.Stderr, "Warning: schema %s not found in %s: %v\n", schemaName, version, err)
				continue
			}
			// Filter out properties that don't exist on client-native model structs.
			// Newer specs may define fields not yet in the client-native library.
			resolved = filterSchemaProperties(schemaName, resolved)

			schemas[schemaName] = resolved

			// Collect all patterns
			for _, prop := range resolved.Properties {
				if prop.Pattern != "" {
					allPatterns[prop.Pattern] = true
				}
			}
		}
		versionSchemas[version] = schemas
	}

	// Generate output files.
	// Generated validators live under pkg/generated/validators (package genvalidators),
	// separate from pkg/dataplane/validators which holds the hand-written ValidatorSet
	// and Cache. This keeps the generated code out of the coverage denominator
	// (pkg/generated/... is excluded from COVERAGE_PACKAGES) and mirrors the layout
	// of the other generated packages (pkg/generated/clientset).
	outputDir := filepath.Join(projectRoot, "pkg", "generated", "validators")

	// Generate patterns.go with pre-compiled regexes
	if err := generatePatternsFile(outputDir, allPatterns); err != nil {
		fmt.Fprintf(os.Stderr, "Error generating patterns.go: %v\n", err)
		os.Exit(1)
	}

	// Generate version-specific validator files
	for version, schemas := range versionSchemas {
		if err := generateVersionFile(outputDir, version, schemas); err != nil {
			fmt.Fprintf(os.Stderr, "Error generating %s validators: %v\n", version, err)
			os.Exit(1)
		}
	}

	fmt.Println("Successfully generated validators")
}

// findProjectRoot locates the project root by looking for go.mod.
func findProjectRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}

	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			return "", errors.New("go.mod not found")
		}
		dir = parent
	}
}

// loadSpec loads and parses an OpenAPI spec JSON file.
func loadSpec(path string) (*OpenAPISpec, error) {
	// Clean the path to prevent path traversal attacks (gosec G304).
	cleanPath := filepath.Clean(path)
	data, err := os.ReadFile(cleanPath)
	if err != nil {
		return nil, fmt.Errorf("reading spec: %w", err)
	}

	var spec OpenAPISpec
	if err := json.Unmarshal(data, &spec); err != nil {
		return nil, fmt.Errorf("parsing spec: %w", err)
	}

	return &spec, nil
}

// writeFormattedFile writes Go code to a file, formatting it first.
func writeFormattedFile(path string, data []byte) error {
	formatted, err := format.Source(data)
	if err != nil {
		// Write unformatted for debugging
		_ = os.WriteFile(path+".unformatted", data, 0o600)
		return fmt.Errorf("formatting %s: %w (unformatted written to %s.unformatted)", path, err, path)
	}

	return os.WriteFile(path, formatted, 0o600)
}

// patternVarName converts a regex pattern to a valid Go variable name.
func patternVarName(pattern string) string {
	// Create a unique name based on pattern content
	switch pattern {
	case "^[^\\s]+$":
		return "patternNoWhitespace"
	case "^[A-Za-z0-9-_.:]+$":
		return "patternGUID"
	case "^https://[^\\s]+$":
		return "patternHTTPS"
	case "^([A-Za-z0-9.:/]+)(,[A-Za-z0-9.:/]+)*$":
		return "patternResolveNet"
	case "^(allow-dup-ip|ignore-weight|prevent-dup-ip)(,(allow-dup-ip|ignore-weight|prevent-dup-ip))*$":
		return "patternResolveOpts"
	case "^(?:[A-Za-z]+\\(\"([A-Za-z\\s]+)\"\\)|[A-Za-z]+)":
		return "patternCaptureSample"
	default:
		// Generate a hash-based name for unknown patterns
		h := fnv.New64a()
		_, _ = h.Write([]byte(pattern))
		return fmt.Sprintf("pattern_%x", h.Sum64()&0xFFFF)
	}
}

const generatedHeader = `// Code generated by gen-validators; DO NOT EDIT.

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

`
