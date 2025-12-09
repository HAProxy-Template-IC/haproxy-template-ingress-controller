package enterprise

import (
	parser "github.com/haproxytech/client-native/v6/config-parser"
	"github.com/haproxytech/client-native/v6/config-parser/errors"
	"github.com/haproxytech/client-native/v6/config-parser/parsers"
	"github.com/haproxytech/client-native/v6/config-parser/parsers/extra"
	"github.com/haproxytech/client-native/v6/config-parser/parsers/tcp"

	eeparsers "haproxy-template-ic/pkg/dataplane/parser/enterprise/parsers"
)

// DefaultFactory implements ParserFactory using client-native parsers.
// It creates parser collections for both CE and EE sections.
type DefaultFactory struct{}

// NewDefaultFactory creates a new DefaultFactory.
func NewDefaultFactory() *DefaultFactory {
	return &DefaultFactory{}
}

// addParser is a helper that adds a parser to the collection.
func addParser(parserMap map[string]parser.ParserInterface, sequence *[]parser.Section, p parser.ParserInterface) {
	p.Init()
	parserMap[p.GetParserName()] = p
	// parser.Section is a string type alias
	*sequence = append(*sequence, parser.Section(p.GetParserName()))
}

// addParserWithNames adds a parser under multiple directive names.
// This is used for parsers like GlobalEE that handle multiple directives.
func addParserWithNames(parserMap map[string]parser.ParserInterface, sequence *[]parser.Section, p parser.ParserInterface, names ...string) {
	p.Init()
	for _, name := range names {
		parserMap[name] = p
	}
	// Add only once to sequence
	if len(names) > 0 {
		*sequence = append(*sequence, parser.Section(names[0]))
	}
}

// createParsers creates a parser.Parsers collection from a list of parsers.
func createParsers(parserList ...parser.ParserInterface) *parser.Parsers {
	parserMap := make(map[string]parser.ParserInterface)
	sequence := []parser.Section{}

	for _, p := range parserList {
		addParser(parserMap, &sequence, p)
	}

	return &parser.Parsers{
		Parsers:        parserMap,
		ParserSequence: sequence,
	}
}

// --- CE Singleton Section Factories ---

// CreateGlobalParsers creates parsers for the global section.
// Includes EE parser for maxmind-load, waf-load, etc.
func (f *DefaultFactory) CreateGlobalParsers() *parser.Parsers {
	parserMap := make(map[string]parser.ParserInterface)
	sequence := []parser.Section{}

	// Add CE parsers
	addParser(parserMap, &sequence, &parsers.Daemon{})
	addParser(parserMap, &sequence, &parsers.MasterWorker{})
	addParser(parserMap, &sequence, &parsers.NbProc{})
	addParser(parserMap, &sequence, &parsers.NbThread{})
	addParser(parserMap, &sequence, &parsers.CPUMap{})
	addParser(parserMap, &sequence, &parsers.Log{})
	addParser(parserMap, &sequence, &parsers.LogSendHostName{})
	addParser(parserMap, &sequence, &parsers.MaxConn{})
	addParser(parserMap, &sequence, &parsers.StatsTimeout{})
	addParser(parserMap, &sequence, &parsers.User{})
	addParser(parserMap, &sequence, &parsers.Group{})
	addParser(parserMap, &sequence, &parsers.ConfigSnippet{})

	// Add EE global parser under each directive name it handles
	// Also register under "global-ee" for extraction
	globalEE := eeparsers.NewGlobalEE()
	addParserWithNames(parserMap, &sequence, globalEE,
		"maxmind-load", "maxmind-update", "maxmind-cache-size",
		"device-atlas-log-level", "device-atlas-json-file", "device-atlas-properties-cookie",
		"waf-load", "module-load",
		"global-ee", // Also register under parser name for extraction
	)

	// Add fallback
	addParser(parserMap, &sequence, &extra.UnProcessed{})

	return &parser.Parsers{
		Parsers:        parserMap,
		ParserSequence: sequence,
	}
}

