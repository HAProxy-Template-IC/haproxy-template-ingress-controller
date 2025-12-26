// Package parsers provides wrapper parsers for HAProxy Enterprise Edition directives.
//
// These wrappers intercept EE-specific directives that client-native doesn't support,
// while delegating standard CE directives to client-native parsers.
package parsers

import (
	"strings"

	"github.com/haproxytech/client-native/v6/config-parser/common"
	"github.com/haproxytech/client-native/v6/config-parser/errors"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/parser/parserconfig"
)

// EEGlobalDirective is a type alias for types.EEGlobalDirective.
type EEGlobalDirective = parserconfig.EEGlobalDirective

// GlobalEE parses EE-specific global directives.
// It captures directives that client-native doesn't support.
type GlobalEE struct {
	// directives holds parsed EE-specific directives
	directives []*EEGlobalDirective

	// preComments holds accumulated pre-comments
	preComments []string
}

// NewGlobalEE creates a new GlobalEE parser.
func NewGlobalEE() *GlobalEE {
	return &GlobalEE{
		directives: nil,
	}
}

// Init initializes the parser.
func (p *GlobalEE) Init() {
	p.directives = nil
	p.preComments = nil
}

// GetParserName returns the parser name.
// Returns "global-ee" to avoid collision with standard global parsers.
func (p *GlobalEE) GetParserName() string {
	return "global-ee"
}

// isEEGlobalDirective returns true if the directive is EE-specific.
func isEEGlobalDirective(directive string) bool {
	switch directive {
	case "maxmind-load", "maxmind-update", "maxmind-cache-size",
		"device-atlas-log-level", "device-atlas-json-file", "device-atlas-properties-cookie",
		"waf-load", "module-load":
		return true
	default:
		return false
	}
}

// Parse parses an EE global directive line.
func (p *GlobalEE) Parse(line string, parts []string, comment string) (string, error) {
	if len(parts) == 0 {
		return "", &errors.ParseError{Parser: "global-ee", Line: line}
	}

	directive := parts[0]

	// Only handle EE-specific directives
	if !isEEGlobalDirective(directive) {
		return "", &errors.ParseError{Parser: "global-ee", Line: line, Message: "not an EE directive"}
	}

	d := &EEGlobalDirective{
		Type:    directive,
		Parts:   parts[1:],
		Comment: comment,
	}

	p.directives = append(p.directives, d)
	return "", nil
}

// PreParse is called before Parse for preprocessor handling.
func (p *GlobalEE) PreParse(line string, parts, preComments []string, comment string) (string, error) {
	p.preComments = preComments
	return p.Parse(line, parts, comment)
}

// Get returns all parsed directives.
func (p *GlobalEE) Get(createIfNotExist bool) (common.ParserData, error) {
	if len(p.directives) == 0 && !createIfNotExist {
		return nil, errors.ErrFetch
	}
	return p.directives, nil
}

// GetPreComments returns pre-comments.
func (p *GlobalEE) GetPreComments() ([]string, error) {
	return p.preComments, nil
}

// GetOne returns parsed data at index.
func (p *GlobalEE) GetOne(index int) (common.ParserData, error) {
	if index < 0 || index >= len(p.directives) {
		return nil, errors.ErrFetch
	}
	return p.directives[index], nil
}

// Delete deletes data at index.
func (p *GlobalEE) Delete(index int) error {
	if index < 0 || index >= len(p.directives) {
		return nil
	}
	p.directives = append(p.directives[:index], p.directives[index+1:]...)
	return nil
}

// Insert inserts data at index.
func (p *GlobalEE) Insert(data common.ParserData, index int) error {
	d, ok := data.(*EEGlobalDirective)
	if !ok {
		return &errors.ParseError{Parser: "global-ee", Message: "invalid data type"}
	}

	if index >= len(p.directives) {
		p.directives = append(p.directives, d)
	} else if index <= 0 {
		p.directives = append([]*EEGlobalDirective{d}, p.directives...)
	} else {
		p.directives = append(p.directives[:index], append([]*EEGlobalDirective{d}, p.directives[index:]...)...)
	}
	return nil
}

// Set sets data at index.
func (p *GlobalEE) Set(data common.ParserData, index int) error {
	d, ok := data.(*EEGlobalDirective)
	if !ok {
		return &errors.ParseError{Parser: "global-ee", Message: "invalid data type"}
	}

	if index < 0 || index >= len(p.directives) {
		return &errors.ParseError{Parser: "global-ee", Message: "index out of range"}
	}

	p.directives[index] = d
	return nil
}

// SetPreComments sets pre-comments.
func (p *GlobalEE) SetPreComments(preComment []string) {
	p.preComments = preComment
}

// ResultAll returns all results for serialization.
func (p *GlobalEE) ResultAll() ([]common.ReturnResultLine, []string, error) {
	results := make([]common.ReturnResultLine, 0, len(p.directives))

	for _, d := range p.directives {
		line := d.Type
		if len(d.Parts) > 0 {
			line += " " + strings.Join(d.Parts, " ")
		}

		results = append(results, common.ReturnResultLine{
			Data:    line,
			Comment: d.Comment,
		})
	}

	return results, p.preComments, nil
}

// GetDirectives returns all parsed EE global directives.
func (p *GlobalEE) GetDirectives() []*EEGlobalDirective {
	return p.directives
}

// GetDirectivesByType returns directives of a specific type.
func (p *GlobalEE) GetDirectivesByType(directiveType string) []*EEGlobalDirective {
	var result []*EEGlobalDirective
	for _, d := range p.directives {
		if d.Type == directiveType {
			result = append(result, d)
		}
	}
	return result
}
