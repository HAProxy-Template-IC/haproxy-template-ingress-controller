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
	"bytes"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
)

// generatePatternsFile creates patterns.go with pre-compiled regex patterns.
func generatePatternsFile(outputDir string, patterns map[string]bool) error {
	var buf bytes.Buffer

	buf.WriteString(generatedHeader)
	buf.WriteString(`package validators

import "regexp"

// Pre-compiled regex patterns for validation.
// These are compiled once at package initialization to avoid
// repeated compilation during validation.
var (
`)

	// Sort patterns for deterministic output
	sortedPatterns := make([]string, 0, len(patterns))
	for p := range patterns {
		sortedPatterns = append(sortedPatterns, p)
	}
	sort.Strings(sortedPatterns)

	for _, pattern := range sortedPatterns {
		varName := patternVarName(pattern)
		// Escape backticks in patterns
		escapedPattern := strings.ReplaceAll(pattern, "`", "` + \"`\" + `")
		buf.WriteString(fmt.Sprintf("\t%s = regexp.MustCompile(`%s`)\n", varName, escapedPattern))
	}

	buf.WriteString(")\n")

	return writeFormattedFile(filepath.Join(outputDir, "patterns.go"), buf.Bytes())
}

// generateVersionFile creates the version-specific validator file.
func generateVersionFile(outputDir, version string, schemas map[string]*ResolvedSchema) error {
	var buf bytes.Buffer

	buf.WriteString(generatedHeader)
	buf.WriteString(fmt.Sprintf(`package validators

import (
	"encoding/binary"

	"github.com/cespare/xxhash/v2"
	"github.com/haproxytech/client-native/v6/models"
)

// Version-specific validators for %s.
// These functions validate client-native models directly without JSON conversion.

`, version))

	// Sort schema names for deterministic output
	schemaNames := make([]string, 0, len(schemas))
	for name := range schemas {
		schemaNames = append(schemaNames, name)
	}
	sort.Strings(schemaNames)

	for _, schemaName := range schemaNames {
		schema := schemas[schemaName]
		generateValidator(&buf, version, schemaName, schema)
		generateHasher(&buf, version, schemaName, schema)
	}

	return writeFormattedFile(filepath.Join(outputDir, version+"_generated.go"), buf.Bytes())
}

// generateValidator generates a validation function for a schema.
func generateValidator(buf *bytes.Buffer, version, schemaName string, schema *ResolvedSchema) {
	funcName := fmt.Sprintf("Validate%s%s", goTypeName(schemaName), strings.ToUpper(version))
	modelType := goModelType(schemaName)

	fmt.Fprintf(buf, "// %s validates a %s model.\n", funcName, schemaName)
	fmt.Fprintf(buf, "func %s(m *models.%s) error {\n", funcName, modelType)
	buf.WriteString("\tif m == nil {\n\t\treturn nil\n\t}\n\n")

	// Generate required field checks
	for _, field := range schema.Required {
		prop, ok := schema.Properties[field]
		if !ok {
			continue
		}
		goField := goFieldName(field)
		generateRequiredCheck(buf, goField, field, prop)
	}

	// Generate constraint checks for all properties
	// Sort for deterministic output
	propNames := make([]string, 0, len(schema.Properties))
	for name := range schema.Properties {
		propNames = append(propNames, name)
	}
	sort.Strings(propNames)

	for _, propName := range propNames {
		prop := schema.Properties[propName]
		goField := goFieldName(propName)
		generateConstraintChecks(buf, goField, propName, prop)
	}

	buf.WriteString("\treturn nil\n}\n\n")
}

// generateRequiredCheck generates a required field check.
func generateRequiredCheck(buf *bytes.Buffer, goField, jsonField string, prop *Property) {
	switch prop.Type {
	case schemaTypeString:
		if prop.IsPointer {
			fmt.Fprintf(buf, "\tif m.%s == nil || *m.%s == \"\" {\n", goField, goField)
		} else {
			fmt.Fprintf(buf, "\tif m.%s == \"\" {\n", goField)
		}
		fmt.Fprintf(buf, "\t\treturn &ValidationError{Field: %q, Message: \"required\"}\n", jsonField)
		buf.WriteString("\t}\n\n")
	case schemaTypeInteger, schemaTypeNumber:
		if prop.IsPointer {
			fmt.Fprintf(buf, "\tif m.%s == nil {\n", goField)
			fmt.Fprintf(buf, "\t\treturn &ValidationError{Field: %q, Message: \"required\"}\n", jsonField)
			buf.WriteString("\t}\n\n")
		}
		// Non-pointer integers are always present (zero value is valid)
	}
}

// generateConstraintChecks generates validation checks for property constraints.
func generateConstraintChecks(buf *bytes.Buffer, goField, jsonField string, prop *Property) {
	generatePatternCheck(buf, goField, jsonField, prop)
	generateEnumCheck(buf, goField, jsonField, prop)
	generateRangeCheck(buf, goField, jsonField, prop)
}

