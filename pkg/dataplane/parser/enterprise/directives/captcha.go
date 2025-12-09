package directives

import (
	"github.com/haproxytech/client-native/v6/config-parser/common"
	"github.com/haproxytech/client-native/v6/config-parser/errors"
)

// CaptchaData holds parsed captcha section data.
type CaptchaData struct {
	Name      string
	Provider  string
	PublicKey string
	SecretKey string
	HTMLFile  string
}

// Captcha parses captcha section directives.
type Captcha struct {
	data        *CaptchaData
	preComments []string
}

// NewCaptcha creates a new Captcha parser.
func NewCaptcha() *Captcha {
	return &Captcha{}
}

// Init initializes the parser.
func (p *Captcha) Init() {
	p.data = nil
	p.preComments = nil
}

// GetParserName returns the parser name.
func (p *Captcha) GetParserName() string {
	return "captcha"
}

// Parse parses a captcha directive line.
func (p *Captcha) Parse(line string, parts []string, comment string) (string, error) {
	if len(parts) < 2 {
		return "", &errors.ParseError{Parser: "captcha", Line: line}
	}

	if p.data == nil {
		p.data = &CaptchaData{}
	}

	directive := parts[0]
	switch directive {
	case "provider":
		p.data.Provider = parts[1]
		return "", nil
	case "public-key":
		p.data.PublicKey = parts[1]
		return "", nil
	case "secret-key":
		p.data.SecretKey = parts[1]
		return "", nil
	case "html-file":
		p.data.HTMLFile = parts[1]
		return "", nil
	default:
		return "", &errors.ParseError{Parser: "captcha", Line: line, Message: "unknown directive"}
	}
}

// PreParse is called before Parse for preprocessor handling.
func (p *Captcha) PreParse(line string, parts, preComments []string, comment string) (string, error) {
	p.preComments = preComments
	return p.Parse(line, parts, comment)
}

// Get returns parsed data.
func (p *Captcha) Get(createIfNotExist bool) (common.ParserData, error) {
	if p.data == nil && !createIfNotExist {
		return nil, errors.ErrFetch
	}
	return p.data, nil
}

// GetPreComments returns pre-comments.
func (p *Captcha) GetPreComments() ([]string, error) {
	return p.preComments, nil
}

// GetOne returns parsed data at index.
func (p *Captcha) GetOne(index int) (common.ParserData, error) {
	if index != 0 || p.data == nil {
		return nil, errors.ErrFetch
	}
	return p.data, nil
}

// Delete deletes data at index.
func (p *Captcha) Delete(index int) error {
	if index == 0 {
		p.data = nil
	}
	return nil
}

// Insert inserts data at index.
func (p *Captcha) Insert(data common.ParserData, index int) error {
	if d, ok := data.(*CaptchaData); ok {
		p.data = d
	}
	return nil
}

// Set sets data at index.
func (p *Captcha) Set(data common.ParserData, index int) error {
	if d, ok := data.(*CaptchaData); ok {
		p.data = d
	}
	return nil
}

// SetPreComments sets pre-comments.
func (p *Captcha) SetPreComments(preComment []string) {
	p.preComments = preComment
}

// ResultAll returns all results for serialization.
func (p *Captcha) ResultAll() ([]common.ReturnResultLine, []string, error) {
	if p.data == nil {
		return nil, p.preComments, nil
	}

	var results []common.ReturnResultLine
	if p.data.Provider != "" {
		results = append(results, common.ReturnResultLine{Data: "provider " + p.data.Provider})
	}
	if p.data.PublicKey != "" {
		results = append(results, common.ReturnResultLine{Data: "public-key " + p.data.PublicKey})
	}
	if p.data.SecretKey != "" {
		results = append(results, common.ReturnResultLine{Data: "secret-key " + p.data.SecretKey})
	}
	if p.data.HTMLFile != "" {
		results = append(results, common.ReturnResultLine{Data: "html-file " + p.data.HTMLFile})
	}

	return results, p.preComments, nil
}

// GetData returns the parsed captcha data.
func (p *Captcha) GetData() *CaptchaData {
	return p.data
}
