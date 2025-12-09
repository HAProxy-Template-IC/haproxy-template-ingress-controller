package directives

import (
	"strconv"

	"github.com/haproxytech/client-native/v6/config-parser/common"
	"github.com/haproxytech/client-native/v6/config-parser/errors"
)

// WAFGlobalData holds parsed waf-global section data.
type WAFGlobalData struct {
	RulesFile     string
	BodyLimit     int
	JSONLevels    int
	AnalyzerCache int
}

// WAFGlobal parses waf-global section directives.
type WAFGlobal struct {
	data        *WAFGlobalData
	preComments []string
}

// NewWAFGlobal creates a new WAFGlobal parser.
func NewWAFGlobal() *WAFGlobal {
	return &WAFGlobal{}
}

// Init initializes the parser.
func (p *WAFGlobal) Init() {
	p.data = nil
	p.preComments = nil
}

// GetParserName returns the parser name.
func (p *WAFGlobal) GetParserName() string {
	return "waf-global"
}

// Parse parses a waf-global directive line.
func (p *WAFGlobal) Parse(line string, parts []string, comment string) (string, error) {
	if len(parts) < 2 {
		return "", &errors.ParseError{Parser: "waf-global", Line: line}
	}

	if p.data == nil {
		p.data = &WAFGlobalData{}
	}

	directive := parts[0]
	switch directive {
	case "rules-file":
		p.data.RulesFile = parts[1]
		return "", nil
	case "body-limit":
		val, err := strconv.Atoi(parts[1])
		if err != nil {
			return "", &errors.ParseError{Parser: "waf-global", Line: line, Message: "invalid body-limit value"}
		}
		p.data.BodyLimit = val
		return "", nil
	case "json-levels":
		val, err := strconv.Atoi(parts[1])
		if err != nil {
			return "", &errors.ParseError{Parser: "waf-global", Line: line, Message: "invalid json-levels value"}
		}
		p.data.JSONLevels = val
		return "", nil
	case "analyzer-cache":
		val, err := strconv.Atoi(parts[1])
		if err != nil {
			return "", &errors.ParseError{Parser: "waf-global", Line: line, Message: "invalid analyzer-cache value"}
		}
		p.data.AnalyzerCache = val
		return "", nil
	default:
		// Unknown directive in waf-global - treat as unprocessed.
		return "", &errors.ParseError{Parser: "waf-global", Line: line, Message: "unknown directive"}
	}
}

// PreParse is called before Parse for preprocessor handling.
func (p *WAFGlobal) PreParse(line string, parts, preComments []string, comment string) (string, error) {
	p.preComments = preComments
	return p.Parse(line, parts, comment)
}

// Get returns parsed data.
func (p *WAFGlobal) Get(createIfNotExist bool) (common.ParserData, error) {
	if p.data == nil && !createIfNotExist {
		return nil, errors.ErrFetch
	}
	return p.data, nil
}

// GetPreComments returns pre-comments.
func (p *WAFGlobal) GetPreComments() ([]string, error) {
	return p.preComments, nil
}

// GetOne returns parsed data at index.
func (p *WAFGlobal) GetOne(index int) (common.ParserData, error) {
	if index != 0 || p.data == nil {
		return nil, errors.ErrFetch
	}
	return p.data, nil
}

// Delete deletes data at index.
func (p *WAFGlobal) Delete(index int) error {
	if index == 0 {
		p.data = nil
	}
	return nil
}

// Insert inserts data at index.
func (p *WAFGlobal) Insert(data common.ParserData, index int) error {
	if d, ok := data.(*WAFGlobalData); ok {
		p.data = d
	}
	return nil
}

// Set sets data at index.
func (p *WAFGlobal) Set(data common.ParserData, index int) error {
	if d, ok := data.(*WAFGlobalData); ok {
		p.data = d
	}
	return nil
}

// SetPreComments sets pre-comments.
func (p *WAFGlobal) SetPreComments(preComment []string) {
	p.preComments = preComment
}

