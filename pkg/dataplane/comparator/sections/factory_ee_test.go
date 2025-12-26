package sections

import (
	"testing"

	v32ee "gitlab.com/haproxy-haptic/haptic/pkg/generated/dataplaneapi/v32ee"

	"github.com/stretchr/testify/assert"
)

func TestBotMgmtProfileFactoryFunctions(t *testing.T) {
	profile := &v32ee.BotmgmtProfile{Name: "bot-profile-1"}

	tests := []struct {
		name             string
		factory          func(*v32ee.BotmgmtProfile) Operation
		wantType         OperationType
		wantSection      string
		wantPriority     int
		wantDescContains string
	}{
		{
			name:             "NewBotMgmtProfileCreate",
			factory:          NewBotMgmtProfileCreate,
			wantType:         OperationCreate,
			wantSection:      "botmgmt-profile",
			wantPriority:     PriorityEEBotMgmtProfile,
			wantDescContains: "Create botmgmt-profile 'bot-profile-1'",
		},
		{
			name:             "NewBotMgmtProfileUpdate",
			factory:          NewBotMgmtProfileUpdate,
			wantType:         OperationUpdate,
			wantSection:      "botmgmt-profile",
			wantPriority:     PriorityEEBotMgmtProfile,
			wantDescContains: "Update botmgmt-profile 'bot-profile-1'",
		},
		{
			name:             "NewBotMgmtProfileDelete",
			factory:          NewBotMgmtProfileDelete,
			wantType:         OperationDelete,
			wantSection:      "botmgmt-profile",
			wantPriority:     PriorityEEBotMgmtProfile,
			wantDescContains: "Delete botmgmt-profile 'bot-profile-1'",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			op := tt.factory(profile)
			assertOperation(t, op, tt.wantType, tt.wantSection, tt.wantPriority, tt.wantDescContains)
		})
	}
}

func TestCaptchaFactoryFunctions(t *testing.T) {
	captcha := &v32ee.Captcha{Name: "recaptcha-v3"}

	tests := []struct {
		name             string
		factory          func(*v32ee.Captcha) Operation
		wantType         OperationType
		wantSection      string
		wantPriority     int
		wantDescContains string
	}{
		{
			name:             "NewCaptchaCreate",
			factory:          NewCaptchaCreate,
			wantType:         OperationCreate,
			wantSection:      "captcha",
			wantPriority:     PriorityEECaptcha,
			wantDescContains: "Create captcha 'recaptcha-v3'",
		},
		{
			name:             "NewCaptchaUpdate",
			factory:          NewCaptchaUpdate,
			wantType:         OperationUpdate,
			wantSection:      "captcha",
			wantPriority:     PriorityEECaptcha,
			wantDescContains: "Update captcha 'recaptcha-v3'",
		},
		{
			name:             "NewCaptchaDelete",
			factory:          NewCaptchaDelete,
			wantType:         OperationDelete,
			wantSection:      "captcha",
			wantPriority:     PriorityEECaptcha,
			wantDescContains: "Delete captcha 'recaptcha-v3'",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			op := tt.factory(captcha)
			assertOperation(t, op, tt.wantType, tt.wantSection, tt.wantPriority, tt.wantDescContains)
		})
	}
}

func TestWAFProfileFactoryFunctions(t *testing.T) {
	profile := &v32ee.WafProfile{Name: "waf-default"}

	tests := []struct {
		name             string
		factory          func(*v32ee.WafProfile) Operation
		wantType         OperationType
		wantSection      string
		wantPriority     int
		wantDescContains string
	}{
		{
			name:             "NewWAFProfileCreate",
			factory:          NewWAFProfileCreate,
			wantType:         OperationCreate,
			wantSection:      "waf-profile",
			wantPriority:     PriorityEEWAFProfile,
			wantDescContains: "Create waf-profile 'waf-default'",
		},
		{
			name:             "NewWAFProfileUpdate",
			factory:          NewWAFProfileUpdate,
			wantType:         OperationUpdate,
			wantSection:      "waf-profile",
			wantPriority:     PriorityEEWAFProfile,
			wantDescContains: "Update waf-profile 'waf-default'",
		},
		{
			name:             "NewWAFProfileDelete",
			factory:          NewWAFProfileDelete,
			wantType:         OperationDelete,
			wantSection:      "waf-profile",
			wantPriority:     PriorityEEWAFProfile,
			wantDescContains: "Delete waf-profile 'waf-default'",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			op := tt.factory(profile)
			assertOperation(t, op, tt.wantType, tt.wantSection, tt.wantPriority, tt.wantDescContains)
		})
	}
}

func TestWAFGlobalFactoryFunctions(t *testing.T) {
	cache := 1000
	wafGlobal := &v32ee.WafGlobal{AnalyzerCache: &cache}

	tests := []struct {
		name             string
		factory          func(*v32ee.WafGlobal) Operation
		wantType         OperationType
		wantSection      string
		wantPriority     int
		wantDescContains string
	}{
		{
			name:             "NewWAFGlobalCreate",
			factory:          NewWAFGlobalCreate,
			wantType:         OperationCreate,
			wantSection:      "waf-global",
			wantPriority:     PriorityEEWAFGlobal,
			wantDescContains: "Create waf-global configuration",
		},
		{
			name:             "NewWAFGlobalUpdate",
			factory:          NewWAFGlobalUpdate,
			wantType:         OperationUpdate,
			wantSection:      "waf-global",
			wantPriority:     PriorityEEWAFGlobal,
			wantDescContains: "Update waf-global configuration",
		},
		{
			name:             "NewWAFGlobalDelete",
			factory:          NewWAFGlobalDelete,
			wantType:         OperationDelete,
			wantSection:      "waf-global",
			wantPriority:     PriorityEEWAFGlobal,
			wantDescContains: "Delete waf-global configuration",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			op := tt.factory(wafGlobal)
			assertOperation(t, op, tt.wantType, tt.wantSection, tt.wantPriority, tt.wantDescContains)
		})
	}
}

