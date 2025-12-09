//go:build integration

package integration

import (
	"testing"
)

// TestSyncEnterpriseSections tests parser -> comparator -> executor flow for all EE sections.
// Uses table-driven approach to avoid code duplication across section types.
//
// This replaces the previous low-value API wrapper tests with comprehensive sync tests
// that exercise the full flow: parse config -> compare -> generate operations -> execute.
func TestSyncEnterpriseSections(t *testing.T) {
	t.Parallel()

	// Define test cases for ALL EE section types in a single table
	// Note: Each feature has its own base config with the required module loaded
	testCases := []syncTestCase{
		// === Bot Management Profiles ===
		// Uses botmgmt-base.cfg which loads hapee-lb-botmgmt.so
		{
			name:              "botmgmt-profile/create",
			initialConfigFile: "enterprise/botmgmt-base.cfg",
			desiredConfigFile: "enterprise/botmgmt-profile/one-profile.cfg",
			expectedCreates:   1,
			expectedUpdates:   0,
			expectedDeletes:   0,
			expectedOperations: []string{
				"Create botmgmt-profile 'bot-detection'",
			},
			expectedReload: true,
			skipFunc:       skipIfBotManagementSyncNotSupported,
		},
		{
			name:              "botmgmt-profile/update",
			initialConfigFile: "enterprise/botmgmt-profile/one-profile.cfg",
			desiredConfigFile: "enterprise/botmgmt-profile/updated-profile.cfg",
			expectedCreates:   0,
			expectedUpdates:   1,
			expectedDeletes:   0,
			expectedOperations: []string{
				"Update botmgmt-profile 'bot-detection'",
			},
			expectedReload: true,
			skipFunc:       skipIfBotManagementSyncNotSupported,
		},
		{
			name:              "botmgmt-profile/delete",
			initialConfigFile: "enterprise/botmgmt-profile/one-profile.cfg",
			desiredConfigFile: "enterprise/botmgmt-base.cfg",
			expectedCreates:   0,
			expectedUpdates:   0,
			expectedDeletes:   1,
			expectedOperations: []string{
				"Delete botmgmt-profile 'bot-detection'",
			},
			expectedReload: true,
			skipFunc:       skipIfBotManagementSyncNotSupported,
		},

		// === Captchas ===
		// Uses captcha-base.cfg which loads hapee-lb-captcha.so (separate from botmgmt module)
		// Does NOT require HAPEE_KEY since captcha doesn't need the botmgmt data file
		{
			name:              "captcha/create",
			initialConfigFile: "enterprise/captcha-base.cfg",
			desiredConfigFile: "enterprise/captcha/one-captcha.cfg",
			expectedCreates:   1,
			expectedUpdates:   0,
			expectedDeletes:   0,
			expectedOperations: []string{
				"Create captcha 'recaptcha'",
			},
			expectedReload: true,
			skipFunc:       skipIfCaptchaNotSupported,
		},
		{
			name:              "captcha/update",
			initialConfigFile: "enterprise/captcha/one-captcha.cfg",
			desiredConfigFile: "enterprise/captcha/updated-captcha.cfg",
			expectedCreates:   0,
			expectedUpdates:   1,
			expectedDeletes:   0,
			expectedOperations: []string{
				"Update captcha 'recaptcha'",
			},
			expectedReload: true,
			skipFunc:       skipIfCaptchaNotSupported,
		},
		{
			name:              "captcha/delete",
			initialConfigFile: "enterprise/captcha/one-captcha.cfg",
			desiredConfigFile: "enterprise/captcha-base.cfg",
			expectedCreates:   0,
			expectedUpdates:   0,
			expectedDeletes:   1,
			expectedOperations: []string{
				"Delete captcha 'recaptcha'",
			},
			expectedReload: true,
			skipFunc:       skipIfCaptchaNotSupported,
		},

		// === WAF Profiles (v3.2+ EE only) ===
		// Uses waf-base.cfg which loads hapee-lb-waf.so
		{
			name:              "waf-profile/create",
			initialConfigFile: "enterprise/waf-base.cfg",
			desiredConfigFile: "enterprise/waf-profile/one-profile.cfg",
			expectedCreates:   1,
			expectedUpdates:   0,
			expectedDeletes:   0,
			expectedOperations: []string{
				"Create waf-profile 'web-security'",
			},
			expectedReload: true,
			skipFunc:       skipIfWAFProfilesNotSupported,
		},
		{
			name:              "waf-profile/update",
			initialConfigFile: "enterprise/waf-profile/one-profile.cfg",
			desiredConfigFile: "enterprise/waf-profile/updated-profile.cfg",
			expectedCreates:   0,
			expectedUpdates:   1,
			expectedDeletes:   0,
			expectedOperations: []string{
				"Update waf-profile 'web-security'",
			},
			expectedReload: true,
			skipFunc:       skipIfWAFProfilesNotSupported,
		},
		{
			name:              "waf-profile/delete",
			initialConfigFile: "enterprise/waf-profile/one-profile.cfg",
			desiredConfigFile: "enterprise/waf-base.cfg",
			expectedCreates:   0,
			expectedUpdates:   0,
			expectedDeletes:   1,
			expectedOperations: []string{
				"Delete waf-profile 'web-security'",
			},
			expectedReload: true,
			skipFunc:       skipIfWAFProfilesNotSupported,
		},

		// === WAF Global (v3.2+ EE, singleton) ===
		// Uses waf-base.cfg which loads hapee-lb-waf.so
		{
			name:              "waf-global/create",
			initialConfigFile: "enterprise/waf-base.cfg",
			desiredConfigFile: "enterprise/waf-global/with-global.cfg",
			expectedCreates:   1,
			expectedUpdates:   0,
			expectedDeletes:   0,
			expectedOperations: []string{
				"Create waf-global configuration",
			},
			expectedReload: true,
			skipFunc:       skipIfWAFGlobalNotSupported,
		},
		{
			name:              "waf-global/update",
			initialConfigFile: "enterprise/waf-global/with-global.cfg",
			desiredConfigFile: "enterprise/waf-global/updated-global.cfg",
			expectedCreates:   0,
			expectedUpdates:   1,
			expectedDeletes:   0,
			expectedOperations: []string{
				"Update waf-global configuration",
			},
			expectedReload: true,
			skipFunc:       skipIfWAFGlobalNotSupported,
		},
		{
			name:              "waf-global/delete",
			initialConfigFile: "enterprise/waf-global/with-global.cfg",
			desiredConfigFile: "enterprise/waf-base.cfg",
			expectedCreates:   0,
			expectedUpdates:   0,
			expectedDeletes:   1,
			expectedOperations: []string{
				"Delete waf-global configuration",
			},
			expectedReload: true,
			skipFunc:       skipIfWAFGlobalNotSupported,
		},
	}

	// Single loop executes all test cases - no code duplication
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			runSyncTest(t, tc) // Reuses existing integration test infrastructure
		})
	}
}
