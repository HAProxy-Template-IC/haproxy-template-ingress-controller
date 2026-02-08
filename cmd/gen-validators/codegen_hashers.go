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
	"sort"
	"strings"
)

// generateHasher generates a hash function for a schema.
func generateHasher(buf *bytes.Buffer, version, schemaName string, schema *ResolvedSchema) {
	funcName := fmt.Sprintf("Hash%s%s", goTypeName(schemaName), strings.ToUpper(version))
	modelType := goModelType(schemaName)

	fmt.Fprintf(buf, "// %s computes a content hash for cache lookup.\n", funcName)
	fmt.Fprintf(buf, "func %s(m *models.%s) uint64 {\n", funcName, modelType)
	buf.WriteString("\tif m == nil {\n\t\treturn 0\n\t}\n\n")
	buf.WriteString("\th := xxhash.New()\n\n")

	// Sort properties for deterministic hash order
	propNames := make([]string, 0, len(schema.Properties))
	for name := range schema.Properties {
		propNames = append(propNames, name)
	}
	sort.Strings(propNames)

	for _, propName := range propNames {
		prop := schema.Properties[propName]
		goField := goFieldName(propName)
		generateHashField(buf, goField, prop)
	}

	buf.WriteString("\treturn h.Sum64()\n}\n\n")
}

// generateHashField generates code to hash a field.
func generateHashField(buf *bytes.Buffer, goField string, prop *Property) {
	switch prop.Type {
	case schemaTypeString:
		if prop.IsPointer {
			fmt.Fprintf(buf, "\tif m.%s != nil {\n", goField)
			fmt.Fprintf(buf, "\t\t_, _ = h.WriteString(*m.%s)\n", goField)
			buf.WriteString("\t}\n")
		} else {
			fmt.Fprintf(buf, "\t_, _ = h.WriteString(m.%s)\n", goField)
		}
	case schemaTypeInteger, schemaTypeNumber:
		if prop.IsPointer {
			fmt.Fprintf(buf, "\tif m.%s != nil {\n", goField)
			fmt.Fprintf(buf, "\t\t_ = binary.Write(h, binary.LittleEndian, *m.%s)\n", goField)
			buf.WriteString("\t}\n")
		} else {
			fmt.Fprintf(buf, "\t_ = binary.Write(h, binary.LittleEndian, m.%s)\n", goField)
		}
	case schemaTypeBoolean:
		if prop.IsPointer {
			fmt.Fprintf(buf, "\tif m.%s != nil {\n", goField)
			fmt.Fprintf(buf, "\t\tif *m.%s {\n", goField)
			buf.WriteString("\t\t\t_, _ = h.Write([]byte{1})\n")
			buf.WriteString("\t\t} else {\n")
			buf.WriteString("\t\t\t_, _ = h.Write([]byte{0})\n")
			buf.WriteString("\t\t}\n")
			buf.WriteString("\t}\n")
		} else {
			fmt.Fprintf(buf, "\tif m.%s {\n", goField)
			buf.WriteString("\t\t_, _ = h.Write([]byte{1})\n")
			buf.WriteString("\t} else {\n")
			buf.WriteString("\t\t_, _ = h.Write([]byte{0})\n")
			buf.WriteString("\t}\n")
		}
	}
}