// generatePatternCheck generates regex pattern validation code.
func generatePatternCheck(buf *bytes.Buffer, goField, jsonField string, prop *Property) {
	if prop.Pattern == "" || prop.Type != schemaTypeString {
		return
	}
	varName := patternVarName(prop.Pattern)
	if prop.IsPointer {
		fmt.Fprintf(buf, "\tif m.%s != nil && *m.%s != \"\" && !%s.MatchString(*m.%s) {\n",
			goField, goField, varName, goField)
	} else {
		fmt.Fprintf(buf, "\tif m.%s != \"\" && !%s.MatchString(m.%s) {\n",
			goField, varName, goField)
	}
	fmt.Fprintf(buf, "\t\treturn &ValidationError{Field: %q, Message: \"invalid format\"}\n", jsonField)
	buf.WriteString("\t}\n\n")
}

// generateEnumCheck generates enum validation code.
func generateEnumCheck(buf *bytes.Buffer, goField, jsonField string, prop *Property) {
	if len(prop.Enum) == 0 || prop.Type != schemaTypeString {
		return
	}
	enumValues := make([]string, len(prop.Enum))
	enumDisplay := make([]string, len(prop.Enum))
	for i, v := range prop.Enum {
		enumValues[i] = fmt.Sprintf("%q", v)
		enumDisplay[i] = v
	}

	if prop.IsPointer {
		fmt.Fprintf(buf, "\tif m.%s != nil && *m.%s != \"\" {\n", goField, goField)
		fmt.Fprintf(buf, "\t\tswitch *m.%s {\n", goField)
	} else {
		fmt.Fprintf(buf, "\tif m.%s != \"\" {\n", goField)
		fmt.Fprintf(buf, "\t\tswitch m.%s {\n", goField)
	}
	fmt.Fprintf(buf, "\t\tcase %s:\n", strings.Join(enumValues, ", "))
	buf.WriteString("\t\t\t// valid\n")
	buf.WriteString("\t\tdefault:\n")
	fmt.Fprintf(buf, "\t\t\treturn &ValidationError{Field: %q, Message: \"must be one of: %s\"}\n",
		jsonField, strings.Join(enumDisplay, ", "))
	buf.WriteString("\t\t}\n")
	buf.WriteString("\t}\n\n")
}

// generateRangeCheck generates min/max range validation code.
func generateRangeCheck(buf *bytes.Buffer, goField, jsonField string, prop *Property) {
	if prop.Type != schemaTypeInteger && prop.Type != schemaTypeNumber {
		return
	}
	if prop.Minimum == nil && prop.Maximum == nil {
		return
	}
	if prop.IsPointer {
		generateRangeCheckPointer(buf, goField, jsonField, prop)
	} else {
		generateRangeCheckValue(buf, goField, jsonField, prop)
	}
}

// generateRangeCheckPointer generates range validation for pointer types.
func generateRangeCheckPointer(buf *bytes.Buffer, goField, jsonField string, prop *Property) {
	fmt.Fprintf(buf, "\tif m.%s != nil {\n", goField)
	hasMin := prop.Minimum != nil
	hasMax := prop.Maximum != nil
	if hasMin && hasMax {
		fmt.Fprintf(buf, "\t\tif *m.%s < %d || *m.%s > %d {\n",
			goField, int64(*prop.Minimum), goField, int64(*prop.Maximum))
		fmt.Fprintf(buf, "\t\t\treturn &ValidationError{Field: %q, Message: \"must be between %d and %d\"}\n",
			jsonField, int64(*prop.Minimum), int64(*prop.Maximum))
	} else if hasMin {
		fmt.Fprintf(buf, "\t\tif *m.%s < %d {\n", goField, int64(*prop.Minimum))
		fmt.Fprintf(buf, "\t\t\treturn &ValidationError{Field: %q, Message: \"must be >= %d\"}\n",
			jsonField, int64(*prop.Minimum))
	} else {
		fmt.Fprintf(buf, "\t\tif *m.%s > %d {\n", goField, int64(*prop.Maximum))
		fmt.Fprintf(buf, "\t\t\treturn &ValidationError{Field: %q, Message: \"must be <= %d\"}\n",
			jsonField, int64(*prop.Maximum))
	}
	buf.WriteString("\t\t}\n\t}\n\n")
}

// generateRangeCheckValue generates range validation for value types.
// For non-pointer integers, 0 means "not set" so we skip validation when value is 0.
func generateRangeCheckValue(buf *bytes.Buffer, goField, jsonField string, prop *Property) {
	hasMin := prop.Minimum != nil
	hasMax := prop.Maximum != nil
	if hasMin && hasMax {
		fmt.Fprintf(buf, "\tif m.%s != 0 && (m.%s < %d || m.%s > %d) {\n",
			goField, goField, int64(*prop.Minimum), goField, int64(*prop.Maximum))
		fmt.Fprintf(buf, "\t\treturn &ValidationError{Field: %q, Message: \"must be between %d and %d\"}\n",
			jsonField, int64(*prop.Minimum), int64(*prop.Maximum))
	} else if hasMin {
		fmt.Fprintf(buf, "\tif m.%s != 0 && m.%s < %d {\n", goField, goField, int64(*prop.Minimum))
		fmt.Fprintf(buf, "\t\treturn &ValidationError{Field: %q, Message: \"must be >= %d\"}\n",
			jsonField, int64(*prop.Minimum))
	} else {
		fmt.Fprintf(buf, "\tif m.%s != 0 && m.%s > %d {\n", goField, goField, int64(*prop.Maximum))
		fmt.Fprintf(buf, "\t\treturn &ValidationError{Field: %q, Message: \"must be <= %d\"}\n",
			jsonField, int64(*prop.Maximum))
	}
	buf.WriteString("\t}\n\n")
}
