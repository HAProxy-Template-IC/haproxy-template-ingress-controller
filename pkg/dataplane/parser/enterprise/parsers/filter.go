package parsers

import (
	"strings"

	"github.com/haproxytech/client-native/v6/config-parser/common"
	"github.com/haproxytech/client-native/v6/config-parser/errors"
	"github.com/haproxytech/client-native/v6/config-parser/parsers/filters"

	"haproxy-template-ic/pkg/dataplane/parser/parserconfig"
)

// Constants for EE filter parsing keywords.
const (
	keywordProfile   = parserconfig.KeywordProfile
	keywordLearning  = parserconfig.KeywordLearning
	keywordRulesFile = "rules-file"
	keywordLogEnable = "log-enable"
)

// EEFilter is a type alias for types.EEFilter.
type EEFilter = parserconfig.EEFilter

// Filters wraps client-native's filters.Filters parser to add EE filter support.
// It intercepts EE-specific filters like "filter waf" and "filter botmgmt" before
// delegating to client-native for standard CE filters.
type Filters struct {
	// ceParser is the client-native parser for CE filters
	ceParser *filters.Filters

	// eeFilters holds parsed EE-specific filters
	eeFilters []*EEFilter

	// preComments holds accumulated pre-comments
	preComments []string
}

// NewFilters creates a new Filters wrapper parser.
func NewFilters() *Filters {
	ce := &filters.Filters{}
	ce.Init()
	return &Filters{
		ceParser:  ce,
		eeFilters: nil,
	}
}

// Init initializes the parser.
func (p *Filters) Init() {
	p.ceParser = &filters.Filters{}
	p.ceParser.Init()
	p.eeFilters = nil
	p.preComments = nil
}

// GetParserName returns the parser name.
func (p *Filters) GetParserName() string {
	return "filter"
}

// Parse parses a filter line.
// It tries EE filters first, then delegates to client-native for CE filters.
func (p *Filters) Parse(line string, parts []string, comment string) (string, error) {
	if len(parts) < 2 {
		return "", &errors.ParseError{Parser: "filter", Line: line}
	}

	// parts[0] should be "filter"
	// parts[1] is the filter type
	filterType := parts[1]

	// Check for EE-specific filters
	switch filterType {
	case parserconfig.FilterTypeWAF:
		return p.parseFilterWAF(line, parts, comment)
	case parserconfig.FilterTypeBotMgmt:
		return p.parseFilterBotMgmt(line, parts, comment)
	default:
		// Delegate to client-native for CE filters
		return p.ceParser.Parse(line, parts, comment)
	}
}

// parseFilterWAF parses "filter waf [name] [learning] [rules-file <path>] ..." directive.
func (p *Filters) parseFilterWAF(line string, parts []string, comment string) (string, error) {
	filter := &EEFilter{
		Type:    parserconfig.FilterTypeWAF,
		Comment: comment,
	}

	// Parse remaining parts
	i := 2 // Start after "filter waf"
	for i < len(parts) {
		switch parts[i] {
		case keywordLearning:
			filter.Learning = true
			i++
		case keywordRulesFile:
			if i+1 < len(parts) {
				filter.RulesFile = parts[i+1]
				i += 2
			} else {
				return "", &errors.ParseError{Parser: "filter", Line: line, Message: "missing rules-file path"}
			}
		case keywordLogEnable, "log-enabled":
			filter.LogEnabled = true
			i++
		default:
			// First unknown part could be the filter name
			if filter.Name == "" && !strings.HasPrefix(parts[i], "-") {
				filter.Name = parts[i]
			}
			i++
		}
	}

	p.eeFilters = append(p.eeFilters, filter)
	return "", nil
}

