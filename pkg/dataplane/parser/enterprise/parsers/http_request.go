// Package parsers provides wrapper parsers for HAProxy Enterprise Edition directives.
//
// These wrappers intercept EE-specific directives that client-native doesn't support,
// while delegating standard CE directives to client-native parsers.
package parsers

import (
	"strings"

	"github.com/haproxytech/client-native/v6/config-parser/common"
	"github.com/haproxytech/client-native/v6/config-parser/errors"
	"github.com/haproxytech/client-native/v6/config-parser/parsers/http"

	"gitlab.com/haproxy-template-ic/haproxy-template-ingress-controller/pkg/dataplane/parser/parserconfig"
)

// EEHTTPRequestAction is a type alias for types.EEHTTPRequestAction.
type EEHTTPRequestAction = parserconfig.EEHTTPRequestAction

// HTTPRequests wraps client-native's http.Requests parser to add EE action support.
// It intercepts EE-specific actions like waf-evaluate and botmgmt-evaluate before
// delegating to client-native for standard CE actions.
type HTTPRequests struct {
	// ceParser is the client-native parser for CE actions
	ceParser *http.Requests

	// eeActions holds parsed EE-specific actions
	eeActions []*EEHTTPRequestAction

	// preComments holds accumulated pre-comments
	preComments []string
}

// NewHTTPRequests creates a new HTTPRequests wrapper parser.
func NewHTTPRequests() *HTTPRequests {
	ce := &http.Requests{}
	ce.Init()
	return &HTTPRequests{
		ceParser:  ce,
		eeActions: nil,
	}
}

// Init initializes the parser.
func (p *HTTPRequests) Init() {
	p.ceParser = &http.Requests{}
	p.ceParser.Init()
	p.eeActions = nil
	p.preComments = nil
}

// GetParserName returns the parser name.
func (p *HTTPRequests) GetParserName() string {
	return "http-request"
}

// Parse parses an http-request line.
// It tries EE actions first, then delegates to client-native for CE actions.
func (p *HTTPRequests) Parse(line string, parts []string, comment string) (string, error) {
	if len(parts) < 2 {
		return "", &errors.ParseError{Parser: "http-request", Line: line}
	}

	// parts[0] should be "http-request"
	// parts[1] is the action type
	action := parts[1]

	// Check for EE-specific actions
	switch action {
	case parserconfig.ActionWAFEvaluate:
		return p.parseWAFEvaluate(line, parts, comment)
	case parserconfig.ActionBotMgmtEvaluate:
		return p.parseBotMgmtEvaluate(line, parts, comment)
	default:
		// Delegate to client-native for CE actions
		return p.ceParser.Parse(line, parts, comment)
	}
}

// parseWAFEvaluate parses "http-request waf-evaluate [profile <name>] [if|unless <cond>]" action.
func (p *HTTPRequests) parseWAFEvaluate(line string, parts []string, comment string) (string, error) {
	action := &EEHTTPRequestAction{
		Type:    parserconfig.ActionWAFEvaluate,
		Comment: comment,
	}

	// Parse remaining parts
	i := 2 // Start after "http-request waf-evaluate"
	for i < len(parts) {
		switch parts[i] {
		case keywordProfile:
			if i+1 < len(parts) {
				action.Profile = parts[i+1]
				i += 2
			} else {
				return "", &errors.ParseError{Parser: "http-request", Line: line, Message: "missing profile name"}
			}
		case parserconfig.KeywordIf, parserconfig.KeywordUnless:
			action.Cond = parts[i]
			if i+1 < len(parts) {
				// Join remaining parts as the condition
				action.CondTest = strings.Join(parts[i+1:], " ")
			}
			i = len(parts) // End parsing
		default:
			i++
		}
	}

	p.eeActions = append(p.eeActions, action)
	return "", nil
}