// ResultAll returns all results for serialization.
func (p *WAFGlobal) ResultAll() ([]common.ReturnResultLine, []string, error) {
	if p.data == nil {
		return nil, p.preComments, nil
	}

	var results []common.ReturnResultLine
	if p.data.RulesFile != "" {
		results = append(results, common.ReturnResultLine{Data: "rules-file " + p.data.RulesFile})
	}
	if p.data.BodyLimit > 0 {
		results = append(results, common.ReturnResultLine{Data: "body-limit " + strconv.Itoa(p.data.BodyLimit)})
	}
	if p.data.JSONLevels > 0 {
		results = append(results, common.ReturnResultLine{Data: "json-levels " + strconv.Itoa(p.data.JSONLevels)})
	}
	if p.data.AnalyzerCache > 0 {
		results = append(results, common.ReturnResultLine{Data: "analyzer-cache " + strconv.Itoa(p.data.AnalyzerCache)})
	}

	return results, p.preComments, nil
}

// GetData returns the parsed WAF global data.
func (p *WAFGlobal) GetData() *WAFGlobalData {
	return p.data
}

// WAFProfileData holds parsed waf-profile section data.
type WAFProfileData struct {
	Name      string
	RulesFile string
	BodyLimit int
}

// WAFProfile parses waf-profile section directives.
type WAFProfile struct {
	data        *WAFProfileData
	preComments []string
}

// NewWAFProfile creates a new WAFProfile parser.
func NewWAFProfile() *WAFProfile {
	return &WAFProfile{}
}

// Init initializes the parser.
func (p *WAFProfile) Init() {
	p.data = nil
	p.preComments = nil
}

// GetParserName returns the parser name.
func (p *WAFProfile) GetParserName() string {
	return "waf-profile"
}

// Parse parses a waf-profile directive line.
func (p *WAFProfile) Parse(line string, parts []string, comment string) (string, error) {
	if len(parts) < 2 {
		return "", &errors.ParseError{Parser: "waf-profile", Line: line}
	}

	if p.data == nil {
		p.data = &WAFProfileData{}
	}

	directive := parts[0]
	switch directive {
	case "rules-file":
		p.data.RulesFile = parts[1]
		return "", nil
	case "body-limit":
		val, err := strconv.Atoi(parts[1])
		if err != nil {
			return "", &errors.ParseError{Parser: "waf-profile", Line: line, Message: "invalid body-limit value"}
		}
		p.data.BodyLimit = val
		return "", nil
	default:
		return "", &errors.ParseError{Parser: "waf-profile", Line: line, Message: "unknown directive"}
	}
}

// PreParse is called before Parse for preprocessor handling.
func (p *WAFProfile) PreParse(line string, parts, preComments []string, comment string) (string, error) {
	p.preComments = preComments
	return p.Parse(line, parts, comment)
}

// Get returns parsed data.
func (p *WAFProfile) Get(createIfNotExist bool) (common.ParserData, error) {
	if p.data == nil && !createIfNotExist {
		return nil, errors.ErrFetch
	}
	return p.data, nil
}

// GetPreComments returns pre-comments.
func (p *WAFProfile) GetPreComments() ([]string, error) {
	return p.preComments, nil
}

// GetOne returns parsed data at index.
func (p *WAFProfile) GetOne(index int) (common.ParserData, error) {
	if index != 0 || p.data == nil {
		return nil, errors.ErrFetch
	}
	return p.data, nil
}

// Delete deletes data at index.
func (p *WAFProfile) Delete(index int) error {
	if index == 0 {
		p.data = nil
	}
	return nil
}

// Insert inserts data at index.
func (p *WAFProfile) Insert(data common.ParserData, index int) error {
	if d, ok := data.(*WAFProfileData); ok {
		p.data = d
	}
	return nil
}

// Set sets data at index.
func (p *WAFProfile) Set(data common.ParserData, index int) error {
	if d, ok := data.(*WAFProfileData); ok {
		p.data = d
	}
	return nil
}

// SetPreComments sets pre-comments.
func (p *WAFProfile) SetPreComments(preComment []string) {
	p.preComments = preComment
}

// ResultAll returns all results for serialization.
func (p *WAFProfile) ResultAll() ([]common.ReturnResultLine, []string, error) {
	if p.data == nil {
		return nil, p.preComments, nil
	}

	var results []common.ReturnResultLine
	if p.data.RulesFile != "" {
		results = append(results, common.ReturnResultLine{Data: "rules-file " + p.data.RulesFile})
	}
	if p.data.BodyLimit > 0 {
		results = append(results, common.ReturnResultLine{Data: "body-limit " + strconv.Itoa(p.data.BodyLimit)})
	}

	return results, p.preComments, nil
}

// GetData returns the parsed WAF profile data.
func (p *WAFProfile) GetData() *WAFProfileData {
	return p.data
}
