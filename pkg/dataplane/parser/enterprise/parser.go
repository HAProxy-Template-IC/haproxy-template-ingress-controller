// Package enterprise provides HAProxy Enterprise Edition configuration parsing.
//
// This package implements a hybrid parser for HAProxy EE configurations that:
// 1. Uses client-native's parser for complete CE section extraction (global, defaults, frontends, backends, etc.)
// 2. Uses a custom line-by-line reader for EE-specific sections (waf-global, waf-profile, captcha, etc.)
// 3. Uses wrapper parsers to intercept EE directives in CE sections (filter waf, http-request waf-evaluate)
//
// This hybrid approach maximizes code reuse from client-native while adding EE support.
//
// Usage:
//
//	// Choose parser based on HAProxy version
//	if isEnterpriseEdition(haproxyVersion) {
//	    parser := enterprise.NewParser()
//	    config, err := parser.ParseFromString(configString)
//	} else {
//	    parser, _ := parser.New()
//	    config, err := parser.ParseFromString(configString)
//	}
package enterprise

import (
	"fmt"
	"log/slog"
	"strings"
	"sync"

	clientparser "github.com/haproxytech/client-native/v6/config-parser"
	"github.com/haproxytech/client-native/v6/models"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/parser/parserconfig"
)

// parserMutex protects against concurrent parsing.
// The enterprise parser shares some client-native parsers which have global state.
var parserMutex sync.Mutex

// Parser is the HAProxy Enterprise Edition configuration parser.
// It uses a hybrid approach:
// 1. client-native parser for complete CE section extraction (global, defaults, frontends, backends, etc.)
// 2. Custom EE reader for EE-specific sections (waf-global, waf-profile, captcha, etc.)
// 3. Custom EE reader for EE directives within CE sections (filter waf, http-request waf-evaluate, etc.)
type Parser struct {
	factory  ParserFactory
	ceParser clientparser.Parser
}

// NewParser creates a new Enterprise Edition parser.
// Returns an error if the underlying client-native parser cannot be initialized.
func NewParser() (*Parser, error) {
	ceParser, err := clientparser.New()
	if err != nil {
		return nil, fmt.Errorf("failed to create client-native parser: %w", err)
	}

	return &Parser{
		factory:  NewDefaultFactory(),
		ceParser: ceParser,
	}, nil
}

// StructuredConfig is the canonical configuration type.
// This is a type alias to parserconfig.StructuredConfig for convenience.
type StructuredConfig = parserconfig.StructuredConfig

// EEFrontendData is a type alias for parserconfig.EEFrontendData.
type EEFrontendData = parserconfig.EEFrontendData

// EEBackendData is a type alias for parserconfig.EEBackendData.
type EEBackendData = parserconfig.EEBackendData

// ParseFromString parses an HAProxy EE configuration string into a structured representation.
//
// This uses a hybrid approach:
// 1. client-native parser processes CE sections (complete field extraction)
// 2. EE reader processes EE-specific sections (waf-global, waf-profile, etc.)
// 3. EE reader captures EE directives within CE sections (filter waf, http-request waf-evaluate).
func (p *Parser) ParseFromString(config string) (*StructuredConfig, error) {
	if config == "" {
		return nil, fmt.Errorf("configuration string is empty")
	}

	parserMutex.Lock()
	defer parserMutex.Unlock()

	// Step 1: Parse with client-native parser for complete CE section extraction
	// This handles all CE directives with full field extraction
	if err := p.ceParser.Process(strings.NewReader(config)); err != nil {
		// Log but continue - EE configs may contain directives client-native doesn't understand
		slog.Debug("client-native parser warning (continuing with EE parser)",
			"error", err)
	}

	// Step 2: Parse with EE reader for EE-specific sections and directives
	reader := NewReader(p.factory)
	if err := reader.ProcessString(config); err != nil {
		return nil, fmt.Errorf("failed to parse EE configuration: %w", err)
	}

	// Step 3: Extract structured configuration from both parsers
	return p.extractConfiguration(reader.GetParsers())
}

// extractConfiguration builds a StructuredConfig from parsed data.
// It combines CE sections from client-native parser with EE sections from EE reader.
func (p *Parser) extractConfiguration(parsed *ConfiguredParsers) (*StructuredConfig, error) {
	conf := &StructuredConfig{
		EEFrontends: make(map[string]*parserconfig.EEFrontendData),
		EEBackends:  make(map[string]*parserconfig.EEBackendData),
		// Initialize pointer-based indexes for zero-copy iteration
		ServerIndex:         make(map[string]map[string]*models.Server),
		ServerTemplateIndex: make(map[string]map[string]*models.ServerTemplate),
		BindIndex:           make(map[string]map[string]*models.Bind),
		PeerEntryIndex:      make(map[string]map[string]*models.PeerEntry),
		NameserverIndex:     make(map[string]map[string]*models.Nameserver),
		MailerEntryIndex:    make(map[string]map[string]*models.MailerEntry),
		UserIndex:           make(map[string]map[string]*models.User),
		GroupIndex:          make(map[string]map[string]*models.Group),
	}

	// Extract CE sections from client-native parser (complete field extraction)
	if err := p.extractCESections(conf); err != nil {
		return nil, fmt.Errorf("failed to extract CE sections: %w", err)
	}

	// Extract EE standalone sections from EE reader
	p.extractEEStandaloneSections(parsed, conf)

	// Extract EE directives from CE sections (filter waf, http-request waf-evaluate, etc.)
	// These are captured by wrapper parsers in the EE reader
	p.extractEEDirectivesFromCESections(parsed, conf)

	return conf, nil
}
