// Package parserconfig provides canonical configuration types for HAProxy parsing.
//
// This package defines StructuredConfig - the single source of truth for
// parsed HAProxy configurations. Both CE and EE parsers return this type.
package parserconfig

import (
	"github.com/haproxytech/client-native/v6/models"

	v32ee "gitlab.com/haproxy-haptic/haptic/pkg/generated/dataplaneapi/v32ee"
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

	// ==========================================================================
	// Pointer-based indexes for zero-copy iteration
	// ==========================================================================
	//
	// These indexes store pointers to nested elements, enabling zero-copy iteration
	// during comparison and validation. The upstream client-native library uses
	// value maps (map[string]T) which cause struct copies on every access.
	// By storing pointers, we avoid copying large structs (e.g., Server is 1504 bytes).
	//
	// These indexes are built during parsing and should be used by comparators
	// and validators instead of the value maps in the parent models.
	// The value maps in models (e.g., Backend.Servers) remain nil.

	// ServerIndex maps backend name -> server name -> server pointer
	ServerIndex map[string]map[string]*models.Server

	// ServerTemplateIndex maps backend name -> template prefix -> server template pointer
	ServerTemplateIndex map[string]map[string]*models.ServerTemplate

	// BindIndex maps frontend name -> bind name -> bind pointer
	BindIndex map[string]map[string]*models.Bind

	// PeerEntryIndex maps peer section name -> peer entry name -> peer entry pointer
	PeerEntryIndex map[string]map[string]*models.PeerEntry

	// NameserverIndex maps resolver name -> nameserver name -> nameserver pointer
	NameserverIndex map[string]map[string]*models.Nameserver

	// MailerEntryIndex maps mailers section name -> mailer entry name -> mailer entry pointer
	MailerEntryIndex map[string]map[string]*models.MailerEntry

	// UserIndex maps userlist name -> username -> user pointer
	UserIndex map[string]map[string]*models.User

	// GroupIndex maps userlist name -> group name -> group pointer
	GroupIndex map[string]map[string]*models.Group
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

// =============================================================================
// Index building helpers
// =============================================================================

// BuildUserIndex builds a pointer index from a slice of users.
// Returns nil if the input slice is nil.
func BuildUserIndex(users []*models.User) map[string]*models.User {
	if users == nil {
		return nil
	}
	index := make(map[string]*models.User, len(users))
	for _, user := range users {
		if user != nil && user.Username != "" {
			index[user.Username] = user
		}
	}
	return index
}

// BuildGroupIndex builds a pointer index from a slice of groups.
// Returns nil if the input slice is nil.
func BuildGroupIndex(groups []*models.Group) map[string]*models.Group {
	if groups == nil {
		return nil
	}
	index := make(map[string]*models.Group, len(groups))
	for _, group := range groups {
		if group != nil && group.Name != "" {
			index[group.Name] = group
		}
	}
	return index
}