// CreateDefaultsParsers creates parsers for the defaults section.
func (f *DefaultFactory) CreateDefaultsParsers() *parser.Parsers {
	return createParsers(
		&parsers.Mode{},
		&parsers.OptionHttpchk{},
		&parsers.OptionForwardFor{},
		&parsers.OptionRedispatch{},
		&parsers.Log{},
		&parsers.MaxConn{},
		&parsers.Balance{},
		&parsers.DefaultServer{},
		&parsers.DefaultBackend{},
		&parsers.HTTPReuse{},
		&parsers.ConfigSnippet{},
		&extra.UnProcessed{},
	)
}

// CreateCommentsParsers creates parsers for comment lines.
func (f *DefaultFactory) CreateCommentsParsers() *parser.Parsers {
	return createParsers(
		&extra.Comments{},
	)
}

// --- CE Named Section Factories ---

// CreateFrontendParsers creates parsers for frontend sections.
// Uses wrapper parsers for http-request and filter to capture EE directives.
func (f *DefaultFactory) CreateFrontendParsers() *parser.Parsers {
	return createParsers(
		&parsers.Mode{},
		&parsers.Bind{},
		&parsers.Log{},
		&parsers.UniqueIDFormat{},
		&parsers.UniqueIDHeader{},
		&parsers.MaxConn{},
		&parsers.DefaultBackend{},
		&parsers.ACL{},
		eeparsers.NewHTTPRequests(), // Wrapper for EE http-request actions
		eeparsers.NewFilters(),      // Wrapper for EE filters
		&tcp.Requests{},
		&tcp.Responses{},
		&parsers.UseBackend{},
		&parsers.OptionForwardFor{},
		&parsers.OptionHTTPLog{},
		&parsers.DeclareCapture{},
		&parsers.ConfigSnippet{},
		&extra.UnProcessed{},
	)
}

// CreateBackendParsers creates parsers for backend sections.
// Uses wrapper parsers for http-request and filter to capture EE directives.
func (f *DefaultFactory) CreateBackendParsers() *parser.Parsers {
	return createParsers(
		&parsers.Mode{},
		&parsers.Balance{},
		&parsers.HashType{},
		&parsers.Server{},
		&parsers.DefaultServer{},
		&parsers.OptionHttpchk{},
		&parsers.HTTPCheckV2{},
		&parsers.ACL{},
		eeparsers.NewHTTPRequests(), // Wrapper for EE http-request actions
		eeparsers.NewFilters(),      // Wrapper for EE filters
		&tcp.Requests{},
		&tcp.Responses{},
		&parsers.Log{},
		&parsers.MaxConn{},
		&parsers.HTTPReuse{},
		&parsers.Cookie{},
		&parsers.StickTable{},
		&parsers.Stick{},
		&parsers.OptionForwardFor{},
		&parsers.OptionHTTPLog{},
		&parsers.ErrorFile{},
		&parsers.ConfigSnippet{},
		&extra.UnProcessed{},
	)
}

// CreateListenParsers creates parsers for listen sections.
// Uses wrapper parsers for http-request and filter to capture EE directives.
func (f *DefaultFactory) CreateListenParsers() *parser.Parsers {
	return createParsers(
		&parsers.Mode{},
		&parsers.Bind{},
		&parsers.Balance{},
		&parsers.Server{},
		&parsers.DefaultServer{},
		&parsers.OptionHttpchk{},
		&parsers.HTTPCheckV2{},
		&parsers.ACL{},
		eeparsers.NewHTTPRequests(), // Wrapper for EE http-request actions
		eeparsers.NewFilters(),      // Wrapper for EE filters
		&tcp.Requests{},
		&tcp.Responses{},
		&parsers.Log{},
		&parsers.MaxConn{},
		&parsers.HTTPReuse{},
		&parsers.Cookie{},
		&parsers.StickTable{},
		&parsers.OptionForwardFor{},
		&parsers.OptionHTTPLog{},
		&parsers.ConfigSnippet{},
		&extra.UnProcessed{},
	)
}

// CreateResolversParsers creates parsers for resolvers sections.
func (f *DefaultFactory) CreateResolversParsers() *parser.Parsers {
	return createParsers(
		&parsers.Nameserver{},
		&parsers.ConfigSnippet{},
		&extra.UnProcessed{},
	)
}

// CreatePeersParsers creates parsers for peers sections.
func (f *DefaultFactory) CreatePeersParsers() *parser.Parsers {
	return createParsers(
		&parsers.Peer{},
		&parsers.Table{},
		&parsers.ConfigSnippet{},
		&extra.UnProcessed{},
	)
}

