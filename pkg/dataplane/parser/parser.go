//go:build playground

// Package parser provides HAProxy configuration parsing using client-native library.
//
// This package wraps the haproxytech/client-native parser to parse HAProxy
// configurations from strings (in-memory, no disk I/O) into structured representations
// suitable for comparison and API operations.
//
// Semantic validation (checking resource availability, directive compatibility, etc.)
// is NOT performed here - that is handled by the external haproxy binary in later stages.
package parser

import (
	"errors"
	"fmt"
	"strings"
	"sync"

	parser "github.com/haproxytech/client-native/v6/config-parser"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/parser/parserconfig"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/parser/sectionextract"
)

// parserMutex protects against concurrent calls to the client-native parser.
//
// WORKAROUND: The upstream client-native library has a package-level global variable
// (config-parser/parser.go:65 "var DefaultSectionName") that is written during parsing
// without synchronization. This causes data races when multiple parsers are used concurrently.
//
// This mutex serializes all parsing operations to prevent the race condition.
// See: https://github.com/haproxytech/client-native/blob/v6.2.5/config-parser/parser.go#L65
//
// PERFORMANCE IMPACT: This mutex serializes ALL parsing operations across the entire
// controller, including concurrent webhook validations and reconciliations. In high-load
// scenarios with many concurrent validations, this can become a bottleneck.
//
// STATUS (checked 2025-12-06): Issue still exists in client-native v6.2.5. The global
// variable has a //nolint:gochecknoglobals comment indicating awareness but no fix.
// Consider checking for updates in newer versions or filing an upstream issue.
var parserMutex sync.Mutex

// Parser wraps client-native's config-parser for parsing HAProxy configurations.
type Parser struct {
	parser parser.Parser
}

// StructuredConfig is a type alias for types.StructuredConfig.
// This alias is provided for backward compatibility with existing code.
// New code should import from haptic/pkg/dataplane/parser/parserconfig.
type StructuredConfig = parserconfig.StructuredConfig

// New creates a new Parser instance.
//
// The parser uses client-native's config-parser which provides robust parsing
// of HAProxy configuration syntax without requiring file I/O.
func New() (*Parser, error) {
	p, err := parser.New()
	if err != nil {
		return nil, fmt.Errorf("creating parser: %w", err)
	}
	return &Parser{
		parser: p,
	}, nil
}

// ParseFromString parses a HAProxy configuration string into a structured representation.
//
// The configuration string should contain valid HAProxy configuration syntax.
// Returns a StructuredConfig containing all parsed sections (global, defaults,
// frontends, backends, etc.) suitable for comparison and synchronization.
//
// Syntax validation is performed as part of parsing - any syntax errors will be returned.
// Semantic validation (resource availability, directive compatibility) is performed
// by HAProxy via the Dataplane API during configuration application.
//
// Example:
//
//	config := `
//	global
//	    daemon
//	defaults
//	    mode http
//	backend web
//	    balance roundrobin
//	    server srv1 192.168.1.10:80
//	`
//	// p is a *Parser; the variable name avoids shadowing the imported
//	// `parser` package so subsequent parser.X calls (e.g. types) keep working.
//	p, _ := parser.New()
//	structured, err := p.ParseFromString(config)
func (p *Parser) ParseFromString(config string) (*StructuredConfig, error) {
	return p.parse(config)
}

func (p *Parser) parse(config string) (*StructuredConfig, error) {
	if config == "" {
		return nil, errors.New("configuration string is empty")
	}

	// Lock to prevent concurrent access to client-native parser
	// (protects against upstream race condition in DefaultSectionName global variable)
	parserMutex.Lock()
	defer parserMutex.Unlock()

	// Parse directly from string - NO file I/O
	// This keeps all config data in memory as required
	// Syntax validation happens automatically during parsing
	//
	// Defensive recover: client-native v6.3.5 has a known panic in
	// `parsers.ConfigSnippet.Parse` (nil-pointer deref when a comment
	// containing "config-snippet" reaches the parser without a preceding
	// `##_config-snippet_### BEGIN` marker — both the `commentParts[1]`
	// and `p.data.Value` accesses skip the prerequisite checks). We catch
	// it here so the controller surfaces a clean parse error instead of
	// crashing the whole pod, which used to take ~30s to recover and
	// snowballed into cascading test failures under heavy churn.
	if err := func() (err error) {
		defer func() {
			if r := recover(); r != nil {
				err = fmt.Errorf("client-native parser panicked: %v", r)
			}
		}()
		return p.parser.Process(strings.NewReader(config))
	}(); err != nil {
		return nil, fmt.Errorf("parsing configuration: %w", err)
	}

	// Extract structured configuration from parser
	conf, err := p.extractConfiguration()
	if err != nil {
		return nil, fmt.Errorf("extracting configuration: %w", err)
	}

	// Callers compare and mutate the returned config, so hand back a
	// normalized shape rather than the parser's raw metadata encoding.
	NormalizeConfigMetadata(conf)

	return conf, nil
}

// extractConfiguration builds a StructuredConfig from the parsed data.
//
// This reads all sections (global, defaults, frontends, backends, etc.)
// from the client-native parser and assembles them into a complete
// configuration structure.
//
// Note: This extracts the parsed structure but does NOT validate semantics.
// The config-parser only ensures syntax correctness.
func (p *Parser) extractConfiguration() (*StructuredConfig, error) {
	conf := parserconfig.NewStructuredConfig()

	// Section extraction lives in the sectionextract package, operating on the
	// config-parser interface.
	if err := sectionextract.All(p.parser, conf); err != nil {
		return nil, err
	}

	return conf, nil
}
