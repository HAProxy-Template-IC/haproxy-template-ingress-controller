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

package dataplane

import (
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// haproxyCheckMutex serializes HAProxy validation to work around issues with
// concurrent haproxy -c execution. Without this, concurrent validations can
// interfere with each other even though they use isolated temp directories.
var haproxyCheckMutex sync.Mutex

// validateSemantics performs semantic validation using haproxy binary.
// This writes files to actual /etc/haproxy/ directories and runs haproxy -c.
// If skipDNSValidation is true, the -dr flag is passed to HAProxy to skip DNS resolution
// failures (servers with unresolvable hostnames start in DOWN state instead of failing).
func validateSemantics(mainConfig string, auxFiles *AuxiliaryFiles, paths *ValidationPaths, skipDNSValidation bool) error {
	// Timing for file I/O setup vs haproxy check
	var clearMs, writeAuxMs, writeConfigMs, haproxyCheckMs int64

	// Clear validation directories to remove any pre-existing files
	clearStart := time.Now()
	if err := clearValidationDirectories(paths); err != nil {
		return fmt.Errorf("failed to clear validation directories: %w", err)
	}
	clearMs = time.Since(clearStart).Milliseconds()

	// Write auxiliary files to their respective directories
	writeAuxStart := time.Now()
	if err := writeAuxiliaryFiles(auxFiles, paths); err != nil {
		return fmt.Errorf("failed to write auxiliary files: %w", err)
	}
	writeAuxMs = time.Since(writeAuxStart).Milliseconds()

	// Write main configuration to ConfigFile path
	writeConfigStart := time.Now()
	if err := os.WriteFile(paths.ConfigFile, []byte(mainConfig), 0o600); err != nil {
		return fmt.Errorf("failed to write config file: %w", err)
	}
	writeConfigMs = time.Since(writeConfigStart).Milliseconds()

	// Run haproxy -c -f <ConfigFile>
	haproxyCheckStart := time.Now()
	if err := runHAProxyCheck(paths.ConfigFile, mainConfig, skipDNSValidation); err != nil {
		return err
	}
	haproxyCheckMs = time.Since(haproxyCheckStart).Milliseconds()

	// Log semantic validation timing breakdown
	slog.Debug("semantic validation timing breakdown",
		"clear_dirs_ms", clearMs,
		"write_aux_ms", writeAuxMs,
		"write_config_ms", writeConfigMs,
		"haproxy_check_ms", haproxyCheckMs,
	)

	return nil
}

// clearValidationDirectories removes all files from validation directories.
// This ensures no pre-existing files interfere with validation.
// It clears both the traditional validation directories (for absolute/simple paths)
// and subdirectories in the config directory (for relative paths with subdirectories).
func clearValidationDirectories(paths *ValidationPaths) error {
	configDir := filepath.Dir(paths.ConfigFile)

	// Clear traditional validation directories (for absolute paths and simple filenames)
	dirs := []string{
		paths.MapsDir,
		paths.SSLCertsDir,
		paths.GeneralStorageDir,
	}

	for _, dir := range dirs {
		if err := clearDirectory(dir); err != nil {
			return err
		}
	}

	// Create config directory if it doesn't exist
	// (No need to clear it - we already cleared the specific validation directories above)
	if err := os.MkdirAll(configDir, 0o750); err != nil {
		return fmt.Errorf("failed to create config directory %s: %w", configDir, err)
	}

	// Remove old config file if it exists
	if err := os.Remove(paths.ConfigFile); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to remove old config file: %w", err)
	}

	return nil
}

// clearDirectory creates a directory and removes all its contents.
// Uses retry logic to handle race conditions where the directory is deleted
// between MkdirAll and ReadDir (e.g., by concurrent cleanup).
func clearDirectory(dir string) error {
	var entries []os.DirEntry
	for attempt := 0; attempt < 2; attempt++ {
		if err := os.MkdirAll(dir, 0o750); err != nil {
			return fmt.Errorf("failed to create directory %s: %w", dir, err)
		}

		var err error
		entries, err = os.ReadDir(dir)
		if err != nil {
			if os.IsNotExist(err) && attempt == 0 {
				// Directory was deleted between MkdirAll and ReadDir
				// (race with concurrent cleanup), retry once
				continue
			}
			return fmt.Errorf("failed to read directory %s: %w", dir, err)
		}
		break // Success
	}

	for _, entry := range entries {
		path := filepath.Join(dir, entry.Name())
		if err := os.RemoveAll(path); err != nil {
			return fmt.Errorf("failed to remove %s: %w", path, err)
		}
	}

	return nil
}

