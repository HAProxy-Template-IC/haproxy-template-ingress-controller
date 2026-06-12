package templating

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// errorLocation represents the location of an error in a template.
type errorLocation struct {
	Line   int
	Column int
}

// parsedError represents a parsed template rendering error with structured information.
type parsedError struct {
	Location *errorLocation
	Problem  string
	Context  string
	Hints    []string
}

// Common error patterns in template errors.
var (
	// Pattern: "at line X: ... at Line=Y Col=Z".
	lineColPattern = regexp.MustCompile(`Line=(\d+)\s+Col=(\d+)`)

	// Pattern: "unable to execute template: ..." or "Unable to execute controlStructure at line X:".
	locationPattern = regexp.MustCompile(`at line (\d+)`)

	// Pattern for Scriggo compilation errors: "validation:1:5: expected '}'" or "template:3:10: syntax error".
	// Format: "name:line:col: message".
	scriggoCompilePattern = regexp.MustCompile(`:(\d+):(\d+):\s*(.*)$`)

	// Pattern: "unknown method 'X'".
	unknownMethodPattern = regexp.MustCompile(`unknown method '([^']+)'`)

	// Pattern: "undefined variable 'X'".
	undefinedVarPattern = regexp.MustCompile(`undefined variable '([^']+)'`)

	// Pattern: "invalid call to method 'X'".
	invalidCallPattern = regexp.MustCompile(`invalid call to method '([^']+)'`)

	// Pattern: "type mismatch" or "expected X, got Y".
	typeMismatchPattern = regexp.MustCompile(`expected (\w+), got (\w+)`)
)

func FormatCompilationError(err error, templateName, templateContent string) string {
	if err == nil {
		return ""
	}
	return formatParsedError(
		"Template Compilation Error: "+templateName,
		templateContent,
		err,
		parseCompilationError(err.Error()),
		formatLocationLineOptionalColumn,
	)
}

// formatParsedError builds the header / location / problem / template
// context / hints layout used by FormatCompilationError.
// formatLocation lets callers customise how a non-nil location renders.
func formatParsedError(header, templateContent string, originalErr error, parsed parsedError, formatLocation func(*errorLocation) string) string {
	var builder strings.Builder

	fmt.Fprintf(&builder, "%s\n", header)
	builder.WriteString(strings.Repeat("─", 60))
	builder.WriteString("\n")

	if parsed.Location != nil {
		builder.WriteString(formatLocation(parsed.Location))
	}

	if parsed.Problem != "" {
		fmt.Fprintf(&builder, "Problem:  %s\n", parsed.Problem)
	} else {
		problem := originalErr.Error()
		if len(problem) > 100 {
			problem = problem[:97] + "..."
		}
		fmt.Fprintf(&builder, "Problem:  %s\n", problem)
	}

	if parsed.Location != nil && templateContent != "" {
		if context := extractTemplateContext(templateContent, parsed.Location.Line, parsed.Location.Column); context != "" {
			builder.WriteString("\nTemplate Context:\n")
			builder.WriteString(context)
		}
	}

	if len(parsed.Hints) > 0 {
		builder.WriteString("\nHint: ")
		builder.WriteString(strings.Join(parsed.Hints, "\n      "))
		builder.WriteString("\n")
	}

	return builder.String()
}

func formatLocationLineOptionalColumn(l *errorLocation) string {
	if l.Column > 0 {
		return fmt.Sprintf("Location: Line %d, Column %d\n", l.Line, l.Column)
	}
	return fmt.Sprintf("Location: Line %d\n", l.Line)
}

// parseCompilationError parses a compilation error string to extract structured information.
// It handles Scriggo-style errors like "validation:1:5: expected '}'".
func parseCompilationError(errorStr string) parsedError {
	parsed := parsedError{}

	// Try Scriggo compile error pattern first: "validation:1:5: expected '}'"
	if matches := scriggoCompilePattern.FindStringSubmatch(errorStr); len(matches) == 4 {
		line, _ := strconv.Atoi(matches[1])
		col, _ := strconv.Atoi(matches[2])
		parsed.Location = &errorLocation{Line: line, Column: col}
		parsed.Problem = strings.TrimSpace(matches[3])
	} else {
		// Fallback to runtime error parsing
		parsed.Location = extractLocation(errorStr)
		parsed.Problem = extractProblem(errorStr)
	}

	// Generate hints based on error patterns
	parsed.Hints = generateCompilationHints(errorStr)

	return parsed
}

