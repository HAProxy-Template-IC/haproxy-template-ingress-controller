package directives

import (
	"strconv"

	"github.com/haproxytech/client-native/v6/config-parser/common"
	"github.com/haproxytech/client-native/v6/config-parser/errors"
)

// DynamicUpdateData holds parsed dynamic-update section data.
type DynamicUpdateData struct {
	Name    string
	URL     string
	Delay   int
	Timeout int
	Map     string
}

// DynamicUpdate parses dynamic-update section directives.
type DynamicUpdate struct {
	data        *DynamicUpdateData
	preComments []string
}

// NewDynamicUpdate creates a new DynamicUpdate parser.
func NewDynamicUpdate() *DynamicUpdate {
	return &DynamicUpdate{}
}

// Init initializes the parser.
func (p *DynamicUpdate) Init() {
	p.data = nil
	p.preComments = nil
}

// GetParserName returns the parser name.
func (p *DynamicUpdate) GetParserName() string {
	return "dynamic-update"
}

// Parse parses a dynamic-update directive line.
func (p *DynamicUpdate) Parse(line string, parts []string, comment string) (string, error) {
	if len(parts) < 2 {
		return "", &errors.ParseError{Parser: "dynamic-update", Line: line}
	}

	if p.data == nil {
		p.data = &DynamicUpdateData{}
	}

	directive := parts[0]
	switch directive {
	case "url":
		p.data.URL = parts[1]
		return "", nil
	case "delay":
		val, err := strconv.Atoi(parts[1])
		if err != nil {
			return "", &errors.ParseError{Parser: "dynamic-update", Line: line, Message: "invalid delay value"}
		}
		p.data.Delay = val
		return "", nil
	case "timeout":
		val, err := strconv.Atoi(parts[1])
		if err != nil {
			return "", &errors.ParseError{Parser: "dynamic-update", Line: line, Message: "invalid timeout value"}
		}
		p.data.Timeout = val
		return "", nil
	case "map":
		p.data.Map = parts[1]
		return "", nil
	default:
		return "", &errors.ParseError{Parser: "dynamic-update", Line: line, Message: "unknown directive"}
	}
}

// PreParse is called before Parse for preprocessor handling.
func (p *DynamicUpdate) PreParse(line string, parts, preComments []string, comment string) (string, error) {
	p.preComments = preComments
	return p.Parse(line, parts, comment)
}

// Get returns parsed data.
func (p *DynamicUpdate) Get(createIfNotExist bool) (common.ParserData, error) {
	if p.data == nil && !createIfNotExist {
		return nil, errors.ErrFetch
	}
	return p.data, nil
}

// GetPreComments returns pre-comments.
func (p *DynamicUpdate) GetPreComments() ([]string, error) {
	return p.preComments, nil
}

// GetOne returns parsed data at index.
func (p *DynamicUpdate) GetOne(index int) (common.ParserData, error) {
	if index != 0 || p.data == nil {
		return nil, errors.ErrFetch
	}
	return p.data, nil
}

// Delete deletes data at index.
func (p *DynamicUpdate) Delete(index int) error {
	if index == 0 {
		p.data = nil
	}
	return nil
}

// Insert inserts data at index.
func (p *DynamicUpdate) Insert(data common.ParserData, index int) error {
	if d, ok := data.(*DynamicUpdateData); ok {
		p.data = d
	}
	return nil
}

// Set sets data at index.
func (p *DynamicUpdate) Set(data common.ParserData, index int) error {
	if d, ok := data.(*DynamicUpdateData); ok {
		p.data = d
	}
	return nil
}

// SetPreComments sets pre-comments.
func (p *DynamicUpdate) SetPreComments(preComment []string) {
	p.preComments = preComment
}

// ResultAll returns all results for serialization.
func (p *DynamicUpdate) ResultAll() ([]common.ReturnResultLine, []string, error) {
	if p.data == nil {
		return nil, p.preComments, nil
	}

	var results []common.ReturnResultLine
	if p.data.URL != "" {
		results = append(results, common.ReturnResultLine{Data: "url " + p.data.URL})
	}
	if p.data.Delay > 0 {
		results = append(results, common.ReturnResultLine{Data: "delay " + strconv.Itoa(p.data.Delay)})
	}
	if p.data.Timeout > 0 {
		results = append(results, common.ReturnResultLine{Data: "timeout " + strconv.Itoa(p.data.Timeout)})
	}
	if p.data.Map != "" {
		results = append(results, common.ReturnResultLine{Data: "map " + p.data.Map})
	}

	return results, p.preComments, nil
}

// GetData returns the parsed dynamic update data.
func (p *DynamicUpdate) GetData() *DynamicUpdateData {
	return p.data
}