// resolveAuxiliaryFilePath determines the full path for an auxiliary file.
// It handles three cases:
// - Absolute paths: Extract filename and use fallback directory (for validation with temp directories).
// - Relative paths with subdirectories (e.g., "maps/hosts.map"): resolved relative to config directory.
// - Simple filenames: written to the specified fallback directory.
func resolveAuxiliaryFilePath(filePath, configDir, fallbackDir string) string {
	if filepath.IsAbs(filePath) {
		// Absolute path - extract filename and use fallback directory
		// This allows validation to work with temp directories instead of production paths
		// Example: /etc/haproxy/ssl/cert.pem → <tmpdir>/ssl/cert.pem
		return filepath.Join(fallbackDir, filepath.Base(filePath))
	}

	if strings.Contains(filePath, string(filepath.Separator)) {
		// Relative path with subdirectory - resolve relative to config directory
		return filepath.Join(configDir, filePath)
	}

	// Just a filename - write to fallback directory
	return filepath.Join(fallbackDir, filePath)
}

// writeFileWithDir writes a file to disk, creating parent directories if needed.
func writeFileWithDir(path, content, fileType string) error {
	// Ensure parent directory exists
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return fmt.Errorf("failed to create directory for %s: %w", fileType, err)
	}

	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		return fmt.Errorf("failed to write %s: %w", fileType, err)
	}

	return nil
}

// writeAuxiliaryFiles writes all auxiliary files to their respective directories.
func writeAuxiliaryFiles(auxFiles *AuxiliaryFiles, paths *ValidationPaths) error {
	if auxFiles == nil {
		return nil // No auxiliary files to write
	}

	configDir := filepath.Dir(paths.ConfigFile)

	// Write map files
	for _, mapFile := range auxFiles.MapFiles {
		mapPath := resolveAuxiliaryFilePath(mapFile.Path, configDir, paths.MapsDir)
		if err := writeFileWithDir(mapPath, mapFile.Content, "map file "+mapFile.Path); err != nil {
			return err
		}
	}

	// Write general files
	// Use file.Path when set (contains full relative path like "ssl/ca-bundle.pem" for
	// CA files). Fall back to file.Filename for backward compatibility with code that
	// only sets Filename.
	for _, file := range auxFiles.GeneralFiles {
		pathToUse := file.Path
		if pathToUse == "" {
			pathToUse = file.Filename
		}
		filePath := resolveAuxiliaryFilePath(pathToUse, configDir, paths.GeneralStorageDir)
		if err := writeFileWithDir(filePath, file.Content, "general file "+pathToUse); err != nil {
			return err
		}
	}

	// Write SSL certificates
	for _, cert := range auxFiles.SSLCertificates {
		certPath := resolveAuxiliaryFilePath(cert.Path, configDir, paths.SSLCertsDir)
		if err := writeFileWithDir(certPath, cert.Content, "SSL certificate "+cert.Path); err != nil {
			return err
		}
	}

	// Write SSL CA files (stored in same directory as SSL certificates)
	for _, caFile := range auxFiles.SSLCaFiles {
		caPath := resolveAuxiliaryFilePath(caFile.Path, configDir, paths.SSLCertsDir)
		if err := writeFileWithDir(caPath, caFile.Content, "SSL CA file "+caFile.Path); err != nil {
			return err
		}
	}

	// Write CRT-list files
	// Use CRTListDir which may differ from SSLCertsDir on HAProxy < 3.2
	for _, crtList := range auxFiles.CRTListFiles {
		crtListPath := resolveAuxiliaryFilePath(crtList.Path, configDir, paths.CRTListDir)
		if err := writeFileWithDir(crtListPath, crtList.Content, "CRT-list file "+crtList.Path); err != nil {
			return err
		}
	}

	return nil
}