// parseBotMgmtEvaluate parses "http-request botmgmt-evaluate [profile <name>] [if|unless <cond>]" action.
func (p *HTTPRequests) parseBotMgmtEvaluate(line string, parts []string, comment string) (string, error) {
	action := &EEHTTPRequestAction{
		Type:    parserconfig.ActionBotMgmtEvaluate,
		Comment: comment,
	}

	// Parse remaining parts
	i := 2 // Start after "http-request botmgmt-evaluate"
	for i < len(parts) {
		switch parts[i] {
		case keywordProfile:
			if i+1 < len(parts) {
				action.Profile = parts[i+1]
				i += 2
			} else {
				return "", &errors.ParseError{Parser: "http-request", Line: line, Message: "missing profile name"}
			}
		case parserconfig.KeywordIf, parserconfig.KeywordUnless:
			action.Cond = parts[i]
			if i+1 < len(parts) {
				action.CondTest = strings.Join(parts[i+1:], " ")
			}
			i = len(parts)
		default:
			i++
		}
	}

	p.eeActions = append(p.eeActions, action)
	return "", nil
}

// PreParse is called before Parse for preprocessor handling.
func (p *HTTPRequests) PreParse(line string, parts, preComments []string, comment string) (string, error) {
	p.preComments = preComments
	return p.Parse(line, parts, comment)
}

// Get returns all parsed data (both CE and EE actions).
func (p *HTTPRequests) Get(createIfNotExist bool) (common.ParserData, error) {
	// Return combined data
	result := &HTTPRequestsData{
		EEActions: p.eeActions,
	}

	// Get CE actions from client-native parser
	if ceData, err := p.ceParser.Get(false); err == nil && ceData != nil {
		result.CEActions = ceData
	}

	return result, nil
}

// GetPreComments returns pre-comments.
func (p *HTTPRequests) GetPreComments() ([]string, error) {
	return p.preComments, nil
}

// GetOne returns parsed data at index.
func (p *HTTPRequests) GetOne(index int) (common.ParserData, error) {
	// For simplicity, return from EE actions first, then CE
	if index < len(p.eeActions) {
		return p.eeActions[index], nil
	}

	// Adjust index for CE actions
	ceIndex := index - len(p.eeActions)
	return p.ceParser.GetOne(ceIndex)
}

// Delete deletes data at index.
func (p *HTTPRequests) Delete(index int) error {
	if index < len(p.eeActions) {
		p.eeActions = append(p.eeActions[:index], p.eeActions[index+1:]...)
		return nil
	}

	ceIndex := index - len(p.eeActions)
	return p.ceParser.Delete(ceIndex)
}

// Insert inserts data at index.
func (p *HTTPRequests) Insert(data common.ParserData, index int) error {
	if eeAction, ok := data.(*EEHTTPRequestAction); ok {
		if index >= len(p.eeActions) {
			p.eeActions = append(p.eeActions, eeAction)
		} else {
			p.eeActions = append(p.eeActions[:index], append([]*EEHTTPRequestAction{eeAction}, p.eeActions[index:]...)...)
		}
		return nil
	}

	return p.ceParser.Insert(data, index)
}

// Set sets data at index.
func (p *HTTPRequests) Set(data common.ParserData, index int) error {
	if eeAction, ok := data.(*EEHTTPRequestAction); ok {
		if index < len(p.eeActions) {
			p.eeActions[index] = eeAction
			return nil
		}
	}

	ceIndex := index - len(p.eeActions)
	return p.ceParser.Set(data, ceIndex)
}

// SetPreComments sets pre-comments.
func (p *HTTPRequests) SetPreComments(preComment []string) {
	p.preComments = preComment
}

// ResultAll returns all results for serialization.
func (p *HTTPRequests) ResultAll() ([]common.ReturnResultLine, []string, error) {
	results := make([]common.ReturnResultLine, 0, len(p.eeActions))

	// Add EE actions
	for _, action := range p.eeActions {
		line := "http-request " + action.Type
		if action.Profile != "" {
			line += " " + keywordProfile + " " + action.Profile
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

	// Add CE actions from client-native parser
	if ceResults, _, err := p.ceParser.ResultAll(); err == nil {
		results = append(results, ceResults...)
	}

	return results, p.preComments, nil
}

// GetEEActions returns only the EE-specific actions.
func (p *HTTPRequests) GetEEActions() []*EEHTTPRequestAction {
	return p.eeActions
}

// HTTPRequestsData holds both CE and EE http-request actions.
type HTTPRequestsData struct {
	CEActions interface{}
	EEActions []*EEHTTPRequestAction
}
