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
// This generator reads OpenAPI specs from pkg/generated/dataplaneapi/v{30,31,32}/spec.json
// and produces Go validation functions that work directly on client-native structs,
// avoiding the ~25GB allocation overhead of JSON marshal/unmarshal cycles.
//
// Usage:
//
//	go run ./cmd/gen-validators
//
// Or via make:
//
//	make generate-validators
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"go/format"
	"os"
	"path/filepath"
	"sort"
	"strings"
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

// versionDirs maps version strings to their spec directories.
var versionDirs = map[string]string{
	"v30": "pkg/generated/dataplaneapi/v30",
	"v31": "pkg/generated/dataplaneapi/v31",
	"v32": "pkg/generated/dataplaneapi/v32",
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

	for version, dir := range versionDirs {
		specPath := filepath.Join(projectRoot, dir, "spec.json")
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

	// Generate output files
	outputDir := filepath.Join(projectRoot, "pkg", "dataplane", "validators")

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
			return "", fmt.Errorf("go.mod not found")
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
		return nil, fmt.Errorf("failed to read spec: %w", err)
	}

	var spec OpenAPISpec
	if err := json.Unmarshal(data, &spec); err != nil {
		return nil, fmt.Errorf("failed to parse spec: %w", err)
	}

	return &spec, nil
}

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

// writeFormattedFile writes Go code to a file, formatting it first.
func writeFormattedFile(path string, data []byte) error {
	formatted, err := format.Source(data)
	if err != nil {
		// Write unformatted for debugging
		_ = os.WriteFile(path+".unformatted", data, 0o600)
		return fmt.Errorf("failed to format %s: %w (unformatted written to %s.unformatted)", path, err, path)
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
		h := xxhash64([]byte(pattern))
		return fmt.Sprintf("pattern_%x", h&0xFFFF)
	}
}

// xxhash64 computes a simple hash for pattern naming.
func xxhash64(data []byte) uint64 {
	var h uint64 = 0xcbf29ce484222325
	for _, b := range data {
		h ^= uint64(b)
		h *= 0x100000001b3
	}
	return h
}

// goTypeName converts a schema name to a Go type name.
func goTypeName(schemaName string) string {
	parts := strings.Split(schemaName, "_")
	var result strings.Builder
	for _, part := range parts {
		if part != "" {
			result.WriteString(strings.ToUpper(part[:1]))
			result.WriteString(part[1:])
		}
	}
	return result.String()
}

// goModelType returns the client-native model type name.
func goModelType(schemaName string) string {
	switch schemaName {
	case "server":
		return "Server"
	case "server_template":
		return "ServerTemplate"
	case "bind":
		return "Bind"
	case "http_request_rule":
		return "HTTPRequestRule"
	case "http_response_rule":
		return "HTTPResponseRule"
	case "tcp_request_rule":
		return "TCPRequestRule"
	case "tcp_response_rule":
		return "TCPResponseRule"
	case "http_after_response_rule":
		return "HTTPAfterResponseRule"
	case "http_error_rule":
		return "HTTPErrorRule"
	case "server_switching_rule":
		return "ServerSwitchingRule"
	case "backend_switching_rule":
		return "BackendSwitchingRule"
	case "stick_rule":
		return "StickRule"
	case "acl":
		return "ACL"
	case "filter":
		return "Filter"
	case "log_target":
		return "LogTarget"
	case "http_check":
		return "HTTPCheck"
	case "tcp_check":
		return "TCPCheck"
	case "capture":
		return "Capture"
	default:
		return goTypeName(schemaName)
	}
}

