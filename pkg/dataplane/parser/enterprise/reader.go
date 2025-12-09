package enterprise

import (
	"bufio"
	"io"
	"strings"

	parser "github.com/haproxytech/client-native/v6/config-parser"
)

// Reader parses HAProxy configuration files line by line.
// It handles both Community Edition (CE) and Enterprise Edition (EE) sections.
//
// The reader implements a state machine that tracks the current section
// and routes directives to the appropriate parser collection.
type Reader struct {
	factory ParserFactory
	parsers *ConfiguredParsers

	// preComments holds comments accumulated before a directive
	preComments []string
}

// NewReader creates a new configuration reader.
func NewReader(factory ParserFactory) *Reader {
	return &Reader{
		factory: factory,
		parsers: NewConfiguredParsers(),
	}
}

// GetParsers returns the configured parsers after processing.
func (r *Reader) GetParsers() *ConfiguredParsers {
	return r.parsers
}

// Process reads and parses a HAProxy configuration from an io.Reader.
// It processes the configuration line by line, routing each line to
// the appropriate section parsers.
func (r *Reader) Process(input io.Reader) error {
	scanner := bufio.NewScanner(input)

	for scanner.Scan() {
		line := scanner.Text()
		r.processLine(line)
	}

	return scanner.Err()
}

// ProcessString reads and parses a HAProxy configuration from a string.
func (r *Reader) ProcessString(config string) error {
	return r.Process(strings.NewReader(config))
}

// processLine processes a single line of configuration.
func (r *Reader) processLine(line string) {
	// Trim leading/trailing whitespace for analysis, but keep original for parsing
	trimmedLine := strings.TrimSpace(line)

	// Empty line - skip
	if trimmedLine == "" {
		return
	}

	// Comment line - accumulate for next directive
	if strings.HasPrefix(trimmedLine, "#") {
		r.preComments = append(r.preComments, trimmedLine)
		return
	}

	// Tokenize the line
	parts, comment := tokenizeLine(trimmedLine)
	if len(parts) == 0 {
		return
	}

	// Check if this is a section header
	if IsAnySectionString(parts[0]) {
		r.handleSectionHeader(parts, comment)
		return
	}

	// Parse as directive in current section
	r.parseDirective(line, parts, comment)
}

// handleSectionHeader processes a section header line.
func (r *Reader) handleSectionHeader(parts []string, _ string) {
	section := Section(parts[0])
	name := ""
	if len(parts) > 1 {
		name = parts[1]
	}

	// Get or create parser collection for this section
	parsers := r.parsers.GetSectionParsers(section, name, r.factory)

	// Update state
	r.parsers.SetState(section, name, parsers)

	// Clear pre-comments (they belong to this section header)
	r.preComments = nil
}

// parseDirective parses a directive in the current section.
func (r *Reader) parseDirective(line string, parts []string, comment string) {
	// No active section - ignore (shouldn't happen with valid config)
	if r.parsers.Active == nil {
		return
	}

	// Look up parser for this directive
	directiveName := parts[0]
	p, ok := r.parsers.Active.Parsers[directiveName]

	// If no specific parser, try the UnProcessed fallback
	// Note: UnProcessed is registered under empty string ""
	// (because extra.UnProcessed.GetParserName() returns "")
	if !ok {
		p, ok = r.parsers.Active.Parsers[""]
	}

	if !ok || p == nil {
		// No parser available - directive will be lost
		r.preComments = nil
		return
	}

	// Parse the directive
	_, _ = p.PreParse(line, parts, r.preComments, comment)

	// Clear pre-comments after use
	r.preComments = nil
}

// tokenizeLine splits a line into parts and extracts the comment.
// Handles quoted strings properly.
func tokenizeLine(line string) (parts []string, comment string) {
	// Find inline comment
	commentIndex := strings.Index(line, "#")
	mainPart := line
	if commentIndex != -1 {
		// Make sure the # is not inside quotes
		if !isInQuotes(line, commentIndex) {
			mainPart = strings.TrimSpace(line[:commentIndex])
			comment = strings.TrimSpace(line[commentIndex+1:])
		}
	}

	// Split main part into fields, respecting quotes
	parts = splitFields(mainPart)
	return parts, comment
}

// isInQuotes checks if position is inside quoted string.
func isInQuotes(line string, pos int) bool {
	inQuotes := false
	quoteChar := byte(0)

	for i := 0; i < pos && i < len(line); i++ {
		c := line[i]
		if !inQuotes && (c == '"' || c == '\'') {
			inQuotes = true
			quoteChar = c
		} else if inQuotes && c == quoteChar {
			// Check for escaped quote
			if i > 0 && line[i-1] != '\\' {
				inQuotes = false
			}
		}
	}

	return inQuotes
}

// splitFields splits a string into fields, respecting quoted strings.
func splitFields(s string) []string {
	var fields []string
	var current strings.Builder
	inQuotes := false
	quoteChar := byte(0)

	for i := 0; i < len(s); i++ {
		c := s[i]

		switch {
		case !inQuotes && (c == '"' || c == '\''):
			// Start quoted string
			inQuotes = true
			quoteChar = c
			current.WriteByte(c)

		case inQuotes && c == quoteChar:
			// End quoted string (check for escape)
			if i > 0 && s[i-1] != '\\' {
				inQuotes = false
			}
			current.WriteByte(c)

		case !inQuotes && (c == ' ' || c == '\t'):
			// Whitespace outside quotes - end field
			if current.Len() > 0 {
				fields = append(fields, current.String())
				current.Reset()
			}

		default:
			current.WriteByte(c)
		}
	}

	// Add final field
	if current.Len() > 0 {
		fields = append(fields, current.String())
	}

	return fields
}

// ExtractSection extracts all parsed data for a section.
// This is used to convert parsed results into the structured config.
func (r *Reader) ExtractSection(section Section, name string) map[string]interface{} {
	var parsers *parser.Parsers

	switch section {
	case SectionGlobal:
		parsers = r.parsers.Global
	case SectionDefaults:
		parsers = r.parsers.Defaults
	case SectionFrontend:
		parsers = r.parsers.Frontend[name]
	case SectionBackend:
		parsers = r.parsers.Backend[name]
	case SectionListen:
		parsers = r.parsers.Listen[name]
	case SectionWAFGlobal:
		parsers = r.parsers.WAFGlobal
	case SectionWAFProfile:
		parsers = r.parsers.WAFProfile[name]
	case SectionBotMgmtProfile:
		parsers = r.parsers.BotMgmtProfile[name]
	case SectionCaptcha:
		parsers = r.parsers.Captcha[name]
	case SectionUDPLB:
		parsers = r.parsers.UDPLB[name]
	case SectionDynamicUpdate:
		parsers = r.parsers.DynamicUpdate[name]
	default:
		return nil
	}

	if parsers == nil {
		return nil
	}

	result := make(map[string]interface{})
	for parserName, p := range parsers.Parsers {
		if data, err := p.Get(false); err == nil && data != nil {
			result[parserName] = data
		}
	}

	return result
}
