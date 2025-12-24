package comparator

import (
	"testing"

	"gitlab.com/haproxy-template-ic/haproxy-template-ingress-controller/pkg/dataplane/comparator/sections"
	"gitlab.com/haproxy-template-ic/haproxy-template-ingress-controller/pkg/dataplane/parser"
	v32ee "gitlab.com/haproxy-template-ic/haproxy-template-ingress-controller/pkg/generated/dataplaneapi/v32ee"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// =============================================================================
// Tests for eeModelEqual[T]()
// =============================================================================

func TestEEModelEqual_BothNil(t *testing.T) {
	var a, b *v32ee.BotmgmtProfile
	assert.True(t, eeModelEqual(a, b), "two nil pointers should be equal")
}

func TestEEModelEqual_OneNil(t *testing.T) {
	profile := &v32ee.BotmgmtProfile{Name: "test"}
	assert.False(t, eeModelEqual(profile, nil), "non-nil vs nil should not be equal")
	assert.False(t, eeModelEqual(nil, profile), "nil vs non-nil should not be equal")
}

func TestEEModelEqual_SameValues(t *testing.T) {
	scoreVersion := 2
	a := &v32ee.BotmgmtProfile{Name: "test", ScoreVersion: &scoreVersion}
	b := &v32ee.BotmgmtProfile{Name: "test", ScoreVersion: &scoreVersion}
	assert.True(t, eeModelEqual(a, b), "identical values should be equal")
}

func TestEEModelEqual_DifferentValues(t *testing.T) {
	scoreV1, scoreV2 := 1, 2
	a := &v32ee.BotmgmtProfile{Name: "test", ScoreVersion: &scoreV1}
	b := &v32ee.BotmgmtProfile{Name: "test", ScoreVersion: &scoreV2}
	assert.False(t, eeModelEqual(a, b), "different values should not be equal")
}

func TestEEModelEqual_DifferentNames(t *testing.T) {
	a := &v32ee.BotmgmtProfile{Name: "profile1"}
	b := &v32ee.BotmgmtProfile{Name: "profile2"}
	assert.False(t, eeModelEqual(a, b), "different names should not be equal")
}

func TestEEModelEqual_WafGlobal(t *testing.T) {
	cache1, cache2 := 1000, 2000
	a := &v32ee.WafGlobal{AnalyzerCache: &cache1}
	b := &v32ee.WafGlobal{AnalyzerCache: &cache2}
	assert.False(t, eeModelEqual(a, b), "different analyzer_cache should not be equal")

	a.AnalyzerCache = &cache1
	b.AnalyzerCache = &cache1
	assert.True(t, eeModelEqual(a, b), "same analyzer_cache should be equal")
}

// =============================================================================
// Tests for compareNamedSections[T]() with EE types
// =============================================================================

func TestCompareNamedSections_AddedProfiles(t *testing.T) {
	current := []*v32ee.BotmgmtProfile{}
	desired := []*v32ee.BotmgmtProfile{
		{Name: "profile1"},
		{Name: "profile2"},
	}

	ops := compareNamedSections(
		current,
		desired,
		func(p *v32ee.BotmgmtProfile) string { return p.Name },
		eeModelEqual[v32ee.BotmgmtProfile],
		func(p *v32ee.BotmgmtProfile) Operation { return sections.NewBotMgmtProfileCreate(p) },
		func(p *v32ee.BotmgmtProfile) Operation { return sections.NewBotMgmtProfileDelete(p) },
		func(p *v32ee.BotmgmtProfile) Operation { return sections.NewBotMgmtProfileUpdate(p) },
	)

	require.Len(t, ops, 2, "should generate 2 create operations")
	for _, op := range ops {
		assert.Equal(t, sections.OperationCreate, op.Type(), "all operations should be CREATE")
		assert.Equal(t, "botmgmt-profile", op.Section())
	}
}

func TestCompareNamedSections_DeletedProfiles(t *testing.T) {
	current := []*v32ee.BotmgmtProfile{
		{Name: "profile1"},
		{Name: "profile2"},
	}
	desired := []*v32ee.BotmgmtProfile{}

	ops := compareNamedSections(
		current,
		desired,
		func(p *v32ee.BotmgmtProfile) string { return p.Name },
		eeModelEqual[v32ee.BotmgmtProfile],
		func(p *v32ee.BotmgmtProfile) Operation { return sections.NewBotMgmtProfileCreate(p) },
		func(p *v32ee.BotmgmtProfile) Operation { return sections.NewBotMgmtProfileDelete(p) },
		func(p *v32ee.BotmgmtProfile) Operation { return sections.NewBotMgmtProfileUpdate(p) },
	)

	require.Len(t, ops, 2, "should generate 2 delete operations")
	for _, op := range ops {
		assert.Equal(t, sections.OperationDelete, op.Type(), "all operations should be DELETE")
	}
}

func TestCompareNamedSections_UpdatedProfiles(t *testing.T) {
	scoreV1, scoreV2 := 1, 2
	current := []*v32ee.BotmgmtProfile{
		{Name: "profile1", ScoreVersion: &scoreV1},
	}
	desired := []*v32ee.BotmgmtProfile{
		{Name: "profile1", ScoreVersion: &scoreV2},
	}

	ops := compareNamedSections(
		current,
		desired,
		func(p *v32ee.BotmgmtProfile) string { return p.Name },
		eeModelEqual[v32ee.BotmgmtProfile],
		func(p *v32ee.BotmgmtProfile) Operation { return sections.NewBotMgmtProfileCreate(p) },
		func(p *v32ee.BotmgmtProfile) Operation { return sections.NewBotMgmtProfileDelete(p) },
		func(p *v32ee.BotmgmtProfile) Operation { return sections.NewBotMgmtProfileUpdate(p) },
	)

	require.Len(t, ops, 1, "should generate 1 update operation")
	assert.Equal(t, sections.OperationUpdate, ops[0].Type())
}

func TestCompareNamedSections_NoChanges(t *testing.T) {
	scoreVersion := 1
	current := []*v32ee.BotmgmtProfile{
		{Name: "profile1", ScoreVersion: &scoreVersion},
	}
	desired := []*v32ee.BotmgmtProfile{
		{Name: "profile1", ScoreVersion: &scoreVersion},
	}

	ops := compareNamedSections(
		current,
		desired,
		func(p *v32ee.BotmgmtProfile) string { return p.Name },
		eeModelEqual[v32ee.BotmgmtProfile],
		func(p *v32ee.BotmgmtProfile) Operation { return sections.NewBotMgmtProfileCreate(p) },
		func(p *v32ee.BotmgmtProfile) Operation { return sections.NewBotMgmtProfileDelete(p) },
		func(p *v32ee.BotmgmtProfile) Operation { return sections.NewBotMgmtProfileUpdate(p) },
	)

	assert.Empty(t, ops, "should generate no operations when profiles are identical")
}

func TestCompareNamedSections_MixedOperations(t *testing.T) {
	scoreV1, scoreV2 := 1, 2
	current := []*v32ee.BotmgmtProfile{
		{Name: "keep-unchanged", ScoreVersion: &scoreV1},
		{Name: "to-update", ScoreVersion: &scoreV1},
		{Name: "to-delete"},
	}
	desired := []*v32ee.BotmgmtProfile{
		{Name: "keep-unchanged", ScoreVersion: &scoreV1},
		{Name: "to-update", ScoreVersion: &scoreV2},
		{Name: "new-profile"},
	}

	ops := compareNamedSections(
		current,
		desired,
		func(p *v32ee.BotmgmtProfile) string { return p.Name },
		eeModelEqual[v32ee.BotmgmtProfile],
		func(p *v32ee.BotmgmtProfile) Operation { return sections.NewBotMgmtProfileCreate(p) },
		func(p *v32ee.BotmgmtProfile) Operation { return sections.NewBotMgmtProfileDelete(p) },
		func(p *v32ee.BotmgmtProfile) Operation { return sections.NewBotMgmtProfileUpdate(p) },
	)

	// Expect: 1 create (new-profile), 1 delete (to-delete), 1 update (to-update)
	require.Len(t, ops, 3, "should generate 3 operations")

	creates := countOperationsByType(ops, sections.OperationCreate)
	deletes := countOperationsByType(ops, sections.OperationDelete)
	updates := countOperationsByType(ops, sections.OperationUpdate)

	assert.Equal(t, 1, creates, "should have 1 create operation")
	assert.Equal(t, 1, deletes, "should have 1 delete operation")
	assert.Equal(t, 1, updates, "should have 1 update operation")
}

// =============================================================================
// Tests for compareBotMgmtProfiles()
// =============================================================================

func TestCompareBotMgmtProfiles_Create(t *testing.T) {
	current := &parser.StructuredConfig{}
	desired := &parser.StructuredConfig{
		BotMgmtProfiles: []*v32ee.BotmgmtProfile{
			{Name: "bot-profile-1"},
		},
	}

	c := New()
	ops := c.compareBotMgmtProfiles(current, desired)

	require.Len(t, ops, 1)
	assert.Equal(t, sections.OperationCreate, ops[0].Type())
	assert.Equal(t, "botmgmt-profile", ops[0].Section())
}

func TestCompareBotMgmtProfiles_Delete(t *testing.T) {
	current := &parser.StructuredConfig{
		BotMgmtProfiles: []*v32ee.BotmgmtProfile{
			{Name: "bot-profile-1"},
		},
	}
	desired := &parser.StructuredConfig{}

	c := New()
	ops := c.compareBotMgmtProfiles(current, desired)

	require.Len(t, ops, 1)
	assert.Equal(t, sections.OperationDelete, ops[0].Type())
}

func TestCompareBotMgmtProfiles_NoChanges(t *testing.T) {
	scoreVersion := 1
	current := &parser.StructuredConfig{
		BotMgmtProfiles: []*v32ee.BotmgmtProfile{
			{Name: "bot-profile-1", ScoreVersion: &scoreVersion},
		},
	}
	desired := &parser.StructuredConfig{
		BotMgmtProfiles: []*v32ee.BotmgmtProfile{
			{Name: "bot-profile-1", ScoreVersion: &scoreVersion},
		},
	}

	c := New()
	ops := c.compareBotMgmtProfiles(current, desired)

	assert.Empty(t, ops)
}

// =============================================================================
// Tests for compareCaptchas()
// =============================================================================

func TestCompareCaptchas_Create(t *testing.T) {
	current := &parser.StructuredConfig{}
	desired := &parser.StructuredConfig{
		Captchas: []*v32ee.Captcha{
			{Name: "recaptcha"},
		},
	}

	c := New()
	ops := c.compareCaptchas(current, desired)

	require.Len(t, ops, 1)
	assert.Equal(t, sections.OperationCreate, ops[0].Type())
	assert.Equal(t, "captcha", ops[0].Section())
}

func TestCompareCaptchas_Delete(t *testing.T) {
	current := &parser.StructuredConfig{
		Captchas: []*v32ee.Captcha{
			{Name: "recaptcha"},
		},
	}
	desired := &parser.StructuredConfig{}

	c := New()
	ops := c.compareCaptchas(current, desired)

	require.Len(t, ops, 1)
	assert.Equal(t, sections.OperationDelete, ops[0].Type())
}

func TestCompareCaptchas_Update(t *testing.T) {
	apiKey1, apiKey2 := "key1", "key2"
	current := &parser.StructuredConfig{
		Captchas: []*v32ee.Captcha{
			{Name: "recaptcha", ApiKey: &apiKey1},
		},
	}
	desired := &parser.StructuredConfig{
		Captchas: []*v32ee.Captcha{
			{Name: "recaptcha", ApiKey: &apiKey2},
		},
	}

	c := New()
	ops := c.compareCaptchas(current, desired)

	require.Len(t, ops, 1)
	assert.Equal(t, sections.OperationUpdate, ops[0].Type())
}

// =============================================================================
// Tests for compareWAFProfiles()
// =============================================================================

func TestCompareWAFProfiles_Create(t *testing.T) {
	current := &parser.StructuredConfig{}
	desired := &parser.StructuredConfig{
		WAFProfiles: []*v32ee.WafProfile{
			{Name: "waf-default"},
		},
	}

	c := New()
	ops := c.compareWAFProfiles(current, desired)

	require.Len(t, ops, 1)
	assert.Equal(t, sections.OperationCreate, ops[0].Type())
	assert.Equal(t, "waf-profile", ops[0].Section())
}

func TestCompareWAFProfiles_Delete(t *testing.T) {
	current := &parser.StructuredConfig{
		WAFProfiles: []*v32ee.WafProfile{
			{Name: "waf-default"},
		},
	}
	desired := &parser.StructuredConfig{}

	c := New()
	ops := c.compareWAFProfiles(current, desired)

	require.Len(t, ops, 1)
	assert.Equal(t, sections.OperationDelete, ops[0].Type())
}

func TestCompareWAFProfiles_Update(t *testing.T) {
	learningOn, learningOff := true, false
	current := &parser.StructuredConfig{
		WAFProfiles: []*v32ee.WafProfile{
			{Name: "waf-default", LearningMode: &learningOn},
		},
	}
	desired := &parser.StructuredConfig{
		WAFProfiles: []*v32ee.WafProfile{
			{Name: "waf-default", LearningMode: &learningOff},
		},
	}

	c := New()
	ops := c.compareWAFProfiles(current, desired)

	require.Len(t, ops, 1)
	assert.Equal(t, sections.OperationUpdate, ops[0].Type())
}

// =============================================================================
// Tests for compareWAFGlobal() - singleton section
// =============================================================================

func TestCompareWAFGlobal_CreateFromNil(t *testing.T) {
	cache := 1000
	current := &parser.StructuredConfig{
		WAFGlobal: nil,
	}
	desired := &parser.StructuredConfig{
		WAFGlobal: &v32ee.WafGlobal{AnalyzerCache: &cache},
	}

	c := New()
	ops := c.compareWAFGlobal(current, desired)

	require.Len(t, ops, 1)
	assert.Equal(t, sections.OperationCreate, ops[0].Type())
	assert.Equal(t, "waf-global", ops[0].Section())
}

func TestCompareWAFGlobal_DeleteToNil(t *testing.T) {
	cache := 1000
	current := &parser.StructuredConfig{
		WAFGlobal: &v32ee.WafGlobal{AnalyzerCache: &cache},
	}
	desired := &parser.StructuredConfig{
		WAFGlobal: nil,
	}

	c := New()
	ops := c.compareWAFGlobal(current, desired)

	require.Len(t, ops, 1)
	assert.Equal(t, sections.OperationDelete, ops[0].Type())
}

func TestCompareWAFGlobal_Update(t *testing.T) {
	cache1, cache2 := 1000, 2000
	current := &parser.StructuredConfig{
		WAFGlobal: &v32ee.WafGlobal{AnalyzerCache: &cache1},
	}
	desired := &parser.StructuredConfig{
		WAFGlobal: &v32ee.WafGlobal{AnalyzerCache: &cache2},
	}

	c := New()
	ops := c.compareWAFGlobal(current, desired)

	require.Len(t, ops, 1)
	assert.Equal(t, sections.OperationUpdate, ops[0].Type())
}

func TestCompareWAFGlobal_NoChange(t *testing.T) {
	cache := 1000
	current := &parser.StructuredConfig{
		WAFGlobal: &v32ee.WafGlobal{AnalyzerCache: &cache},
	}
	desired := &parser.StructuredConfig{
		WAFGlobal: &v32ee.WafGlobal{AnalyzerCache: &cache},
	}

	c := New()
	ops := c.compareWAFGlobal(current, desired)

	assert.Empty(t, ops)
}

func TestCompareWAFGlobal_BothNil(t *testing.T) {
	current := &parser.StructuredConfig{WAFGlobal: nil}
	desired := &parser.StructuredConfig{WAFGlobal: nil}

	c := New()
	ops := c.compareWAFGlobal(current, desired)

	assert.Empty(t, ops)
}

// =============================================================================
// Tests for compareEnterpriseSections() integration
// =============================================================================

func TestCompareEnterpriseSections_AllTypes(t *testing.T) {
	scoreVersion := 1
	cache := 1000

	current := &parser.StructuredConfig{}
	desired := &parser.StructuredConfig{
		BotMgmtProfiles: []*v32ee.BotmgmtProfile{{Name: "bot-1", ScoreVersion: &scoreVersion}},
		Captchas:        []*v32ee.Captcha{{Name: "captcha-1"}},
		WAFProfiles:     []*v32ee.WafProfile{{Name: "waf-1"}},
		WAFGlobal:       &v32ee.WafGlobal{AnalyzerCache: &cache},
	}

	c := New()
	ops := c.compareEnterpriseSections(current, desired)

	// Expect: 1 botmgmt-profile create, 1 captcha create, 1 waf-profile create, 1 waf-global create
	require.Len(t, ops, 4)

	sectionTypes := make(map[string]int)
	for _, op := range ops {
		sectionTypes[op.Section()]++
	}

	assert.Equal(t, 1, sectionTypes["botmgmt-profile"])
	assert.Equal(t, 1, sectionTypes["captcha"])
	assert.Equal(t, 1, sectionTypes["waf-profile"])
	assert.Equal(t, 1, sectionTypes["waf-global"])
}

// =============================================================================
// Helper functions
// =============================================================================

func countOperationsByType(ops []Operation, opType sections.OperationType) int {
	count := 0
	for _, op := range ops {
		if op.Type() == opType {
			count++
		}
	}
	return count
}
