// Copyright 2025 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package validators

import (
	"github.com/haproxytech/client-native/v6/models"

	genvalidators "gitlab.com/haproxy-haptic/haptic/pkg/generated/validators"
)

const (
	apiV30 = "v30"
	apiV31 = "v31"
	apiV32 = "v32"
	apiV33 = "v33"
)

// ValidatorSet provides type-specific validation functions for a HAProxy version.
// Each method validates a model directly without JSON conversion.
type ValidatorSet struct {
	version string

	// Validator functions
	validateServer              func(*models.Server) error
	validateServerTemplate      func(*models.ServerTemplate) error
	validateBind                func(*models.Bind) error
	validateHTTPRequestRule     func(*models.HTTPRequestRule) error
	validateHTTPResponseRule    func(*models.HTTPResponseRule) error
	validateTCPRequestRule      func(*models.TCPRequestRule) error
	validateTCPResponseRule     func(*models.TCPResponseRule) error
	validateHTTPAfterResponse   func(*models.HTTPAfterResponseRule) error
	validateHTTPErrorRule       func(*models.HTTPErrorRule) error
	validateServerSwitchingRule func(*models.ServerSwitchingRule) error
	validateBackendSwitching    func(*models.BackendSwitchingRule) error
	validateStickRule           func(*models.StickRule) error
	validateACL                 func(*models.ACL) error
	validateFilter              func(*models.Filter) error
	validateLogTarget           func(*models.LogTarget) error
	validateHTTPCheck           func(*models.HTTPCheck) error
	validateTCPCheck            func(*models.TCPCheck) error
	validateCapture             func(*models.Capture) error

	// Hasher functions for caching
	hashServer              func(*models.Server) uint64
	hashServerTemplate      func(*models.ServerTemplate) uint64
	hashBind                func(*models.Bind) uint64
	hashHTTPRequestRule     func(*models.HTTPRequestRule) uint64
	hashHTTPResponseRule    func(*models.HTTPResponseRule) uint64
	hashTCPRequestRule      func(*models.TCPRequestRule) uint64
	hashTCPResponseRule     func(*models.TCPResponseRule) uint64
	hashHTTPAfterResponse   func(*models.HTTPAfterResponseRule) uint64
	hashHTTPErrorRule       func(*models.HTTPErrorRule) uint64
	hashServerSwitchingRule func(*models.ServerSwitchingRule) uint64
	hashBackendSwitching    func(*models.BackendSwitchingRule) uint64
	hashStickRule           func(*models.StickRule) uint64
	hashACL                 func(*models.ACL) uint64
	hashFilter              func(*models.Filter) uint64
	hashLogTarget           func(*models.LogTarget) uint64
	hashHTTPCheck           func(*models.HTTPCheck) uint64
	hashTCPCheck            func(*models.TCPCheck) uint64
	hashCapture             func(*models.Capture) uint64
}

// ForVersion returns the ValidatorSet for a specific HAProxy version.
// Returns v33 for 3.3+, v32 for 3.2, v31 for 3.1, v30 for 3.0 and below.
func ForVersion(major, minor int) *ValidatorSet {
	if major < 3 {
		return validatorSetV30
	}
	if major > 3 {
		return validatorSetV33
	}

	switch {
	case minor >= 3:
		return validatorSetV33
	case minor >= 2:
		return validatorSetV32
	case minor >= 1:
		return validatorSetV31
	default:
		return validatorSetV30
	}
}

// Pre-built validator sets for each version.
// These are initialized in init() after the generated validators are loaded.
var (
	validatorSetV30 *ValidatorSet
	validatorSetV31 *ValidatorSet
	validatorSetV32 *ValidatorSet
	validatorSetV33 *ValidatorSet
)