// runHAProxyCheck runs haproxy binary with -c flag to validate configuration.
// The configuration can reference auxiliary files using relative paths
// (e.g., maps/host.map) which will be resolved relative to the config file directory.
//
// If skipDNSValidation is true, the -dr flag is passed to HAProxy. This causes HAProxy
// to append "none" to all server resolution methods, allowing startup/validation to
// proceed even when DNS resolution fails. Servers with unresolvable hostnames will
// start in RMAINT (DOWN) state instead of causing validation failure.
func runHAProxyCheck(configPath, configContent string, skipDNSValidation bool) error {
	// Serialize HAProxy execution to work around concurrent execution issues
	haproxyCheckMutex.Lock()
	defer haproxyCheckMutex.Unlock()

	// Find haproxy binary
	haproxyBin, err := exec.LookPath("haproxy")
	if err != nil {
		return fmt.Errorf("haproxy binary not found: %w", err)
	}

	// Get absolute path for config file
	absConfigPath, err := filepath.Abs(configPath)
	if err != nil {
		return fmt.Errorf("failed to get absolute config path: %w", err)
	}

	// Build haproxy command arguments
	// -c: check configuration and exit
	// -f: path to configuration file
	// -dr: (optional) skip DNS resolution failures - servers start in DOWN state instead of failing
	var args []string
	if skipDNSValidation {
		args = []string{"-dr", "-c", "-f", filepath.Base(absConfigPath)}
	} else {
		args = []string{"-c", "-f", filepath.Base(absConfigPath)}
	}

	// Run haproxy with the constructed arguments
	// Set working directory to config file directory so relative paths work
	cmd := exec.Command(haproxyBin, args...)
	cmd.Dir = filepath.Dir(absConfigPath)

	// Capture both stdout and stderr
	output, err := cmd.CombinedOutput()
	if err != nil {
		// Parse and format HAProxy error output with config file context
		errorMsg := parseHAProxyError(string(output), configContent)
		return fmt.Errorf("haproxy validation failed: %s", errorMsg)
	}

	return nil
}

// parseHAProxyError parses HAProxy's error output to extract meaningful error messages with context.
// HAProxy outputs errors with [ALERT] prefix and line numbers. This function:
// 1. Captures 3 lines before/after each [ALERT] from HAProxy's output
// 2. Parses line numbers from [ALERT] messages (e.g., [haproxy.cfg:90])
// 3. Extracts and shows the corresponding lines from the config file.
func parseHAProxyError(output, configContent string) string {
	lines := strings.Split(output, "\n")

	// Find all meaningful [ALERT] line indices (skip summary alerts)
	alertIndices := findAlertIndices(lines)
	if len(alertIndices) == 0 {
		return strings.TrimSpace(output)
	}

	// Split config content into lines for context extraction
	configLines := strings.Split(configContent, "\n")

	// Extract context for each alert
	errorBlocks := extractErrorBlocks(lines, alertIndices, configLines, configContent)
	if len(errorBlocks) == 0 {
		return strings.TrimSpace(output)
	}

	// Join multiple error blocks with blank line separator
	return strings.Join(errorBlocks, "\n\n")
}

// findAlertIndices finds all meaningful [ALERT] line indices, skipping summary alerts.
func findAlertIndices(lines []string) []int {
	alertIndices := make([]int, 0, 5) // Pre-allocate for typical case of few alerts
	for i, line := range lines {
		if isRelevantAlert(line) {
			alertIndices = append(alertIndices, i)
		}
	}
	return alertIndices
}

// isRelevantAlert checks if a line contains a relevant alert (not a summary).
func isRelevantAlert(line string) bool {
	trimmed := strings.TrimSpace(line)
	if !strings.HasPrefix(trimmed, "[ALERT]") {
		return false
	}

	// Skip summary [ALERT] lines
	lineLower := strings.ToLower(trimmed)
	return !strings.Contains(lineLower, "fatal errors found in configuration") &&
		!strings.Contains(lineLower, "error(s) found in configuration file")
}

// extractErrorBlocks extracts error context blocks for each alert.
func extractErrorBlocks(lines []string, alertIndices []int, configLines []string, configContent string) []string {
	var errorBlocks []string
	for _, alertIdx := range alertIndices {
		block := buildErrorBlock(lines, alertIdx, configLines, configContent)
		if len(block) > 0 {
			errorBlocks = append(errorBlocks, strings.Join(block, "\n"))
		}
	}
	return errorBlocks
}

