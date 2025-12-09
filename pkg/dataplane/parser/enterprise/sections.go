// Package enterprise provides HAProxy Enterprise Edition configuration parsing.
//
// HAProxy EE extends CE in three dimensions:
// 1. New sections (udp-lb, waf-global, waf-profile, botmgmt-profile, captcha, dynamic-update)
// 2. New directives in existing sections (maxmind-load in global, filter waf in frontend)
// 3. New actions in existing directives (http-request waf-evaluate, botmgmt-evaluate)
//
// This package provides a custom parser that handles EE-specific sections and wraps
// client-native parsers to intercept EE directives that client-native doesn't support.
package enterprise

// Section represents a HAProxy configuration section type.
// Follows the same pattern as github.com/haproxytech/client-native/v6/config-parser.Section.
type Section string

// Enterprise Edition section types.
// These sections are not supported by client-native's parser.
const (
	// SectionUDPLB is UDP load balancing section (named), syntax: udp-lb <name>.
	SectionUDPLB Section = "udp-lb"

	// SectionWAFGlobal is WAF global configuration section (singleton), syntax: waf-global.
	SectionWAFGlobal Section = "waf-global"

	// SectionWAFProfile is WAF profile definition section (named), syntax: waf-profile <name>.
	SectionWAFProfile Section = "waf-profile"

	// SectionBotMgmtProfile is bot management profile section (named), syntax: botmgmt-profile <name>.
	SectionBotMgmtProfile Section = "botmgmt-profile"

	// SectionCaptcha is CAPTCHA provider configuration section (named), syntax: captcha <name>.
	SectionCaptcha Section = "captcha"

	// SectionDynamicUpdate is dynamic update configuration section (named), syntax: dynamic-update <name>.
	SectionDynamicUpdate Section = "dynamic-update"
)

// Standard Community Edition section types.
// These are handled by client-native but listed here for completeness.
const (
	// SectionGlobal is the global configuration section (singleton).
	SectionGlobal Section = "global"

	// SectionDefaults is the defaults section (singleton or named).
	SectionDefaults Section = "defaults"

	// SectionFrontend is a frontend section (named).
	SectionFrontend Section = "frontend"

	// SectionBackend is a backend section (named).
	SectionBackend Section = "backend"

	// SectionListen is a listen section (combined frontend+backend, named).
	SectionListen Section = "listen"

	// SectionResolvers is a DNS resolvers section (named).
	SectionResolvers Section = "resolvers"

	// SectionPeers is a peers section for stick-table replication (named).
	SectionPeers Section = "peers"

	// SectionMailers is a mailers section for email alerts (named).
	SectionMailers Section = "mailers"

	// SectionCache is a cache section (named).
	SectionCache Section = "cache"

	// SectionProgram is a program section for external programs (named).
	SectionProgram Section = "program"

	// SectionHTTPErrors is an http-errors section (named).
	SectionHTTPErrors Section = "http-errors"

	// SectionRing is a ring buffer section (named).
	SectionRing Section = "ring"

	// SectionLogForward is a log forwarding section (named).
	SectionLogForward Section = "log-forward"

	// SectionFCGIApp is a FastCGI application section (named).
	SectionFCGIApp Section = "fcgi-app"

	// SectionCrtStore is a certificate store section (named).
	SectionCrtStore Section = "crt-store"

	// SectionTraces is a traces section for OpenTelemetry (named).
	SectionTraces Section = "traces"

	// SectionLogProfile is a log profile section (named).
	SectionLogProfile Section = "log-profile"

	// SectionACME is an ACME section for Let's Encrypt (named).
	SectionACME Section = "acme"

	// SectionUserlist is a userlist section for authentication (named).
	SectionUserlist Section = "userlist"

	// SectionComments represents comment lines.
	SectionComments Section = "#"
)

// eeSections contains all Enterprise Edition section types.
var eeSections = map[Section]bool{
	SectionUDPLB:          true,
	SectionWAFGlobal:      true,
	SectionWAFProfile:     true,
	SectionBotMgmtProfile: true,
	SectionCaptcha:        true,
	SectionDynamicUpdate:  true,
}

// ceSections contains all Community Edition section types.
var ceSections = map[Section]bool{
	SectionGlobal:     true,
	SectionDefaults:   true,
	SectionFrontend:   true,
	SectionBackend:    true,
	SectionListen:     true,
	SectionResolvers:  true,
	SectionPeers:      true,
	SectionMailers:    true,
	SectionCache:      true,
	SectionProgram:    true,
	SectionHTTPErrors: true,
	SectionRing:       true,
	SectionLogForward: true,
	SectionFCGIApp:    true,
	SectionCrtStore:   true,
	SectionTraces:     true,
	SectionLogProfile: true,
	SectionACME:       true,
	SectionUserlist:   true,
}

// singletonSections contains sections that can only appear once.
// Note: While HAProxy technically supports named defaults (defaults <name>),
// we treat defaults as a singleton for simplicity.
var singletonSections = map[Section]bool{
	SectionGlobal:    true,
	SectionDefaults:  true,
	SectionWAFGlobal: true,
}