func init() {
	validatorSetV30 = &ValidatorSet{
		version:                     apiV30,
		validateServer:              genvalidators.ValidateServerV30,
		validateServerTemplate:      genvalidators.ValidateServerTemplateV30,
		validateBind:                genvalidators.ValidateBindV30,
		validateHTTPRequestRule:     genvalidators.ValidateHttpRequestRuleV30,
		validateHTTPResponseRule:    genvalidators.ValidateHttpResponseRuleV30,
		validateTCPRequestRule:      genvalidators.ValidateTcpRequestRuleV30,
		validateTCPResponseRule:     genvalidators.ValidateTcpResponseRuleV30,
		validateHTTPAfterResponse:   genvalidators.ValidateHttpAfterResponseRuleV30,
		validateHTTPErrorRule:       genvalidators.ValidateHttpErrorRuleV30,
		validateServerSwitchingRule: genvalidators.ValidateServerSwitchingRuleV30,
		validateBackendSwitching:    genvalidators.ValidateBackendSwitchingRuleV30,
		validateStickRule:           genvalidators.ValidateStickRuleV30,
		validateACL:                 genvalidators.ValidateAclV30,
		validateFilter:              genvalidators.ValidateFilterV30,
		validateLogTarget:           genvalidators.ValidateLogTargetV30,
		validateHTTPCheck:           genvalidators.ValidateHttpCheckV30,
		validateTCPCheck:            genvalidators.ValidateTcpCheckV30,
		validateCapture:             genvalidators.ValidateCaptureV30,
		hashServer:                  genvalidators.HashServerV30,
		hashServerTemplate:          genvalidators.HashServerTemplateV30,
		hashBind:                    genvalidators.HashBindV30,
		hashHTTPRequestRule:         genvalidators.HashHttpRequestRuleV30,
		hashHTTPResponseRule:        genvalidators.HashHttpResponseRuleV30,
		hashTCPRequestRule:          genvalidators.HashTcpRequestRuleV30,
		hashTCPResponseRule:         genvalidators.HashTcpResponseRuleV30,
		hashHTTPAfterResponse:       genvalidators.HashHttpAfterResponseRuleV30,
		hashHTTPErrorRule:           genvalidators.HashHttpErrorRuleV30,
		hashServerSwitchingRule:     genvalidators.HashServerSwitchingRuleV30,
		hashBackendSwitching:        genvalidators.HashBackendSwitchingRuleV30,
		hashStickRule:               genvalidators.HashStickRuleV30,
		hashACL:                     genvalidators.HashAclV30,
		hashFilter:                  genvalidators.HashFilterV30,
		hashLogTarget:               genvalidators.HashLogTargetV30,
		hashHTTPCheck:               genvalidators.HashHttpCheckV30,
		hashTCPCheck:                genvalidators.HashTcpCheckV30,
		hashCapture:                 genvalidators.HashCaptureV30,
	}

	validatorSetV31 = &ValidatorSet{
		version:                     apiV31,
		validateServer:              genvalidators.ValidateServerV31,
		validateServerTemplate:      genvalidators.ValidateServerTemplateV31,
		validateBind:                genvalidators.ValidateBindV31,
		validateHTTPRequestRule:     genvalidators.ValidateHttpRequestRuleV31,
		validateHTTPResponseRule:    genvalidators.ValidateHttpResponseRuleV31,
		validateTCPRequestRule:      genvalidators.ValidateTcpRequestRuleV31,
		validateTCPResponseRule:     genvalidators.ValidateTcpResponseRuleV31,
		validateHTTPAfterResponse:   genvalidators.ValidateHttpAfterResponseRuleV31,
		validateHTTPErrorRule:       genvalidators.ValidateHttpErrorRuleV31,
		validateServerSwitchingRule: genvalidators.ValidateServerSwitchingRuleV31,
		validateBackendSwitching:    genvalidators.ValidateBackendSwitchingRuleV31,
		validateStickRule:           genvalidators.ValidateStickRuleV31,
		validateACL:                 genvalidators.ValidateAclV31,
		validateFilter:              genvalidators.ValidateFilterV31,
		validateLogTarget:           genvalidators.ValidateLogTargetV31,
		validateHTTPCheck:           genvalidators.ValidateHttpCheckV31,
		validateTCPCheck:            genvalidators.ValidateTcpCheckV31,
		validateCapture:             genvalidators.ValidateCaptureV31,
		hashServer:                  genvalidators.HashServerV31,
		hashServerTemplate:          genvalidators.HashServerTemplateV31,
		hashBind:                    genvalidators.HashBindV31,
		hashHTTPRequestRule:         genvalidators.HashHttpRequestRuleV31,
		hashHTTPResponseRule:        genvalidators.HashHttpResponseRuleV31,
		hashTCPRequestRule:          genvalidators.HashTcpRequestRuleV31,
		hashTCPResponseRule:         genvalidators.HashTcpResponseRuleV31,
		hashHTTPAfterResponse:       genvalidators.HashHttpAfterResponseRuleV31,
		hashHTTPErrorRule:           genvalidators.HashHttpErrorRuleV31,
		hashServerSwitchingRule:     genvalidators.HashServerSwitchingRuleV31,
		hashBackendSwitching:        genvalidators.HashBackendSwitchingRuleV31,
		hashStickRule:               genvalidators.HashStickRuleV31,
		hashACL:                     genvalidators.HashAclV31,
		hashFilter:                  genvalidators.HashFilterV31,
		hashLogTarget:               genvalidators.HashLogTargetV31,
		hashHTTPCheck:               genvalidators.HashHttpCheckV31,
		hashTCPCheck:                genvalidators.HashTcpCheckV31,
		hashCapture:                 genvalidators.HashCaptureV31,
	}

	validatorSetV32 = &ValidatorSet{
		version:                     apiV32,
		validateServer:              genvalidators.ValidateServerV32,
		validateServerTemplate:      genvalidators.ValidateServerTemplateV32,
		validateBind:                genvalidators.ValidateBindV32,
		validateHTTPRequestRule:     genvalidators.ValidateHttpRequestRuleV32,
		validateHTTPResponseRule:    genvalidators.ValidateHttpResponseRuleV32,
		validateTCPRequestRule:      genvalidators.ValidateTcpRequestRuleV32,
		validateTCPResponseRule:     genvalidators.ValidateTcpResponseRuleV32,
		validateHTTPAfterResponse:   genvalidators.ValidateHttpAfterResponseRuleV32,
		validateHTTPErrorRule:       genvalidators.ValidateHttpErrorRuleV32,
		validateServerSwitchingRule: genvalidators.ValidateServerSwitchingRuleV32,
		validateBackendSwitching:    genvalidators.ValidateBackendSwitchingRuleV32,
		validateStickRule:           genvalidators.ValidateStickRuleV32,
		validateACL:                 genvalidators.ValidateAclV32,
		validateFilter:              genvalidators.ValidateFilterV32,
		validateLogTarget:           genvalidators.ValidateLogTargetV32,
		validateHTTPCheck:           genvalidators.ValidateHttpCheckV32,
		validateTCPCheck:            genvalidators.ValidateTcpCheckV32,
		validateCapture:             genvalidators.ValidateCaptureV32,
		hashServer:                  genvalidators.HashServerV32,
		hashServerTemplate:          genvalidators.HashServerTemplateV32,
		hashBind:                    genvalidators.HashBindV32,
		hashHTTPRequestRule:         genvalidators.HashHttpRequestRuleV32,
		hashHTTPResponseRule:        genvalidators.HashHttpResponseRuleV32,
		hashTCPRequestRule:          genvalidators.HashTcpRequestRuleV32,
		hashTCPResponseRule:         genvalidators.HashTcpResponseRuleV32,
		hashHTTPAfterResponse:       genvalidators.HashHttpAfterResponseRuleV32,
		hashHTTPErrorRule:           genvalidators.HashHttpErrorRuleV32,
		hashServerSwitchingRule:     genvalidators.HashServerSwitchingRuleV32,
		hashBackendSwitching:        genvalidators.HashBackendSwitchingRuleV32,
		hashStickRule:               genvalidators.HashStickRuleV32,
		hashACL:                     genvalidators.HashAclV32,
		hashFilter:                  genvalidators.HashFilterV32,
		hashLogTarget:               genvalidators.HashLogTargetV32,
		hashHTTPCheck:               genvalidators.HashHttpCheckV32,
		hashTCPCheck:                genvalidators.HashTcpCheckV32,
		hashCapture:                 genvalidators.HashCaptureV32,
	}

	validatorSetV33 = &ValidatorSet{
		version:                     apiV33,
		validateServer:              genvalidators.ValidateServerV33,
		validateServerTemplate:      genvalidators.ValidateServerTemplateV33,
		validateBind:                genvalidators.ValidateBindV33,
		validateHTTPRequestRule:     genvalidators.ValidateHttpRequestRuleV33,
		validateHTTPResponseRule:    genvalidators.ValidateHttpResponseRuleV33,
		validateTCPRequestRule:      genvalidators.ValidateTcpRequestRuleV33,
		validateTCPResponseRule:     genvalidators.ValidateTcpResponseRuleV33,
		validateHTTPAfterResponse:   genvalidators.ValidateHttpAfterResponseRuleV33,
		validateHTTPErrorRule:       genvalidators.ValidateHttpErrorRuleV33,
		validateServerSwitchingRule: genvalidators.ValidateServerSwitchingRuleV33,
		validateBackendSwitching:    genvalidators.ValidateBackendSwitchingRuleV33,
		validateStickRule:           genvalidators.ValidateStickRuleV33,
		validateACL:                 genvalidators.ValidateAclV33,
		validateFilter:              genvalidators.ValidateFilterV33,
		validateLogTarget:           genvalidators.ValidateLogTargetV33,
		validateHTTPCheck:           genvalidators.ValidateHttpCheckV33,
		validateTCPCheck:            genvalidators.ValidateTcpCheckV33,
		validateCapture:             genvalidators.ValidateCaptureV33,
		hashServer:                  genvalidators.HashServerV33,
		hashServerTemplate:          genvalidators.HashServerTemplateV33,
		hashBind:                    genvalidators.HashBindV33,
		hashHTTPRequestRule:         genvalidators.HashHttpRequestRuleV33,
		hashHTTPResponseRule:        genvalidators.HashHttpResponseRuleV33,
		hashTCPRequestRule:          genvalidators.HashTcpRequestRuleV33,
		hashTCPResponseRule:         genvalidators.HashTcpResponseRuleV33,
		hashHTTPAfterResponse:       genvalidators.HashHttpAfterResponseRuleV33,
		hashHTTPErrorRule:           genvalidators.HashHttpErrorRuleV33,
		hashServerSwitchingRule:     genvalidators.HashServerSwitchingRuleV33,
		hashBackendSwitching:        genvalidators.HashBackendSwitchingRuleV33,
		hashStickRule:               genvalidators.HashStickRuleV33,
		hashACL:                     genvalidators.HashAclV33,
		hashFilter:                  genvalidators.HashFilterV33,
		hashLogTarget:               genvalidators.HashLogTargetV33,
		hashHTTPCheck:               genvalidators.HashHttpCheckV33,
		hashTCPCheck:                genvalidators.HashTcpCheckV33,
		hashCapture:                 genvalidators.HashCaptureV33,
	}
}