// CreateMailersParsers creates parsers for mailers sections.
func (f *DefaultFactory) CreateMailersParsers() *parser.Parsers {
	return createParsers(
		&parsers.Mailer{},
		&parsers.ConfigSnippet{},
		&extra.UnProcessed{},
	)
}

// CreateCacheParsers creates parsers for cache sections.
func (f *DefaultFactory) CreateCacheParsers() *parser.Parsers {
	return createParsers(
		&parsers.ProcessVary{},
		&parsers.ConfigSnippet{},
		&extra.UnProcessed{},
	)
}

// CreateProgramParsers creates parsers for program sections.
func (f *DefaultFactory) CreateProgramParsers() *parser.Parsers {
	return createParsers(
		&parsers.Command{},
		&parsers.User{},
		&parsers.Group{},
		&parsers.ConfigSnippet{},
		&extra.UnProcessed{},
	)
}

// CreateHTTPErrorsParsers creates parsers for http-errors sections.
func (f *DefaultFactory) CreateHTTPErrorsParsers() *parser.Parsers {
	return createParsers(
		&parsers.ErrorFile{},
		&parsers.ConfigSnippet{},
		&extra.UnProcessed{},
	)
}

// CreateRingParsers creates parsers for ring sections.
func (f *DefaultFactory) CreateRingParsers() *parser.Parsers {
	return createParsers(
		&parsers.Server{},
		&parsers.ConfigSnippet{},
		&extra.UnProcessed{},
	)
}

// CreateLogForwardParsers creates parsers for log-forward sections.
func (f *DefaultFactory) CreateLogForwardParsers() *parser.Parsers {
	return createParsers(
		&parsers.DgramBind{},
		&parsers.Log{},
		&parsers.MaxConn{},
		&parsers.ConfigSnippet{},
		&extra.UnProcessed{},
	)
}

// CreateFCGIAppParsers creates parsers for fcgi-app sections.
func (f *DefaultFactory) CreateFCGIAppParsers() *parser.Parsers {
	return createParsers(
		&parsers.ACL{},
		&parsers.SetParam{},
		&parsers.PassHeader{},
		&parsers.LogStdErr{},
		&parsers.ConfigSnippet{},
		&extra.UnProcessed{},
	)
}

// CreateCrtStoreParsers creates parsers for crt-store sections.
func (f *DefaultFactory) CreateCrtStoreParsers() *parser.Parsers {
	return createParsers(
		&parsers.ConfigSnippet{},
		&extra.UnProcessed{},
	)
}

// CreateTracesParsers creates parsers for traces sections.
func (f *DefaultFactory) CreateTracesParsers() *parser.Parsers {
	return createParsers(
		&parsers.ConfigSnippet{},
		&extra.UnProcessed{},
	)
}

// CreateLogProfileParsers creates parsers for log-profile sections.
func (f *DefaultFactory) CreateLogProfileParsers() *parser.Parsers {
	return createParsers(
		&parsers.ConfigSnippet{},
		&extra.UnProcessed{},
	)
}

// CreateACMEParsers creates parsers for acme sections.
func (f *DefaultFactory) CreateACMEParsers() *parser.Parsers {
	return createParsers(
		&parsers.ConfigSnippet{},
		&extra.UnProcessed{},
	)
}

// CreateUserlistParsers creates parsers for userlist sections.
func (f *DefaultFactory) CreateUserlistParsers() *parser.Parsers {
	return createParsers(
		&parsers.User{},
		&parsers.Group{},
		&parsers.ConfigSnippet{},
		&extra.UnProcessed{},
	)
}

// --- EE Singleton Section Factories ---

// CreateWAFGlobalParsers creates parsers for waf-global sections.
func (f *DefaultFactory) CreateWAFGlobalParsers() *parser.Parsers {
	return createParsers(
		&extra.UnProcessed{}, // Fallback captures all for now
	)
}

// --- EE Named Section Factories ---

// CreateWAFProfileParsers creates parsers for waf-profile sections.
func (f *DefaultFactory) CreateWAFProfileParsers() *parser.Parsers {
	return createParsers(
		&extra.UnProcessed{},
	)
}

