package enterprise

import "gitlab.com/haproxy-haptic/haptic/pkg/dataplane/parser/sectionextract"

// extractCESections extracts standard (Community Edition) HAProxy sections from
// the client-native parser into conf.
//
// The extraction logic is shared verbatim with the CE parser
// (pkg/dataplane/parser) via the sectionextract package — both operate on the
// same config-parser interface, so the CE-section pass is identical and stays
// in lockstep. Enterprise-specific sections (waf-global, waf-profile, ...) and
// EE directives within CE sections are layered on separately by the caller; see
// extractConfiguration.
func (p *Parser) extractCESections(conf *StructuredConfig) error {
	return sectionextract.All(p.ceParser, conf)
}