// parseFilterBotMgmt parses "filter botmgmt [profile <name>] ..." directive.
func (p *Filters) parseFilterBotMgmt(line string, parts []string, comment string) (string, error) {
	filter := &EEFilter{
		Type:    parserconfig.FilterTypeBotMgmt,
		Comment: comment,
	}

	// Parse remaining parts
	i := 2 // Start after "filter botmgmt"
	for i < len(parts) {
		switch parts[i] {
		case keywordProfile:
			if i+1 < len(parts) {
				filter.Profile = parts[i+1]
				i += 2
			} else {
				return "", &errors.ParseError{Parser: "filter", Line: line, Message: "missing profile name"}
			}
		default:
			// First unknown part could be the filter name
			if filter.Name == "" && !strings.HasPrefix(parts[i], "-") {
				filter.Name = parts[i]
			}
			i++
		}
	}

	p.eeFilters = append(p.eeFilters, filter)
	return "", nil
}

// PreParse is called before Parse for preprocessor handling.
func (p *Filters) PreParse(line string, parts, preComments []string, comment string) (string, error) {
	p.preComments = preComments
	return p.Parse(line, parts, comment)
}

// Get returns all parsed data (both CE and EE filters).
func (p *Filters) Get(createIfNotExist bool) (common.ParserData, error) {
	result := &FiltersData{
		EEFilters: p.eeFilters,
	}

	// Get CE filters from client-native parser
	if ceData, err := p.ceParser.Get(false); err == nil && ceData != nil {
		result.CEFilters = ceData
	}

	return result, nil
}

// GetPreComments returns pre-comments.
func (p *Filters) GetPreComments() ([]string, error) {
	return p.preComments, nil
}

// GetOne returns parsed data at index.
func (p *Filters) GetOne(index int) (common.ParserData, error) {
	if index < len(p.eeFilters) {
		return p.eeFilters[index], nil
	}

	ceIndex := index - len(p.eeFilters)
	return p.ceParser.GetOne(ceIndex)
}

// Delete deletes data at index.
func (p *Filters) Delete(index int) error {
	if index < len(p.eeFilters) {
		p.eeFilters = append(p.eeFilters[:index], p.eeFilters[index+1:]...)
		return nil
	}

	ceIndex := index - len(p.eeFilters)
	return p.ceParser.Delete(ceIndex)
}

// Insert inserts data at index.
func (p *Filters) Insert(data common.ParserData, index int) error {
	if eeFilter, ok := data.(*EEFilter); ok {
		if index >= len(p.eeFilters) {
			p.eeFilters = append(p.eeFilters, eeFilter)
		} else {
			p.eeFilters = append(p.eeFilters[:index], append([]*EEFilter{eeFilter}, p.eeFilters[index:]...)...)
		}
		return nil
	}

	return p.ceParser.Insert(data, index)
}

// Set sets data at index.
func (p *Filters) Set(data common.ParserData, index int) error {
	if eeFilter, ok := data.(*EEFilter); ok {
		if index < len(p.eeFilters) {
			p.eeFilters[index] = eeFilter
			return nil
		}
	}

	ceIndex := index - len(p.eeFilters)
	return p.ceParser.Set(data, ceIndex)
}

// SetPreComments sets pre-comments.
func (p *Filters) SetPreComments(preComment []string) {
	p.preComments = preComment
}

// ResultAll returns all results for serialization.
func (p *Filters) ResultAll() ([]common.ReturnResultLine, []string, error) {
	results := make([]common.ReturnResultLine, 0, len(p.eeFilters))

	// Add EE filters
	for _, filter := range p.eeFilters {
		line := "filter " + filter.Type
		if filter.Name != "" {
			line += " " + filter.Name
		}
		if filter.Learning {
			line += " learning"
		}
		if filter.RulesFile != "" {
			line += " rules-file " + filter.RulesFile
		}
		if filter.Profile != "" {
			line += " " + keywordProfile + " " + filter.Profile
		}
		if filter.LogEnabled {
			line += " log-enable"
		}

		results = append(results, common.ReturnResultLine{
			Data:    line,
			Comment: filter.Comment,
		})
	}

	// Add CE filters from client-native parser
	if ceResults, _, err := p.ceParser.ResultAll(); err == nil {
		results = append(results, ceResults...)
	}

	return results, p.preComments, nil
}

// GetEEFilters returns only the EE-specific filters.
func (p *Filters) GetEEFilters() []*EEFilter {
	return p.eeFilters
}

// FiltersData holds both CE and EE filters.
type FiltersData struct {
	CEFilters interface{}
	EEFilters []*EEFilter
}
