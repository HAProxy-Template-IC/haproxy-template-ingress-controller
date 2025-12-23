// Package enterprise provides HAProxy Enterprise Edition configuration parsing.
//
// This file contains extraction helpers for EE section field values.
// Directives are parsed from the UnProcessed parser and mapped to struct fields.
package enterprise

import (
	"strconv"
	"strings"

	clientparser "github.com/haproxytech/client-native/v6/config-parser"
	"github.com/haproxytech/client-native/v6/config-parser/parsers/extra"
	cptypes "github.com/haproxytech/client-native/v6/config-parser/types"

	v32ee "haptic/pkg/generated/dataplaneapi/v32ee"
)

// =============================================================================
// Directive Parsing Helpers
// =============================================================================

// getUnprocessedLines retrieves all unprocessed directive lines from a parser collection.
func getUnprocessedLines(parsers *clientparser.Parsers) []string {
	if parsers == nil || parsers.Parsers == nil {
		return nil
	}

	// UnProcessed parser is registered under empty string ""
	// (because extra.UnProcessed.GetParserName() returns "")
	parser, ok := parsers.Parsers[""]
	if !ok {
		return nil
	}

	unprocessed, ok := parser.(*extra.UnProcessed)
	if !ok {
		return nil
	}

	data, err := unprocessed.Get(false)
	if err != nil || data == nil {
		return nil
	}

	items, ok := data.([]cptypes.UnProcessed)
	if !ok {
		return nil
	}

	lines := make([]string, len(items))
	for i, item := range items {
		lines[i] = item.Value
	}
	return lines
}

// parseDirective parses a directive line into keyword and values.
// Strips any trailing comment (after #).
func parseDirective(line string) (keyword string, values []string) {
	// Handle inline comments
	commentIdx := strings.Index(line, "#")
	mainPart := line
	if commentIdx != -1 && !isInQuotedString(line, commentIdx) {
		mainPart = strings.TrimSpace(line[:commentIdx])
	}

	// Split into fields
	fields := splitQuotedFields(mainPart)
	if len(fields) == 0 {
		return "", nil
	}

	keyword = fields[0]
	if len(fields) > 1 {
		values = fields[1:]
	}
	return keyword, values
}

// isInQuotedString checks if position is inside a quoted string.
func isInQuotedString(s string, pos int) bool {
	inQuotes := false
	quoteChar := byte(0)
	for i := 0; i < pos && i < len(s); i++ {
		c := s[i]
		if !inQuotes && (c == '"' || c == '\'') {
			inQuotes = true
			quoteChar = c
		} else if inQuotes && c == quoteChar && (i == 0 || s[i-1] != '\\') {
			inQuotes = false
		}
	}
	return inQuotes
}

// splitQuotedFields splits a string into fields, preserving quoted strings.
func splitQuotedFields(s string) []string {
	var fields []string
	var current strings.Builder
	inQuotes := false
	quoteChar := byte(0)

	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case !inQuotes && (c == '"' || c == '\''):
			inQuotes = true
			quoteChar = c
		case inQuotes && c == quoteChar && (i == 0 || s[i-1] != '\\'):
			inQuotes = false
		case !inQuotes && (c == ' ' || c == '\t'):
			if current.Len() > 0 {
				fields = append(fields, current.String())
				current.Reset()
			}
		default:
			current.WriteByte(c)
		}
	}
	if current.Len() > 0 {
		fields = append(fields, current.String())
	}
	return fields
}

// parseInt parses an integer value, returning nil on error.
func parseInt(s string) *int {
	v, err := strconv.Atoi(s)
	if err != nil {
		return nil
	}
	return &v
}

// parseBool parses a boolean value.
func parseBool(s string) *bool {
	s = strings.ToLower(s)
	switch s {
	case "true", "enabled", "on", "1":
		v := true
		return &v
	case "false", "disabled", "off", "0":
		v := false
		return &v
	}
	return nil
}

// =============================================================================
// WAF Global Extraction
// =============================================================================

// extractWAFGlobalFields populates WAF global fields from unprocessed directives.
// Returns nil if no meaningful data was extracted.
func extractWAFGlobalFields(wafParsers *clientparser.Parsers) *v32ee.WafGlobal {
	if wafParsers == nil {
		return nil
	}

	wafGlobal := &v32ee.WafGlobal{}
	hasData := false
	lines := getUnprocessedLines(wafParsers)

	for _, line := range lines {
		keyword, values := parseDirective(line)
		if len(values) == 0 {
			continue
		}

		switch keyword {
		case "rules-file":
			wafGlobal.RulesFile = &values[0]
			hasData = true
		case "rules-path":
			wafGlobal.RulesPath = &values[0]
			hasData = true
		case "body-limit":
			wafGlobal.BodyLimit = parseInt(values[0])
			hasData = true
		case "json-levels":
			wafGlobal.JsonLevels = parseInt(values[0])
			hasData = true
		case "analyzer-cache":
			wafGlobal.AnalyzerCache = parseInt(values[0])
			hasData = true
		case "log-host-header-len":
			wafGlobal.LogHostHeaderLen = parseInt(values[0])
			hasData = true
		}
	}

	if !hasData {
		return nil
	}
	return wafGlobal
}

// =============================================================================
// WAF Profile Extraction
// =============================================================================

