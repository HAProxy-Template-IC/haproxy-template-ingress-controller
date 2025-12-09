// Package parserconfig provides canonical configuration types for HAProxy parsing.
//
// This package defines StructuredConfig - the single source of truth for
// parsed HAProxy configurations. Both CE and EE parsers return this type.
package parserconfig

import (
	"github.com/haproxytech/client-native/v6/models"

	v32ee "haproxy-template-ic/pkg/generated/dataplaneapi/v32ee"
)

// =============================================================================
// Enterprise Edition Constants
// =============================================================================

// EE filter type constants.
const (
	FilterTypeWAF     = "waf"
	FilterTypeBotMgmt = "botmgmt"
)

// EE http-request action type constants.
const (
	ActionWAFEvaluate     = "waf-evaluate"
	ActionBotMgmtEvaluate = "botmgmt-evaluate"
)

// EE global directive type constants.
const (
	DirectiveMaxmindLoad      = "maxmind-load"
	DirectiveMaxmindCacheSize = "maxmind-cache-size"
	DirectiveWAFLoad          = "waf-load"
)

// Common keyword constants.
const (
	KeywordProfile  = "profile"
	KeywordLearning = "learning"
	KeywordIf       = "if"
	KeywordUnless   = "unless"
)

// StructuredConfig holds all parsed configuration sections.
//
// This is the canonical configuration type used by both CE and EE parsers.
// CE parser populates standard fields, leaving EE fields nil.
// EE parser populates all fields including enterprise-specific ones.
//
// Usage:
//
//	// CE parsing
//	ceParser, _ := parser.New()
//	config, _ := ceParser.ParseFromString(configStr)
//	// config.WAFProfiles is nil, config.EEFrontends is nil
//
//	// EE parsing
//	eeParser := enterprise.NewParser()
//	config, _ := eeParser.ParseFromString(configStr)
//	// config.WAFProfiles and config.EEFrontends are populated
type StructuredConfig struct {
	// ==========================================================================
	// Standard HAProxy sections (CE and EE)
	// ==========================================================================

	Global      *models.Global
	Defaults    []*models.Defaults
	Frontends   []*models.Frontend
	Backends    []*models.Backend
	Peers       []*models.PeerSection
	Resolvers   []*models.Resolver
	Mailers     []*models.MailersSection
	Caches      []*models.Cache
	Rings       []*models.Ring
	HTTPErrors  []*models.HTTPErrorsSection
	Userlists   []*models.Userlist
	Programs    []*models.Program
	LogForwards []*models.LogForward
	FCGIApps    []*models.FCGIApp
	CrtStores   []*models.CrtStore

	// Observability sections (v3.1+ features)
	LogProfiles []*models.LogProfile // log-profile sections
	Traces      *models.Traces       // traces section (singleton)

	// Certificate automation (v3.2+ features)
	AcmeProviders []*models.AcmeProvider // acme sections for Let's Encrypt/ACME automation

	// ==========================================================================
	// Enterprise Edition standalone sections (EE only)
	// These are nil when parsed by CE parser.
	// ==========================================================================

	UDPLBs          []*v32ee.UdpLbBase      // udp-lb sections
	WAFGlobal       *v32ee.WafGlobal        // waf-global section (singleton)
	WAFProfiles     []*v32ee.WafProfile     // waf-profile sections
	BotMgmtProfiles []*v32ee.BotmgmtProfile // botmgmt-profile sections
	Captchas        []*v32ee.Captcha        // captcha sections

	// ==========================================================================
	// Enterprise Edition directives in CE sections (EE only)
	// These capture EE-specific directives within standard sections.
	// These are nil when parsed by CE parser.
	// ==========================================================================

	// EEFrontends maps frontend name to EE-specific directive data.
	// Contains filter waf/botmgmt, http-request waf-evaluate/botmgmt-evaluate, etc.
	EEFrontends map[string]*EEFrontendData

	// EEBackends maps backend name to EE-specific directive data.
	EEBackends map[string]*EEBackendData

	// EEGlobal holds EE-specific directives from the global section.
	// Contains maxmind-load, maxmind-cache-size, etc.
	EEGlobal *EEGlobalData
}

// =============================================================================
// Enterprise Edition directive data types
// =============================================================================

// EEFrontendData holds EE-specific directives parsed from a frontend section.
type EEFrontendData struct {
	Filters      []*EEFilter
	HTTPRequests []*EEHTTPRequestAction
}

// EEBackendData holds EE-specific directives parsed from a backend section.
type EEBackendData struct {
	Filters      []*EEFilter
	HTTPRequests []*EEHTTPRequestAction
}

// EEGlobalData holds EE-specific directives parsed from the global section.
type EEGlobalData struct {
	Directives []*EEGlobalDirective
}

// EEFilter represents an Enterprise Edition filter directive.
type EEFilter struct {
	// Type is the filter type: "waf" or "botmgmt"
	Type string

	// Name is the filter instance name (for WAF filters)
	Name string

	// Profile is the profile reference (for botmgmt filters)
	Profile string

	// RulesFile is the path to rules file (for WAF filters)
	RulesFile string

	// Learning enables learning mode for WAF filters
	Learning bool

	// LogEnabled enables logging for the filter
	LogEnabled bool

	// Comment is the inline comment
	Comment string
}

// EEHTTPRequestAction represents an Enterprise Edition http-request action.
type EEHTTPRequestAction struct {
	// Type is the action type: "waf-evaluate" or "botmgmt-evaluate"
	Type string

	// Profile is the profile name for the action
	Profile string

	// Cond is the condition type: "if" or "unless"
	Cond string

	// CondTest is the ACL condition expression
	CondTest string

	// Comment is the inline comment
	Comment string
}

// EEGlobalDirective represents an Enterprise Edition global directive.
type EEGlobalDirective struct {
	// Type is the directive type: "maxmind-load", "maxmind-cache-size", etc.
	Type string

	// Parts contains the directive arguments
	Parts []string

	// Comment is the inline comment
	Comment string
}