// buildErrorBlock builds a single error context block for an alert.
func buildErrorBlock(lines []string, alertIdx int, configLines []string, configContent string) []string {
	startIdx, endIdx := calculateContextRange(alertIdx, len(lines))

	var block []string
	var alertLine string

	// Build HAProxy output context
	for i := startIdx; i < endIdx; i++ {
		line := strings.TrimRight(lines[i], " \t\r\n")
		if shouldSkipLine(line) {
			continue
		}

		// Add arrow marker for the alert line
		if i == alertIdx {
			block = append(block, "→ "+line)
			alertLine = line
		} else {
			block = append(block, "  "+line)
		}
	}

	// Add config context if available
	if alertLine != "" && configContent != "" {
		if configContext := extractConfigContext(alertLine, configLines); configContext != "" {
			block = append(block, "", "  Config context:", configContext)
		}
	}

	return block
}

// calculateContextRange calculates the start and end indices for context lines (3 before/after).
func calculateContextRange(alertIdx, totalLines int) (start, end int) {
	start = alertIdx - 3
	if start < 0 {
		start = 0
	}

	end = alertIdx + 4 // +4 because we want 3 lines after (inclusive range)
	if end > totalLines {
		end = totalLines
	}

	return start, end
}

// shouldSkipLine checks if a line should be skipped (empty or summary line).
func shouldSkipLine(line string) bool {
	if line == "" {
		return true
	}

	lineLower := strings.ToLower(line)
	return strings.Contains(lineLower, "fatal errors found in configuration") ||
		strings.Contains(lineLower, "error(s) found in configuration file")
}

// extractConfigContext extracts configuration file context around an error line.
// It parses the line number from an [ALERT] message like "[haproxy.cfg:90]"
// and returns 3 lines before/after that line with line numbers and an arrow marker.
func extractConfigContext(alertLine string, configLines []string) string {
	// Parse line number from [ALERT] message
	// Format: [ALERT] ... : config : [haproxy.cfg:90] : ...
	// or: [ALERT] ... : [haproxy.cfg:90] : ...

	// Find [filename:linenum] pattern - look for second [ (after [ALERT])
	firstBracket := strings.Index(alertLine, "[")
	if firstBracket == -1 {
		return ""
	}

	// Look for second bracket after [ALERT]
	remaining := alertLine[firstBracket+1:]
	secondBracket := strings.Index(remaining, "[")
	if secondBracket == -1 {
		return ""
	}

	// Now parse the [filename:line] part
	fileLinePart := remaining[secondBracket+1:]
	colonIdx := strings.Index(fileLinePart, ":")
	if colonIdx == -1 {
		return ""
	}

	bracketClose := strings.Index(fileLinePart, "]")
	if bracketClose == -1 || bracketClose < colonIdx {
		return ""
	}

	// Extract line number part (after the colon, before the bracket)
	lineNumStr := fileLinePart[colonIdx+1 : bracketClose]
	lineNum := 0
	if _, err := fmt.Sscanf(lineNumStr, "%d", &lineNum); err != nil {
		return ""
	}

	// Convert to 0-based index
	errorLineIdx := lineNum - 1
	if errorLineIdx < 0 || errorLineIdx >= len(configLines) {
		return ""
	}

	// Calculate context range (3 lines before and after)
	startIdx := errorLineIdx - 3
	if startIdx < 0 {
		startIdx = 0
	}

	endIdx := errorLineIdx + 4 // +4 because we want 3 lines after
	if endIdx > len(configLines) {
		endIdx = len(configLines)
	}

	// Build context block with line numbers
	var contextLines []string
	for i := startIdx; i < endIdx; i++ {
		lineContent := configLines[i]
		lineNumber := i + 1

		var formatted string
		if i == errorLineIdx {
			// Error line - add arrow marker
			formatted = fmt.Sprintf("  %4d → %s", lineNumber, lineContent)
		} else {
			formatted = fmt.Sprintf("  %4d   %s", lineNumber, lineContent)
		}

		// Trim trailing spaces for cleaner output
		contextLines = append(contextLines, strings.TrimRight(formatted, " "))
	}

	return strings.Join(contextLines, "\n")
}