// goFieldName converts a JSON field name to a Go field name.
// This mapping must match the field names in github.com/haproxytech/client-native/v6/models.
func goFieldName(jsonName string) string {
	// Handle common mappings
	fieldMappings := map[string]string{
		"name":                   "Name",
		"address":                "Address",
		"port":                   "Port",
		"id":                     "ID",
		"agent-addr":             "AgentAddr",
		"agent-check":            "AgentCheck",
		"agent-inter":            "AgentInter",
		"agent-port":             "AgentPort",
		"agent-send":             "AgentSend",
		"allow_0rtt":             "Allow0rtt",
		"alpn":                   "Alpn",
		"backup":                 "Backup",
		"check":                  "Check",
		"check-pool-conn-name":   "CheckPoolConnName",
		"check-reuse-pool":       "CheckReusePool",
		"check-send-proxy":       "CheckSendProxy",
		"check-sni":              "CheckSni",
		"check-ssl":              "CheckSsl",
		"check_alpn":             "CheckAlpn",
		"check_proto":            "CheckProto",
		"check_via_socks4":       "CheckViaSocks4",
		"ciphers":                "Ciphers",
		"ciphersuites":           "Ciphersuites",
		"client_sigalgs":         "ClientSigalgs",
		"cookie":                 "Cookie",
		"crl_file":               "CrlFile",
		"curves":                 "Curves",
		"downinter":              "Downinter",
		"error_limit":            "ErrorLimit",
		"fall":                   "Fall",
		"fastinter":              "Fastinter",
		"force_sslv3":            "ForceSslv3",
		"force_tlsv10":           "ForceTlsv10",
		"force_tlsv11":           "ForceTlsv11",
		"force_tlsv12":           "ForceTlsv12",
		"force_tlsv13":           "ForceTlsv13",
		"guid":                   "GUID",
		"guid_prefix":            "GUIDPrefix", // Note: GUIDPrefix not GuidPrefix
		"hash_key":               "HashKey",
		"health_check_address":   "HealthCheckAddress",
		"health_check_port":      "HealthCheckPort",
		"idle_ping":              "IdlePing",
		"init-addr":              "InitAddr",
		"init-state":             "InitState",
		"inter":                  "Inter",
		"log-bufsize":            "LogBufsize",
		"log_proto":              "LogProto",
		"maintenance":            "Maintenance",
		"max_reuse":              "MaxReuse",
		"maxconn":                "Maxconn",
		"maxqueue":               "Maxqueue",
		"minconn":                "Minconn",
		"namespace":              "Namespace",
		"no_sslv3":               "NoSslv3",
		"no_tls_tickets":         "NoTLSTickets", // Note: TLS not Tls
		"no_tlsv10":              "NoTlsv10",
		"no_tlsv11":              "NoTlsv11",
		"no_tlsv12":              "NoTlsv12",
		"no_tlsv13":              "NoTlsv13",
		"no_verifyhost":          "NoVerifyhost",
		"npn":                    "Npn",
		"observe":                "Observe",
		"on-error":               "OnError",
		"on-marked-down":         "OnMarkedDown",
		"on-marked-up":           "OnMarkedUp",
		"pool_conn_name":         "PoolConnName",
		"pool_low_conn":          "PoolLowConn",
		"pool_max_conn":          "PoolMaxConn",
		"pool_purge_delay":       "PoolPurgeDelay",
		"proto":                  "Proto",
		"proxy-v2-options":       "ProxyV2Options",
		"redir":                  "Redir",
		"resolve-net":            "ResolveNet",
		"resolve-prefer":         "ResolvePrefer",
		"resolve_opts":           "ResolveOpts",
		"tcp_user_timeout":       "TCPUserTimeout", // Note: TCP not Tcp
		"tls_ticket_keys":        "TLSTicketKeys",  // Note: TLS not Tls
		"tls_tickets":            "TLSTickets",     // Note: TLS not Tls
		"uid":                    "UID",            // Note: UID not Uid
		"acl_file":               "ACLFile",        // Note: ACL not Acl
		"acl_keyfmt":             "ACLKeyfmt",      // Note: ACL not Acl
		"sc_id":                  "ScID",
		"sc_idx":                 "ScIdx",
		"sc_inc_id":              "ScIncID",
		"sc_int":                 "ScInt",
		"sc_expr":                "ScExpr",
		"uri-fmt":                "URIFmt",
		"uri-match":              "URIMatch",
		"uri":                    "URI",
		"uri_log_format":         "URILogFormat",
		"rst_ttl":                "RstTTL",
		"resolvers":              "Resolvers",
		"rise":                   "Rise",
		"send-proxy":             "SendProxy",
		"send-proxy-v2":          "SendProxyV2",
		"send_proxy_v2_ssl":      "SendProxyV2Ssl",
		"send_proxy_v2_ssl_cn":   "SendProxyV2SslCn",
		"set-proxy-v2-tlv-fmt":   "SetProxyV2TlvFmt",
		"shard":                  "Shard",
		"sigalgs":                "Sigalgs",
		"slowstart":              "Slowstart",
		"sni":                    "Sni",
		"socks4":                 "Socks4",
		"source":                 "Source",
		"ssl":                    "Ssl",
		"ssl_cafile":             "SslCafile",
		"ssl_certificate":        "SslCertificate",
		"ssl_max_ver":            "SslMaxVer",
		"ssl_min_ver":            "SslMinVer",
		"ssl_reuse":              "SslReuse",
		"sslv3":                  "Sslv3",
		"stick":                  "Stick",
		"strict-maxconn":         "StrictMaxconn",
		"tcp_ut":                 "TCPUt",
		"tfo":                    "Tfo",
		"tlsv10":                 "Tlsv10",
		"tlsv11":                 "Tlsv11",
		"tlsv12":                 "Tlsv12",
		"tlsv13":                 "Tlsv13",
		"track":                  "Track",
		"verify":                 "Verify",
		"verifyhost":             "Verifyhost",
		"weight":                 "Weight",
		"ws":                     "Ws",
		"acl_name":               "ACLName",
		"criterion":              "Criterion",
		"value":                  "Value",
		"prefix":                 "Prefix",
		"num_or_range":           "NumOrRange",
		"fqdn":                   "Fqdn",
		"target_server":          "TargetServer",
		"cond":                   "Cond",
		"cond_test":              "CondTest",
		"type":                   "Type",
		"index":                  "Index",
		"auth_realm":             "AuthRealm",
		"bandwidth_limit_limit":  "BandwidthLimitLimit",
		"bandwidth_limit_name":   "BandwidthLimitName",
		"bandwidth_limit_period": "BandwidthLimitPeriod",
		"cache_name":             "CacheName",
		"capture_id":             "CaptureID",
		"capture_len":            "CaptureLen",
		"capture_sample":         "CaptureSample",
		"deny_status":            "DenyStatus",
		"expr":                   "Expr",
		"hdr_format":             "HdrFormat",
		"hdr_match":              "HdrMatch",
		"hdr_name":               "HdrName",
		"hint_format":            "HintFormat",
		"hint_name":              "HintName",
		"log_level":              "LogLevel",
		"lua_action":             "LuaAction",
		"lua_params":             "LuaParams",
		"map_file":               "MapFile",
		"map_keyfmt":             "MapKeyfmt",
		"map_valuefmt":           "MapValuefmt",
		"mark_value":             "MarkValue",
		"method_fmt":             "MethodFmt",
		"nice_value":             "NiceValue",
		"normalizer":             "Normalizer",
		"normalizer_full":        "NormalizerFull",
		"normalizer_strict":      "NormalizerStrict",
		"path_fmt":               "PathFmt",
		"path_match":             "PathMatch",
		"protocol":               "Protocol",
		"query_fmt":              "QueryFmt",
		"redir_code":             "RedirCode",
		"redir_option":           "RedirOption",
		"redir_type":             "RedirType",
		"redir_value":            "RedirValue",
		"resolvers_id":           "ResolversID",
		"return_content":         "ReturnContent",
		"return_content_format":  "ReturnContentFormat",
		"return_content_type":    "ReturnContentType",
		"return_hdrs":            "ReturnHdrs",
		"return_status_code":     "ReturnStatusCode",
		"service_name":           "ServiceName",
		"spoe_engine":            "SpoeEngine",
		"spoe_group":             "SpoeGroup",
		"status":                 "Status",
		"status_reason":          "StatusReason",
		"strict_mode":            "StrictMode",
		"timeout":                "Timeout",
		"timeout_type":           "TimeoutType",
		"tos_value":              "TosValue",
		"track_sc0_key":          "TrackSc0Key",
		"track_sc1_key":          "TrackSc1Key",
		"track_sc2_key":          "TrackSc2Key",
		"track_sc_key":           "TrackScKey",
		"track_sc_stick_counter": "TrackScStickCounter",
		"track_sc_table":         "TrackScTable",
		"var_expr":               "VarExpr",
		"var_format":             "VarFormat",
		"var_name":               "VarName",
		"var_scope":              "VarScope",
		"wait_at_least":          "WaitAtLeast",
		"wait_time":              "WaitTime",
	}

	if mapped, ok := fieldMappings[jsonName]; ok {
		return mapped
	}

	// Default: convert to PascalCase
	parts := strings.FieldsFunc(jsonName, func(r rune) bool {
		return r == '-' || r == '_'
	})
	var result strings.Builder
	for _, part := range parts {
		if part != "" {
			result.WriteString(strings.ToUpper(part[:1]))
			result.WriteString(part[1:])
		}
	}
	return result.String()
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
