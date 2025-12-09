package parsers

import (
	"github.com/haproxytech/client-native/v6/config-parser/common"
	"github.com/haproxytech/client-native/v6/config-parser/errors"
	"github.com/haproxytech/client-native/v6/config-parser/parsers/http"
)

// EEHTTPResponseAction represents an Enterprise Edition http-response action.
// Currently, no EE http-response actions are documented (oidc-sso was skipped).
// This struct is defined for future extensibility.
type EEHTTPResponseAction struct {
	// Type is the action type.
	Type string

	// Profile is the profile name for the action.
	Profile string

	// Cond is the condition type ("if" or "unless").
	Cond string

	// CondTest is the ACL condition expression.
	CondTest string

	// Comment is the inline comment.
	Comment string
}

// HTTPResponses wraps client-native's http.Responses parser to add EE action support.
// It intercepts EE-specific actions before delegating to client-native for standard CE actions.
// Currently, no EE http-response actions are implemented, but the wrapper is ready for future use.
type HTTPResponses struct {
	// ceParser is the client-native parser for CE actions.
	ceParser *http.Responses

	// eeActions holds parsed EE-specific actions.
	eeActions []*EEHTTPResponseAction

	// preComments holds accumulated pre-comments.
	preComments []string
}

// NewHTTPResponses creates a new HTTPResponses wrapper parser.
func NewHTTPResponses() *HTTPResponses {
	ce := &http.Responses{}
	ce.Init()
	return &HTTPResponses{
		ceParser:  ce,
		eeActions: nil,
	}
}

// Init initializes the parser.
func (p *HTTPResponses) Init() {
	p.ceParser = &http.Responses{}
	p.ceParser.Init()
	p.eeActions = nil
	p.preComments = nil
}

// GetParserName returns the parser name.
func (p *HTTPResponses) GetParserName() string {
	return "http-response"
}

// Parse parses an http-response line.
// It tries EE actions first, then delegates to client-native for CE actions.
func (p *HTTPResponses) Parse(line string, parts []string, comment string) (string, error) {
	if len(parts) < 2 {
		return "", &errors.ParseError{Parser: "http-response", Line: line}
	}

	// parts[0] should be "http-response"
	// parts[1] is the action type
	// Currently no EE http-response actions are implemented.
	// When adding EE actions, add switch cases here before delegating to CE parser.

	// Delegate to client-native for all actions (no EE actions implemented yet).
	return p.ceParser.Parse(line, parts, comment)
}

// PreParse is called before Parse for preprocessor handling.
func (p *HTTPResponses) PreParse(line string, parts, preComments []string, comment string) (string, error) {
	p.preComments = preComments
	return p.Parse(line, parts, comment)
}

// Get returns all parsed data (both CE and EE actions).
func (p *HTTPResponses) Get(createIfNotExist bool) (common.ParserData, error) {
	result := &HTTPResponsesData{
		EEActions: p.eeActions,
	}

	// Get CE actions from client-native parser.
	if ceData, err := p.ceParser.Get(false); err == nil && ceData != nil {
		result.CEActions = ceData
	}

	return result, nil
}

// GetPreComments returns pre-comments.
func (p *HTTPResponses) GetPreComments() ([]string, error) {
	return p.preComments, nil
}

// GetOne returns parsed data at index.
func (p *HTTPResponses) GetOne(index int) (common.ParserData, error) {
	if index < len(p.eeActions) {
		return p.eeActions[index], nil
	}

	ceIndex := index - len(p.eeActions)
	return p.ceParser.GetOne(ceIndex)
}

// Delete deletes data at index.
func (p *HTTPResponses) Delete(index int) error {
	if index < len(p.eeActions) {
		p.eeActions = append(p.eeActions[:index], p.eeActions[index+1:]...)
		return nil
	}

	ceIndex := index - len(p.eeActions)
	return p.ceParser.Delete(ceIndex)
}

// Insert inserts data at index.
func (p *HTTPResponses) Insert(data common.ParserData, index int) error {
	if eeAction, ok := data.(*EEHTTPResponseAction); ok {
		if index >= len(p.eeActions) {
			p.eeActions = append(p.eeActions, eeAction)
		} else {
			p.eeActions = append(p.eeActions[:index], append([]*EEHTTPResponseAction{eeAction}, p.eeActions[index:]...)...)
		}
		return nil
	}

	return p.ceParser.Insert(data, index)
}

// Set sets data at index.
func (p *HTTPResponses) Set(data common.ParserData, index int) error {
	if eeAction, ok := data.(*EEHTTPResponseAction); ok {
		if index < len(p.eeActions) {
			p.eeActions[index] = eeAction
			return nil
		}
	}

	ceIndex := index - len(p.eeActions)
	return p.ceParser.Set(data, ceIndex)
}

// SetPreComments sets pre-comments.
func (p *HTTPResponses) SetPreComments(preComment []string) {
	p.preComments = preComment
}

// ResultAll returns all results for serialization.
func (p *HTTPResponses) ResultAll() ([]common.ReturnResultLine, []string, error) {
	results := make([]common.ReturnResultLine, 0, len(p.eeActions))

	// Add EE actions (none currently implemented).
	for _, action := range p.eeActions {
		line := "http-response " + action.Type
		if action.Profile != "" {
			line += " profile " + action.Profile
		}
		if action.Cond != "" {
			line += " " + action.Cond
			if action.CondTest != "" {
				line += " " + action.CondTest
			}
		}

		results = append(results, common.ReturnResultLine{
			Data:    line,
			Comment: action.Comment,
		})
	}

	// Add CE actions from client-native parser.
	if ceResults, _, err := p.ceParser.ResultAll(); err == nil {
		results = append(results, ceResults...)
	}

	return results, p.preComments, nil
}

// GetEEActions returns only the EE-specific actions.
func (p *HTTPResponses) GetEEActions() []*EEHTTPResponseAction {
	return p.eeActions
}

// HTTPResponsesData holds both CE and EE http-response actions.
type HTTPResponsesData struct {
	CEActions interface{}
	EEActions []*EEHTTPResponseAction
}