// =============================================================================
// Tests for EE Helper Functions
// =============================================================================

func TestEENameExtractors(t *testing.T) {
	t.Run("BotMgmtProfileName", func(t *testing.T) {
		profile := &v32ee.BotmgmtProfile{Name: "bot-profile-1"}
		assert.Equal(t, "bot-profile-1", BotMgmtProfileName(profile))
	})

	t.Run("CaptchaEEName", func(t *testing.T) {
		captcha := &v32ee.Captcha{Name: "recaptcha"}
		assert.Equal(t, "recaptcha", CaptchaEEName(captcha))
	})

	t.Run("WAFProfileName", func(t *testing.T) {
		profile := &v32ee.WafProfile{Name: "waf-default"}
		assert.Equal(t, "waf-default", WAFProfileName(profile))
	})
}

func TestEEIdentityTransforms(t *testing.T) {
	t.Run("IdentityBotMgmtProfile", func(t *testing.T) {
		profile := &v32ee.BotmgmtProfile{Name: "test"}
		assert.Same(t, profile, IdentityBotMgmtProfile(profile))
	})

	t.Run("IdentityCaptchaEE", func(t *testing.T) {
		captcha := &v32ee.Captcha{Name: "test"}
		assert.Same(t, captcha, IdentityCaptchaEE(captcha))
	})

	t.Run("IdentityWAFProfile", func(t *testing.T) {
		profile := &v32ee.WafProfile{Name: "test"}
		assert.Same(t, profile, IdentityWAFProfile(profile))
	})

	t.Run("IdentityWAFGlobal", func(t *testing.T) {
		cache := 1000
		wafGlobal := &v32ee.WafGlobal{AnalyzerCache: &cache}
		assert.Same(t, wafGlobal, IdentityWAFGlobal(wafGlobal))
	})
}

func TestEENilTransforms(t *testing.T) {
	t.Run("NilBotMgmtProfile", func(t *testing.T) {
		profile := &v32ee.BotmgmtProfile{Name: "test"}
		assert.Nil(t, NilBotMgmtProfile(profile))
	})

	t.Run("NilCaptchaEE", func(t *testing.T) {
		captcha := &v32ee.Captcha{Name: "test"}
		assert.Nil(t, NilCaptchaEE(captcha))
	})

	t.Run("NilWAFProfile", func(t *testing.T) {
		profile := &v32ee.WafProfile{Name: "test"}
		assert.Nil(t, NilWAFProfile(profile))
	})

	t.Run("NilWAFGlobal", func(t *testing.T) {
		cache := 1000
		wafGlobal := &v32ee.WafGlobal{AnalyzerCache: &cache}
		assert.Nil(t, NilWAFGlobal(wafGlobal))
	})
}

func TestDescribeSingleton(t *testing.T) {
	tests := []struct {
		name     string
		opType   OperationType
		section  string
		expected string
	}{
		{
			name:     "Create waf-global",
			opType:   OperationCreate,
			section:  "waf-global",
			expected: "Create waf-global configuration",
		},
		{
			name:     "Update waf-global",
			opType:   OperationUpdate,
			section:  "waf-global",
			expected: "Update waf-global configuration",
		},
		{
			name:     "Delete waf-global",
			opType:   OperationDelete,
			section:  "waf-global",
			expected: "Delete waf-global configuration",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			describeFn := DescribeSingleton(tt.opType, tt.section)
			assert.Equal(t, tt.expected, describeFn())
		})
	}
}

// =============================================================================
// Tests for WAF Global Singleton Operations
// =============================================================================

func TestWAFGlobalSingleton_Methods(t *testing.T) {
	cache := 1000
	wafGlobal := &v32ee.WafGlobal{AnalyzerCache: &cache}

	op := NewWAFGlobalCreate(wafGlobal)

	assert.Equal(t, OperationCreate, op.Type())
	assert.Equal(t, "waf-global", op.Section())
	assert.Equal(t, PriorityEEWAFGlobal, op.Priority())
	assert.Contains(t, op.Describe(), "waf-global")
}

// =============================================================================
// Tests for Priority Constants
// =============================================================================

func TestEEPriorityConstants(t *testing.T) {
	// WAF global should be created before WAF profiles
	assert.Less(t, PriorityEEWAFGlobal, PriorityEEWAFProfile,
		"WAF global should have lower priority (execute first) than WAF profiles")

	// All EE sections should have reasonable priority values
	assert.Greater(t, PriorityEEBotMgmtProfile, 0)
	assert.Greater(t, PriorityEECaptcha, 0)
	assert.Greater(t, PriorityEEWAFGlobal, 0)
	assert.Greater(t, PriorityEEWAFProfile, 0)
}
