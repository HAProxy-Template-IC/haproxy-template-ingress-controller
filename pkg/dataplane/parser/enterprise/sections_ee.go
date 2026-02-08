package enterprise

import (
	clientparser "github.com/haproxytech/client-native/v6/config-parser"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/parser/enterprise/parsers"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/parser/parserconfig"
	v32ee "gitlab.com/haproxy-haptic/haptic/pkg/generated/dataplaneapi/v32ee"
)

// extractEEStandaloneSections extracts EE standalone sections (udp-lb, waf-global, etc.).
func (p *Parser) extractEEStandaloneSections(parsed *ConfiguredParsers, conf *StructuredConfig) {
	// Extract WAF Global
	if parsed.WAFGlobal != nil {
		conf.WAFGlobal = p.extractWAFGlobal(parsed.WAFGlobal)
	}

	// Extract WAF Profiles
	for name, wafParsers := range parsed.WAFProfile {
		profile := p.extractWAFProfile(name, wafParsers)
		if profile != nil {
			conf.WAFProfiles = append(conf.WAFProfiles, profile)
		}
	}

	// Extract BotMgmt Profiles
	for name, botParsers := range parsed.BotMgmtProfile {
		profile := p.extractBotMgmtProfile(name, botParsers)
		if profile != nil {
			conf.BotMgmtProfiles = append(conf.BotMgmtProfiles, profile)
		}
	}

	// Extract Captchas
	for name, captchaParsers := range parsed.Captcha {
		captcha := p.extractCaptcha(name, captchaParsers)
		if captcha != nil {
			conf.Captchas = append(conf.Captchas, captcha)
		}
	}

	// Extract UDP LBs
	for name, udpParsers := range parsed.UDPLB {
		udplb := p.extractUDPLB(name, udpParsers)
		if udplb != nil {
			conf.UDPLBs = append(conf.UDPLBs, udplb)
		}
	}
}

// extractWAFGlobal extracts the waf-global section.
func (p *Parser) extractWAFGlobal(wafParsers *clientparser.Parsers) *v32ee.WafGlobal {
	return extractWAFGlobalFields(wafParsers)
}

// extractWAFProfile extracts a waf-profile section.
func (p *Parser) extractWAFProfile(name string, wafParsers *clientparser.Parsers) *v32ee.WafProfile {
	return extractWAFProfileFields(name, wafParsers)
}

// extractBotMgmtProfile extracts a botmgmt-profile section.
func (p *Parser) extractBotMgmtProfile(name string, botParsers *clientparser.Parsers) *v32ee.BotmgmtProfile {
	return extractBotMgmtProfileFields(name, botParsers)
}

// extractCaptcha extracts a captcha section.
func (p *Parser) extractCaptcha(name string, captchaParsers *clientparser.Parsers) *v32ee.Captcha {
	return extractCaptchaFields(name, captchaParsers)
}

// extractUDPLB extracts a udp-lb section.
func (p *Parser) extractUDPLB(name string, udpParsers *clientparser.Parsers) *v32ee.UdpLbBase {
	return extractUDPLBFields(name, udpParsers)
}

// extractEEDirectivesFromCESections extracts EE-specific directives from CE sections.
// This captures filter waf/botmgmt, http-request waf-evaluate/botmgmt-evaluate, etc.
func (p *Parser) extractEEDirectivesFromCESections(parsed *ConfiguredParsers, conf *StructuredConfig) {
	// Extract EE directives from global
	if parsed.Global != nil {
		if eeData := p.extractEEGlobalData(parsed.Global); eeData != nil {
			conf.EEGlobal = eeData
		}
	}

	// Extract EE directives from frontends
	for name, feParsers := range parsed.Frontend {
		if eeData := p.extractEEFrontendData(feParsers); eeData != nil {
			conf.EEFrontends[name] = eeData
		}
	}

	// Extract EE directives from backends
	for name, beParsers := range parsed.Backend {
		if eeData := p.extractEEBackendData(beParsers); eeData != nil {
			conf.EEBackends[name] = eeData
		}
	}
}

// extractEEGlobalData extracts EE-specific directives from global section.
func (p *Parser) extractEEGlobalData(globalParsers *clientparser.Parsers) *parserconfig.EEGlobalData {
	if globalParsers == nil || globalParsers.Parsers == nil {
		return nil
	}

	parser, ok := globalParsers.Parsers["global-ee"]
	if !ok {
		return nil
	}

	eeParser, ok := parser.(*parsers.GlobalEE)
	if !ok {
		return nil
	}

	directives := eeParser.GetDirectives()
	if len(directives) == 0 {
		return nil
	}

	return &parserconfig.EEGlobalData{
		Directives: convertEEGlobalDirectives(directives),
	}
}

// extractEEFrontendData extracts EE-specific directives from a frontend section.
func (p *Parser) extractEEFrontendData(feParsers *clientparser.Parsers) *EEFrontendData {
	if feParsers == nil || feParsers.Parsers == nil {
		return nil
	}

	data := &EEFrontendData{}

	// Extract EE filters
	if parser, ok := feParsers.Parsers["filter"]; ok {
		if filterParser, ok := parser.(*parsers.Filters); ok {
			data.Filters = filterParser.GetEEFilters()
		}
	}

	// Extract EE HTTP request actions
	if parser, ok := feParsers.Parsers["http-request"]; ok {
		if httpParser, ok := parser.(*parsers.HTTPRequests); ok {
			data.HTTPRequests = httpParser.GetEEActions()
		}
	}

	if len(data.Filters) == 0 && len(data.HTTPRequests) == 0 {
		return nil
	}

	return data
}

// extractEEBackendData extracts EE-specific directives from a backend section.
func (p *Parser) extractEEBackendData(beParsers *clientparser.Parsers) *EEBackendData {
	if beParsers == nil || beParsers.Parsers == nil {
		return nil
	}

	data := &EEBackendData{}

	// Extract EE filters
	if parser, ok := beParsers.Parsers["filter"]; ok {
		if filterParser, ok := parser.(*parsers.Filters); ok {
			data.Filters = filterParser.GetEEFilters()
		}
	}

	// Extract EE HTTP request actions
	if parser, ok := beParsers.Parsers["http-request"]; ok {
		if httpParser, ok := parser.(*parsers.HTTPRequests); ok {
			data.HTTPRequests = httpParser.GetEEActions()
		}
	}

	if len(data.Filters) == 0 && len(data.HTTPRequests) == 0 {
		return nil
	}

	return data
}

// convertEEGlobalDirectives converts parser directives to parserconfig directives.
// Since both are type aliases to parserconfig.EEGlobalDirective, this is a direct return.
func convertEEGlobalDirectives(directives []*parsers.EEGlobalDirective) []*parserconfig.EEGlobalDirective {
	// Both types are aliases to parserconfig.EEGlobalDirective, so they're identical
	return directives
}
