package directives

import (
	"strconv"

	"github.com/haproxytech/client-native/v6/config-parser/common"
	"github.com/haproxytech/client-native/v6/config-parser/errors"
)

// UDPLBData holds parsed udp-lb section data.
type UDPLBData struct {
	Name           string
	Balance        string
	ProxyRequests  int
	ProxyResponses int
}

// UDPLB parses udp-lb section directives.
type UDPLB struct {
	data        *UDPLBData
	preComments []string
}

// NewUDPLB creates a new UDPLB parser.
func NewUDPLB() *UDPLB {
	return &UDPLB{}
}

// Init initializes the parser.
func (p *UDPLB) Init() {
	p.data = nil
	p.preComments = nil
}

// GetParserName returns the parser name.
func (p *UDPLB) GetParserName() string {
	return "udp-lb"
}

// Parse parses a udp-lb directive line.
func (p *UDPLB) Parse(line string, parts []string, comment string) (string, error) {
	if len(parts) < 2 {
		return "", &errors.ParseError{Parser: "udp-lb", Line: line}
	}

	if p.data == nil {
		p.data = &UDPLBData{}
	}

	directive := parts[0]
	switch directive {
	case "balance":
		p.data.Balance = parts[1]
		return "", nil
	case "proxy-requests":
		val, err := strconv.Atoi(parts[1])
		if err != nil {
			return "", &errors.ParseError{Parser: "udp-lb", Line: line, Message: "invalid proxy-requests value"}
		}
		p.data.ProxyRequests = val
		return "", nil
	case "proxy-responses":
		val, err := strconv.Atoi(parts[1])
		if err != nil {
			return "", &errors.ParseError{Parser: "udp-lb", Line: line, Message: "invalid proxy-responses value"}
		}
		p.data.ProxyResponses = val
		return "", nil
	default:
		// Unknown directive - could be dgram-bind, server, etc.
		// These are handled by other parsers (e.g., standard server parser).
		return "", &errors.ParseError{Parser: "udp-lb", Line: line, Message: "unknown directive"}
	}
}

// PreParse is called before Parse for preprocessor handling.
func (p *UDPLB) PreParse(line string, parts, preComments []string, comment string) (string, error) {
	p.preComments = preComments
	return p.Parse(line, parts, comment)
}

// Get returns parsed data.
func (p *UDPLB) Get(createIfNotExist bool) (common.ParserData, error) {
	if p.data == nil && !createIfNotExist {
		return nil, errors.ErrFetch
	}
	return p.data, nil
}

// GetPreComments returns pre-comments.
func (p *UDPLB) GetPreComments() ([]string, error) {
	return p.preComments, nil
}

// GetOne returns parsed data at index.
func (p *UDPLB) GetOne(index int) (common.ParserData, error) {
	if index != 0 || p.data == nil {
		return nil, errors.ErrFetch
	}
	return p.data, nil
}

// Delete deletes data at index.
func (p *UDPLB) Delete(index int) error {
	if index == 0 {
		p.data = nil
	}
	return nil
}

// Insert inserts data at index.
func (p *UDPLB) Insert(data common.ParserData, index int) error {
	if d, ok := data.(*UDPLBData); ok {
		p.data = d
	}
	return nil
}

// Set sets data at index.
func (p *UDPLB) Set(data common.ParserData, index int) error {
	if d, ok := data.(*UDPLBData); ok {
		p.data = d
	}
	return nil
}

// SetPreComments sets pre-comments.
func (p *UDPLB) SetPreComments(preComment []string) {
	p.preComments = preComment
}

// ResultAll returns all results for serialization.
func (p *UDPLB) ResultAll() ([]common.ReturnResultLine, []string, error) {
	if p.data == nil {
		return nil, p.preComments, nil
	}

	var results []common.ReturnResultLine
	if p.data.Balance != "" {
		results = append(results, common.ReturnResultLine{Data: "balance " + p.data.Balance})
	}
	if p.data.ProxyRequests > 0 {
		results = append(results, common.ReturnResultLine{Data: "proxy-requests " + strconv.Itoa(p.data.ProxyRequests)})
	}
	if p.data.ProxyResponses > 0 {
		results = append(results, common.ReturnResultLine{Data: "proxy-responses " + strconv.Itoa(p.data.ProxyResponses)})
	}

	return results, p.preComments, nil
}

// GetData returns the parsed UDP LB data.
func (p *UDPLB) GetData() *UDPLBData {
	return p.data
}