// generateCompilationHints generates actionable hints for compilation errors.
func generateCompilationHints(errorStr string) []string {
	var hints []string

	// Syntax error patterns
	if strings.Contains(errorStr, "expected") {
		if strings.Contains(errorStr, "expected '}'") || strings.Contains(errorStr, "expected '{'") {
			hints = append(hints,
				"Check for missing or mismatched braces in your template.",
				"Ensure {% %} blocks are properly closed with {% end %}.")
		} else if strings.Contains(errorStr, "expected '%}'") || strings.Contains(errorStr, "expected '}}'") {
			hints = append(hints,
				"Check for unclosed template tags.",
				"Ensure {{ }} and {% %} are properly closed.")
		} else {
			hints = append(hints,
				"The template syntax is incomplete or malformed.",
				"Check for missing operators, parentheses, or keywords.")
		}
	}

	// Unexpected token patterns
	if strings.Contains(errorStr, "unexpected") {
		hints = append(hints,
			"The template contains an unexpected token at this location.",
			"Check for typos or misplaced syntax elements.")
	}

	// Undefined identifier
	if strings.Contains(errorStr, "undefined") || strings.Contains(errorStr, "not declared") {
		hints = append(hints,
			"The variable or function is not defined.",
			"Check spelling and ensure it's declared in the template context.")
	}

	// Generic hint if no specific hint matched
	if len(hints) == 0 {
		hints = append(hints,
			"Check your template syntax for errors.",
			"See Scriggo template documentation for syntax help.")
	}

	return hints
}

func extractLocation(errorStr string) *errorLocation {
	// Try Line=X Col=Y pattern first (most specific)
	if matches := lineColPattern.FindStringSubmatch(errorStr); len(matches) == 3 {
		line, _ := strconv.Atoi(matches[1])
		col, _ := strconv.Atoi(matches[2])
		return &errorLocation{Line: line, Column: col}
	}

	// Fallback to "at line X" pattern
	if matches := locationPattern.FindStringSubmatch(errorStr); len(matches) == 2 {
		line, _ := strconv.Atoi(matches[1])
		return &errorLocation{Line: line, Column: 0}
	}

	return nil
}

// extractProblem extracts the core problem description from the error.
func extractProblem(errorStr string) string {
	// Try to find the most specific error message by working backwards
	// from nested error chains

	// Check for unknown method
	if matches := unknownMethodPattern.FindStringSubmatch(errorStr); len(matches) == 2 {
		methodName := matches[1]
		// Try to find what type it was called on
		if strings.Contains(errorStr, "invalid call to method") {
			return fmt.Sprintf("Unknown method '%s' - cannot call methods on this type", methodName)
		}
		return fmt.Sprintf("Unknown method '%s'", methodName)
	}

	// Check for undefined variable
	if matches := undefinedVarPattern.FindStringSubmatch(errorStr); len(matches) == 2 {
		return fmt.Sprintf("Undefined variable '%s'", matches[1])
	}

	// Check for invalid method call
	if matches := invalidCallPattern.FindStringSubmatch(errorStr); len(matches) == 2 {
		return fmt.Sprintf("Invalid method call '%s()' on this type", matches[1])
	}

	// Check for type mismatch
	if matches := typeMismatchPattern.FindStringSubmatch(errorStr); len(matches) == 3 {
		return fmt.Sprintf("Type mismatch: expected %s, got %s", matches[1], matches[2])
	}

	// Generic patterns
	if strings.Contains(errorStr, "unable to evaluate") {
		// Extract the part after "unable to evaluate"
		if _, after, ok := strings.Cut(errorStr, "unable to evaluate"); ok {
			rest := after
			// Find the next colon to get the expression
			if colonIdx := strings.Index(rest, ":"); colonIdx > 0 {
				expr := strings.TrimSpace(rest[:colonIdx])
				return fmt.Sprintf("Unable to evaluate expression: %s", expr)
			}
		}
	}

	return ""
}

func extractTemplateContext(templateContent string, line, column int) string {
	lines := strings.Split(templateContent, "\n")

	if line < 1 || line > len(lines) {
		return ""
	}

	var builder strings.Builder

	lineIndex := line - 1

	// Calculate the width needed for line numbers (for alignment)
	maxLineNum := min(line+1, len(lines))
	lineNumWidth := len(strconv.Itoa(maxLineNum))

	// Show line above (if it exists)
	if lineIndex > 0 {
		prevLine := lines[lineIndex-1]
		fmt.Fprintf(&builder, "%*d | %s\n", lineNumWidth, line-1, prevLine)
	}

	// Show the error line
	errorLine := lines[lineIndex]
	fmt.Fprintf(&builder, "%*d | %s\n", lineNumWidth, line, errorLine)

	// Add caret pointing to the column if we have it
	if column > 0 && column <= len(errorLine)+1 {
		// Calculate padding: line number width + " | " + spaces to column
		padding := lineNumWidth + 3 + column - 1
		builder.WriteString(strings.Repeat(" ", padding))
		builder.WriteString("^\n")
	}

	// Show line below (if it exists)
	if lineIndex < len(lines)-1 {
		nextLine := lines[lineIndex+1]
		fmt.Fprintf(&builder, "%*d | %s\n", lineNumWidth, line+1, nextLine)
	}

	return builder.String()
}