// extractWAFProfileFields populates WAF profile fields from unprocessed directives.
// Always returns a profile with the name set since named sections always exist.
func extractWAFProfileFields(name string, wafParsers *clientparser.Parsers) *v32ee.WafProfile {
	if wafParsers == nil {
		return nil
	}

	profile := &v32ee.WafProfile{
		Name: name,
	}
	lines := getUnprocessedLines(wafParsers)

	for _, line := range lines {
		keyword, values := parseDirective(line)
		if len(values) == 0 {
			continue
		}

		switch keyword {
		case "body-limit":
			profile.BodyLimit = parseInt(values[0])
		case "learning-mode", "learning":
			profile.LearningMode = parseBool(values[0])
		case "analyze":
			if len(values) > 0 {
				analyze := v32ee.WafProfileAnalyze(values[0])
				profile.Analyze = &analyze
			}
		case "analyze-acl":
			profile.AnalyzeAcl = &values[0]
		}
	}

	// Named sections always return a profile (name is meaningful data)
	return profile
}

// =============================================================================
// Bot Management Profile Extraction
// =============================================================================

// extractBotMgmtProfileFields populates bot management profile fields.
func extractBotMgmtProfileFields(name string, botParsers *clientparser.Parsers) *v32ee.BotmgmtProfile {
	if botParsers == nil {
		return nil
	}

	profile := &v32ee.BotmgmtProfile{
		Name: name,
	}
	lines := getUnprocessedLines(botParsers)

	for _, line := range lines {
		keyword, values := parseDirective(line)
		if len(values) == 0 {
			continue
		}

		switch keyword {
		case "score-version":
			profile.ScoreVersion = parseInt(values[0])
		case "track":
			if len(values) > 0 {
				track := v32ee.BotmgmtProfileTrack(values[0])
				profile.Track = &track
			}
		case "track-peers":
			profile.TrackPeers = &values[0]
		case "track-defaults":
			// Parse track-defaults subfields if present in same line
			// Format: track-defaults size <n> expire <n> period <n>
			profile.TrackDefaults = parseTrackDefaults(values)
		}
	}

	return profile
}

// parseTrackDefaults parses track-defaults directive values.
func parseTrackDefaults(values []string) *v32ee.BotmgmtTrackDefaults {
	defaults := &v32ee.BotmgmtTrackDefaults{}
	hasValues := false

	for i := 0; i < len(values)-1; i += 2 {
		key := values[i]
		val := values[i+1]
		switch key {
		case "size":
			defaults.Size = parseInt(val)
			hasValues = true
		case "expire":
			defaults.Expire = parseInt(val)
			hasValues = true
		case "period":
			defaults.Period = parseInt(val)
			hasValues = true
		}
	}

	if !hasValues {
		return nil
	}
	return defaults
}

// =============================================================================
// Captcha Extraction
// =============================================================================

// extractCaptchaFields populates captcha fields from unprocessed directives.
func extractCaptchaFields(name string, captchaParsers *clientparser.Parsers) *v32ee.Captcha {
	if captchaParsers == nil {
		return nil
	}

	captcha := &v32ee.Captcha{
		Name: name,
	}
	lines := getUnprocessedLines(captchaParsers)

	for _, line := range lines {
		keyword, values := parseDirective(line)
		if len(values) == 0 {
			continue
		}

		switch keyword {
		case "provider", "mode":
			captcha.Mode = &values[0]
		case "public-key", "site-key":
			captcha.SiteKey = &values[0]
		case "secret-key":
			captcha.SecretKey = &values[0]
		case "api-key":
			captcha.ApiKey = &values[0]
		case "html-file", "cust-html-file":
			captcha.CustHtmlFile = &values[0]
		case "cookie-domain":
			captcha.CookieDomain = &values[0]
		case "cookie-path":
			captcha.CookiePath = &values[0]
		case "cookie-expires":
			captcha.CookieExpires = &values[0]
		case "cookie-max-age":
			captcha.CookieMaxAge = parseInt(values[0])
		case "cookie-secure":
			if len(values) > 0 {
				secure := v32ee.CaptchaCookieSecure(values[0])
				captcha.CookieSecure = &secure
			}
		case "cookie-samesite":
			if len(values) > 0 {
				samesite := v32ee.CaptchaCookieSamesite(values[0])
				captcha.CookieSamesite = &samesite
			}
		}
	}

	return captcha
}

// =============================================================================
// UDP LB Extraction
// =============================================================================

// extractUDPLBFields populates UDP LB fields from parser data.
func extractUDPLBFields(name string, udpParsers *clientparser.Parsers) *v32ee.UdpLbBase {
	if udpParsers == nil {
		return nil
	}

	udplb := &v32ee.UdpLbBase{
		Name: name,
	}

	// Extract balance from dedicated parser if available
	if parser, ok := udpParsers.Parsers["balance"]; ok {
		if data, err := parser.Get(false); err == nil && data != nil {
			if balance, ok := data.(*cptypes.Balance); ok && balance != nil {
				udplb.Balance = &v32ee.Balance{
					Algorithm: v32ee.BalanceAlgorithm(balance.Algorithm),
				}
			}
		}
	}

	// Extract remaining fields from unprocessed
	lines := getUnprocessedLines(udpParsers)
	for _, line := range lines {
		keyword, values := parseDirective(line)
		if len(values) == 0 {
			continue
		}

		switch keyword {
		case "proxy-requests":
			udplb.ProxyRequests = parseInt(values[0])
		case "proxy-responses":
			udplb.ProxyResponses = parseInt(values[0])
		case "client-timeout":
			udplb.ClientTimeout = parseInt(values[0])
		case "server-timeout":
			udplb.ServerTimeout = parseInt(values[0])
		case "maxconn":
			udplb.Maxconn = parseInt(values[0])
		case "accepted-payload-size":
			udplb.AcceptedPayloadSize = parseInt(values[0])
		}
	}

	return udplb
}
