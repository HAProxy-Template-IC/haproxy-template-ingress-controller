package sections

import (
	"testing"

	v32ee "gitlab.com/haproxy-haptic/haptic/pkg/generated/dataplaneapi/v32ee"

	"github.com/stretchr/testify/assert"
)

func TestBotMgmtProfileFactoryFunctions(t *testing.T) {
	profile := &v32ee.BotmgmtProfile{Name: "bot-profile-1"}

	tests := []struct {
		name        string
		factory     func(*v32ee.BotmgmtProfile) Operation
		wantType    OperationType
		wantSection string

		wantDescContains string
	}{
		{
			name:             "BotMgmtProfileOps.Create",
			factory:          BotMgmtProfileOps.Create,
			wantType:         OperationCreate,
			wantSection:      "botmgmt-profile",
			wantDescContains: "Create botmgmt-profile 'bot-profile-1'",
		},
		{
			name:             "BotMgmtProfileOps.Update",
			factory:          BotMgmtProfileOps.Update,
			wantType:         OperationUpdate,
			wantSection:      "botmgmt-profile",
			wantDescContains: "Update botmgmt-profile 'bot-profile-1'",
		},
		{
			name:             "BotMgmtProfileOps.Delete",
			factory:          BotMgmtProfileOps.Delete,
			wantType:         OperationDelete,
			wantSection:      "botmgmt-profile",
			wantDescContains: "Delete botmgmt-profile 'bot-profile-1'",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			op := tt.factory(profile)
			assertOperation(t, op, tt.wantType, tt.wantSection, tt.wantDescContains)
		})
	}
}

func TestCaptchaFactoryFunctions(t *testing.T) {
	captcha := &v32ee.Captcha{Name: "recaptcha-v3"}

	tests := []struct {
		name        string
		factory     func(*v32ee.Captcha) Operation
		wantType    OperationType
		wantSection string

		wantDescContains string
	}{
		{
			name:             "CaptchaOps.Create",
			factory:          CaptchaOps.Create,
			wantType:         OperationCreate,
			wantSection:      "captcha",
			wantDescContains: "Create captcha 'recaptcha-v3'",
		},
		{
			name:             "CaptchaOps.Update",
			factory:          CaptchaOps.Update,
			wantType:         OperationUpdate,
			wantSection:      "captcha",
			wantDescContains: "Update captcha 'recaptcha-v3'",
		},
		{
			name:             "CaptchaOps.Delete",
			factory:          CaptchaOps.Delete,
			wantType:         OperationDelete,
			wantSection:      "captcha",
			wantDescContains: "Delete captcha 'recaptcha-v3'",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			op := tt.factory(captcha)
			assertOperation(t, op, tt.wantType, tt.wantSection, tt.wantDescContains)
		})
	}
}

func TestWAFProfileFactoryFunctions(t *testing.T) {
	profile := &v32ee.WafProfile{Name: "waf-default"}

	tests := []struct {
		name        string
		factory     func(*v32ee.WafProfile) Operation
		wantType    OperationType
		wantSection string

		wantDescContains string
	}{
		{
			name:             "WafProfileOps.Create",
			factory:          WafProfileOps.Create,
			wantType:         OperationCreate,
			wantSection:      "waf-profile",
			wantDescContains: "Create waf-profile 'waf-default'",
		},
		{
			name:             "WafProfileOps.Update",
			factory:          WafProfileOps.Update,
			wantType:         OperationUpdate,
			wantSection:      "waf-profile",
			wantDescContains: "Update waf-profile 'waf-default'",
		},
		{
			name:             "WafProfileOps.Delete",
			factory:          WafProfileOps.Delete,
			wantType:         OperationDelete,
			wantSection:      "waf-profile",
			wantDescContains: "Delete waf-profile 'waf-default'",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			op := tt.factory(profile)
			assertOperation(t, op, tt.wantType, tt.wantSection, tt.wantDescContains)
		})
	}
}

func TestWAFGlobalFactoryFunctions(t *testing.T) {
	cache := 1000
	wafGlobal := &v32ee.WafGlobal{AnalyzerCache: &cache}

	tests := []struct {
		name        string
		factory     func(*v32ee.WafGlobal) Operation
		wantType    OperationType
		wantSection string

		wantDescContains string
	}{
		{
			name:             "NewWAFGlobalCreate",
			factory:          NewWAFGlobalCreate,
			wantType:         OperationCreate,
			wantSection:      "waf-global",
			wantDescContains: "Create waf-global configuration",
		},
		{
			name:             "NewWAFGlobalUpdate",
			factory:          NewWAFGlobalUpdate,
			wantType:         OperationUpdate,
			wantSection:      "waf-global",
			wantDescContains: "Update waf-global configuration",
		},
		{
			name:             "NewWAFGlobalDelete",
			factory:          NewWAFGlobalDelete,
			wantType:         OperationDelete,
			wantSection:      "waf-global",
			wantDescContains: "Delete waf-global configuration",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			op := tt.factory(wafGlobal)
			assertOperation(t, op, tt.wantType, tt.wantSection, tt.wantDescContains)
		})
	}
}

func TestEENameExtractors(t *testing.T) {
	t.Run("botMgmtProfileName", func(t *testing.T) {
		profile := &v32ee.BotmgmtProfile{Name: "bot-profile-1"}
		assert.Equal(t, "bot-profile-1", botMgmtProfileName(profile))
	})

	t.Run("captchaEEName", func(t *testing.T) {
		captcha := &v32ee.Captcha{Name: "recaptcha"}
		assert.Equal(t, "recaptcha", captchaEEName(captcha))
	})

	t.Run("wafProfileName", func(t *testing.T) {
		profile := &v32ee.WafProfile{Name: "waf-default"}
		assert.Equal(t, "waf-default", wafProfileName(profile))
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
			describeFn := describeSingleton(tt.opType, tt.section)
			assert.Equal(t, tt.expected, describeFn())
		})
	}
}

func TestWAFGlobalSingleton_Methods(t *testing.T) {
	cache := 1000
	wafGlobal := &v32ee.WafGlobal{AnalyzerCache: &cache}

	op := NewWAFGlobalCreate(wafGlobal)

	assert.Equal(t, OperationCreate, op.Type())
	assert.Equal(t, "waf-global", op.Section())
	assert.Contains(t, op.Describe(), "waf-global")
}