// CreateBotMgmtProfileParsers creates parsers for botmgmt-profile sections.
func (f *DefaultFactory) CreateBotMgmtProfileParsers() *parser.Parsers {
	return createParsers(
		&extra.UnProcessed{},
	)
}

// CreateCaptchaParsers creates parsers for captcha sections.
func (f *DefaultFactory) CreateCaptchaParsers() *parser.Parsers {
	return createParsers(
		&extra.UnProcessed{},
	)
}

// CreateUDPLBParsers creates parsers for udp-lb sections.
func (f *DefaultFactory) CreateUDPLBParsers() *parser.Parsers {
	return createParsers(
		&parsers.Balance{},
		&parsers.Server{},
		&parsers.ACL{},
		&parsers.DefaultServer{},
		&parsers.DgramBind{},
		&extra.UnProcessed{},
	)
}

// CreateDynamicUpdateParsers creates parsers for dynamic-update sections.
func (f *DefaultFactory) CreateDynamicUpdateParsers() *parser.Parsers {
	return createParsers(
		&extra.UnProcessed{},
	)
}

// Ensure DefaultFactory implements ParserFactory.
var _ ParserFactory = (*DefaultFactory)(nil)

// SectionHeader parses section headers (e.g., "frontend foo", "backend bar").
// Used by the reader to detect section transitions.
type SectionHeader struct {
	data        *SectionHeaderData
	preComments []string
}

// SectionHeaderData holds parsed section header information.
type SectionHeaderData struct {
	Type    Section
	Name    string
	Comment string
}

// Init initializes the parser.
func (p *SectionHeader) Init() {
	p.data = nil
	p.preComments = nil
}

// GetParserName returns the parser name.
func (p *SectionHeader) GetParserName() string {
	return "section"
}

// Parse parses a section header line.
func (p *SectionHeader) Parse(line string, parts []string, comment string) (string, error) {
	if len(parts) == 0 {
		return "", &errors.ParseError{Parser: "section", Line: line}
	}

	section := Section(parts[0])
	if !IsAnySection(section) {
		return "", &errors.ParseError{Parser: "section", Line: line}
	}

	name := ""
	if len(parts) > 1 {
		name = parts[1]
	}

	// Named sections require a name
	if IsNamedSection(section) && name == "" {
		return "", &errors.ParseError{Parser: "section", Line: line, Message: "section requires name"}
	}

	p.data = &SectionHeaderData{
		Type:    section,
		Name:    name,
		Comment: comment,
	}

	return string(section), nil
}

// PreParse is called before Parse for preprocessor handling.
func (p *SectionHeader) PreParse(line string, parts, preComments []string, comment string) (string, error) {
	p.preComments = preComments
	return p.Parse(line, parts, comment)
}

// Get returns all parsed data.
func (p *SectionHeader) Get(createIfNotExist bool) (interface{}, error) {
	if p.data == nil {
		return nil, errors.ErrFetch
	}
	return p.data, nil
}

// GetPreComments returns pre-comments.
func (p *SectionHeader) GetPreComments() ([]string, error) {
	return p.preComments, nil
}

// GetOne returns parsed data at index.
func (p *SectionHeader) GetOne(index int) (interface{}, error) {
	if index != 0 || p.data == nil {
		return nil, errors.ErrFetch
	}
	return p.data, nil
}

// Delete deletes data at index.
func (p *SectionHeader) Delete(index int) error {
	if index == 0 {
		p.data = nil
	}
	return nil
}

// Insert inserts data at index.
func (p *SectionHeader) Insert(data interface{}, index int) error {
	if d, ok := data.(*SectionHeaderData); ok {
		p.data = d
	}
	return nil
}

// Set sets data at index.
func (p *SectionHeader) Set(data interface{}, index int) error {
	return p.Insert(data, index)
}

// SetPreComments sets pre-comments.
func (p *SectionHeader) SetPreComments(preComment []string) {
	p.preComments = preComment
}

// ResultAll returns all results for serialization.
func (p *SectionHeader) ResultAll() (results []interface{}, preComments []string, err error) {
	if p.data == nil {
		return nil, nil, nil
	}
	return []interface{}{p.data}, p.preComments, nil
}
