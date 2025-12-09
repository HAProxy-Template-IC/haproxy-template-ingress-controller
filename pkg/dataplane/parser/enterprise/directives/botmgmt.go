package directives

import (
	"strconv"

	"github.com/haproxytech/client-native/v6/config-parser/common"
	"github.com/haproxytech/client-native/v6/config-parser/errors"
)

// BotMgmtProfileData holds parsed botmgmt-profile section data.
type BotMgmtProfileData struct {
	Name         string
	ScoreVersion int
	Track        string
	TrackPeers   string
}

// BotMgmtProfile parses botmgmt-profile section directives.
type BotMgmtProfile struct {
	data        *BotMgmtProfileData
	preComments []string
}

// NewBotMgmtProfile creates a new BotMgmtProfile parser.
func NewBotMgmtProfile() *BotMgmtProfile {
	return &BotMgmtProfile{}
}

// Init initializes the parser.
func (p *BotMgmtProfile) Init() {
	p.data = nil
	p.preComments = nil
}

// GetParserName returns the parser name.
func (p *BotMgmtProfile) GetParserName() string {
	return "botmgmt-profile"
}

// Parse parses a botmgmt-profile directive line.
func (p *BotMgmtProfile) Parse(line string, parts []string, comment string) (string, error) {
	if len(parts) < 2 {
		return "", &errors.ParseError{Parser: "botmgmt-profile", Line: line}
	}

	if p.data == nil {
		p.data = &BotMgmtProfileData{}
	}

	directive := parts[0]
	switch directive {
	case "score-version":
		val, err := strconv.Atoi(parts[1])
		if err != nil {
			return "", &errors.ParseError{Parser: "botmgmt-profile", Line: line, Message: "invalid score-version value"}
		}
		p.data.ScoreVersion = val
		return "", nil
	case "track":
		p.data.Track = parts[1]
		return "", nil
	case "track-peers":
		p.data.TrackPeers = parts[1]
		return "", nil
	default:
		return "", &errors.ParseError{Parser: "botmgmt-profile", Line: line, Message: "unknown directive"}
	}
}

// PreParse is called before Parse for preprocessor handling.
func (p *BotMgmtProfile) PreParse(line string, parts, preComments []string, comment string) (string, error) {
	p.preComments = preComments
	return p.Parse(line, parts, comment)
}

// Get returns parsed data.
func (p *BotMgmtProfile) Get(createIfNotExist bool) (common.ParserData, error) {
	if p.data == nil && !createIfNotExist {
		return nil, errors.ErrFetch
	}
	return p.data, nil
}

// GetPreComments returns pre-comments.
func (p *BotMgmtProfile) GetPreComments() ([]string, error) {
	return p.preComments, nil
}

// GetOne returns parsed data at index.
func (p *BotMgmtProfile) GetOne(index int) (common.ParserData, error) {
	if index != 0 || p.data == nil {
		return nil, errors.ErrFetch
	}
	return p.data, nil
}

// Delete deletes data at index.
func (p *BotMgmtProfile) Delete(index int) error {
	if index == 0 {
		p.data = nil
	}
	return nil
}

// Insert inserts data at index.
func (p *BotMgmtProfile) Insert(data common.ParserData, index int) error {
	if d, ok := data.(*BotMgmtProfileData); ok {
		p.data = d
	}
	return nil
}

// Set sets data at index.
func (p *BotMgmtProfile) Set(data common.ParserData, index int) error {
	if d, ok := data.(*BotMgmtProfileData); ok {
		p.data = d
	}
	return nil
}

// SetPreComments sets pre-comments.
func (p *BotMgmtProfile) SetPreComments(preComment []string) {
	p.preComments = preComment
}

// ResultAll returns all results for serialization.
func (p *BotMgmtProfile) ResultAll() ([]common.ReturnResultLine, []string, error) {
	if p.data == nil {
		return nil, p.preComments, nil
	}

	var results []common.ReturnResultLine
	if p.data.ScoreVersion > 0 {
		results = append(results, common.ReturnResultLine{Data: "score-version " + strconv.Itoa(p.data.ScoreVersion)})
	}
	if p.data.Track != "" {
		results = append(results, common.ReturnResultLine{Data: "track " + p.data.Track})
	}
	if p.data.TrackPeers != "" {
		results = append(results, common.ReturnResultLine{Data: "track-peers " + p.data.TrackPeers})
	}

	return results, p.preComments, nil
}

// GetData returns the parsed bot management profile data.
func (p *BotMgmtProfile) GetData() *BotMgmtProfileData {
	return p.data
}