// IsEESection returns true if the section type is an Enterprise Edition section.
func IsEESection(section Section) bool {
	return eeSections[section]
}

// IsEESectionString returns true if the section string is an Enterprise Edition section.
func IsEESectionString(section string) bool {
	return eeSections[Section(section)]
}

// IsCESection returns true if the section type is a Community Edition section.
func IsCESection(section Section) bool {
	return ceSections[section]
}

// IsCESectionString returns true if the section string is a Community Edition section.
func IsCESectionString(section string) bool {
	return ceSections[Section(section)]
}

// IsAnySection returns true if the section type is any valid HAProxy section.
func IsAnySection(section Section) bool {
	return eeSections[section] || ceSections[section]
}

// IsAnySectionString returns true if the section string is any valid HAProxy section.
func IsAnySectionString(section string) bool {
	s := Section(section)
	return eeSections[s] || ceSections[s]
}

// IsSingletonSection returns true if the section can only appear once.
func IsSingletonSection(section Section) bool {
	return singletonSections[section]
}

// IsSingletonSectionString returns true if the section string can only appear once.
func IsSingletonSectionString(section string) bool {
	return singletonSections[Section(section)]
}

// IsNamedSection returns true if the section requires a name.
func IsNamedSection(section Section) bool {
	// All non-singleton sections require names (except comments)
	if section == SectionComments {
		return false
	}
	return !singletonSections[section]
}

// IsNamedSectionString returns true if the section string requires a name.
func IsNamedSectionString(section string) bool {
	return IsNamedSection(Section(section))
}

// GetAllEESections returns all Enterprise Edition section types.
func GetAllEESections() []Section {
	return []Section{
		SectionUDPLB,
		SectionWAFGlobal,
		SectionWAFProfile,
		SectionBotMgmtProfile,
		SectionCaptcha,
		SectionDynamicUpdate,
	}
}

// GetAllCESections returns all Community Edition section types.
func GetAllCESections() []Section {
	return []Section{
		SectionGlobal,
		SectionDefaults,
		SectionFrontend,
		SectionBackend,
		SectionListen,
		SectionResolvers,
		SectionPeers,
		SectionMailers,
		SectionCache,
		SectionProgram,
		SectionHTTPErrors,
		SectionRing,
		SectionLogForward,
		SectionFCGIApp,
		SectionCrtStore,
		SectionTraces,
		SectionLogProfile,
		SectionACME,
		SectionUserlist,
	}
}

// GetAllSections returns all HAProxy section types (CE + EE).
func GetAllSections() []Section {
	return append(GetAllCESections(), GetAllEESections()...)
}

// SectionInfo contains metadata about a section type.
type SectionInfo struct {
	// Section is the section type.
	Section Section

	// IsEnterprise indicates if this is an EE-only section.
	IsEnterprise bool

	// IsSingleton indicates if only one instance can exist.
	IsSingleton bool

	// RequiresName indicates if the section requires a name argument.
	RequiresName bool

	// Description is a human-readable description.
	Description string
}

// sectionDescriptions maps section types to human-readable descriptions.
var sectionDescriptions = map[Section]string{
	// EE sections
	SectionUDPLB:          "UDP load balancing",
	SectionWAFGlobal:      "WAF global configuration",
	SectionWAFProfile:     "WAF profile definition",
	SectionBotMgmtProfile: "Bot management profile",
	SectionCaptcha:        "CAPTCHA provider configuration",
	SectionDynamicUpdate:  "Dynamic update configuration",
	// CE sections
	SectionGlobal:     "Global process-wide settings",
	SectionDefaults:   "Default settings for frontends/backends",
	SectionFrontend:   "Client-facing proxy configuration",
	SectionBackend:    "Server pool configuration",
	SectionListen:     "Combined frontend and backend",
	SectionResolvers:  "DNS resolver configuration",
	SectionPeers:      "Stick-table peer replication",
	SectionMailers:    "Email alert configuration",
	SectionCache:      "HTTP cache configuration",
	SectionProgram:    "External program definition",
	SectionHTTPErrors: "Custom HTTP error responses",
	SectionRing:       "Ring buffer for logging",
	SectionLogForward: "Log forwarding configuration",
	SectionFCGIApp:    "FastCGI application",
	SectionCrtStore:   "Certificate store",
	SectionTraces:     "OpenTelemetry tracing",
	SectionLogProfile: "Log formatting profile",
	SectionACME:       "ACME/Let's Encrypt configuration",
	SectionUserlist:   "User authentication list",
}

// GetSectionInfo returns metadata about a section type.
func GetSectionInfo(section Section) SectionInfo {
	desc, ok := sectionDescriptions[section]
	if !ok {
		desc = "Unknown section type"
	}

	return SectionInfo{
		Section:      section,
		IsEnterprise: IsEESection(section),
		IsSingleton:  IsSingletonSection(section),
		RequiresName: IsNamedSection(section),
		Description:  desc,
	}
}
