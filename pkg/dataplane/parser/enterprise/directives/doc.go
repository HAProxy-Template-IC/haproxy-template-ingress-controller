// Package types provides parser implementations for HAProxy Enterprise Edition
// section-specific directives.
//
// Each file in this package implements ParserInterface for directives within
// specific EE sections (udp-lb, waf-global, waf-profile, botmgmt-profile, captcha).
//
// These parsers are used by the enterprise.Parser to extract directive values
// from EE sections, populating the corresponding API model fields.
package directives
