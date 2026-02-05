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

// Version returns the HAProxy version string for this validator set.
func (v *ValidatorSet) Version() string {
	return v.version
}

// ValidateServer validates a Server model.
func (v *ValidatorSet) ValidateServer(m *models.Server) error {
	if v.validateServer != nil {
		return v.validateServer(m)
	}
	return nil
}

// ValidateServerTemplate validates a ServerTemplate model.
func (v *ValidatorSet) ValidateServerTemplate(m *models.ServerTemplate) error {
	if v.validateServerTemplate != nil {
		return v.validateServerTemplate(m)
	}
	return nil
}

// ValidateBind validates a Bind model.
func (v *ValidatorSet) ValidateBind(m *models.Bind) error {
	if v.validateBind != nil {
		return v.validateBind(m)
	}
	return nil
}

// ValidateHTTPRequestRule validates an HTTPRequestRule model.
func (v *ValidatorSet) ValidateHTTPRequestRule(m *models.HTTPRequestRule) error {
	if v.validateHTTPRequestRule != nil {
		return v.validateHTTPRequestRule(m)
	}
	return nil
}

// ValidateHTTPResponseRule validates an HTTPResponseRule model.
func (v *ValidatorSet) ValidateHTTPResponseRule(m *models.HTTPResponseRule) error {
	if v.validateHTTPResponseRule != nil {
		return v.validateHTTPResponseRule(m)
	}
	return nil
}

// ValidateTCPRequestRule validates a TCPRequestRule model.
func (v *ValidatorSet) ValidateTCPRequestRule(m *models.TCPRequestRule) error {
	if v.validateTCPRequestRule != nil {
		return v.validateTCPRequestRule(m)
	}
	return nil
}

// ValidateTCPResponseRule validates a TCPResponseRule model.
func (v *ValidatorSet) ValidateTCPResponseRule(m *models.TCPResponseRule) error {
	if v.validateTCPResponseRule != nil {
		return v.validateTCPResponseRule(m)
	}
	return nil
}

// ValidateHTTPAfterResponseRule validates an HTTPAfterResponseRule model.
func (v *ValidatorSet) ValidateHTTPAfterResponseRule(m *models.HTTPAfterResponseRule) error {
	if v.validateHTTPAfterResponse != nil {
		return v.validateHTTPAfterResponse(m)
	}
	return nil
}

// ValidateHTTPErrorRule validates an HTTPErrorRule model.
func (v *ValidatorSet) ValidateHTTPErrorRule(m *models.HTTPErrorRule) error {
	if v.validateHTTPErrorRule != nil {
		return v.validateHTTPErrorRule(m)
	}
	return nil
}

// ValidateServerSwitchingRule validates a ServerSwitchingRule model.
func (v *ValidatorSet) ValidateServerSwitchingRule(m *models.ServerSwitchingRule) error {
	if v.validateServerSwitchingRule != nil {
		return v.validateServerSwitchingRule(m)
	}
	return nil
}

// ValidateBackendSwitchingRule validates a BackendSwitchingRule model.
func (v *ValidatorSet) ValidateBackendSwitchingRule(m *models.BackendSwitchingRule) error {
	if v.validateBackendSwitching != nil {
		return v.validateBackendSwitching(m)
	}
	return nil
}

// ValidateStickRule validates a StickRule model.
func (v *ValidatorSet) ValidateStickRule(m *models.StickRule) error {
	if v.validateStickRule != nil {
		return v.validateStickRule(m)
	}
	return nil
}

// ValidateACL validates an ACL model.
func (v *ValidatorSet) ValidateACL(m *models.ACL) error {
	if v.validateACL != nil {
		return v.validateACL(m)
	}
	return nil
}

// ValidateFilter validates a Filter model.
func (v *ValidatorSet) ValidateFilter(m *models.Filter) error {
	if v.validateFilter != nil {
		return v.validateFilter(m)
	}
	return nil
}

// ValidateLogTarget validates a LogTarget model.
func (v *ValidatorSet) ValidateLogTarget(m *models.LogTarget) error {
	if v.validateLogTarget != nil {
		return v.validateLogTarget(m)
	}
	return nil
}

// ValidateHTTPCheck validates an HTTPCheck model.
func (v *ValidatorSet) ValidateHTTPCheck(m *models.HTTPCheck) error {
	if v.validateHTTPCheck != nil {
		return v.validateHTTPCheck(m)
	}
	return nil
}

// ValidateTCPCheck validates a TCPCheck model.
func (v *ValidatorSet) ValidateTCPCheck(m *models.TCPCheck) error {
	if v.validateTCPCheck != nil {
		return v.validateTCPCheck(m)
	}
	return nil
}

// ValidateCapture validates a Capture model.
func (v *ValidatorSet) ValidateCapture(m *models.Capture) error {
	if v.validateCapture != nil {
		return v.validateCapture(m)
	}
	return nil
}

// HashServer computes a content hash for a Server model.
func (v *ValidatorSet) HashServer(m *models.Server) uint64 {
	if v.hashServer != nil {
		return v.hashServer(m)
	}
	return 0
}

// HashServerTemplate computes a content hash for a ServerTemplate model.
func (v *ValidatorSet) HashServerTemplate(m *models.ServerTemplate) uint64 {
	if v.hashServerTemplate != nil {
		return v.hashServerTemplate(m)
	}
	return 0
}

// HashBind computes a content hash for a Bind model.
func (v *ValidatorSet) HashBind(m *models.Bind) uint64 {
	if v.hashBind != nil {
		return v.hashBind(m)
	}
	return 0
}

// HashHTTPRequestRule computes a content hash for an HTTPRequestRule model.
func (v *ValidatorSet) HashHTTPRequestRule(m *models.HTTPRequestRule) uint64 {
	if v.hashHTTPRequestRule != nil {
		return v.hashHTTPRequestRule(m)
	}
	return 0
}

// HashHTTPResponseRule computes a content hash for an HTTPResponseRule model.
func (v *ValidatorSet) HashHTTPResponseRule(m *models.HTTPResponseRule) uint64 {
	if v.hashHTTPResponseRule != nil {
		return v.hashHTTPResponseRule(m)
	}
	return 0
}

// HashTCPRequestRule computes a content hash for a TCPRequestRule model.
func (v *ValidatorSet) HashTCPRequestRule(m *models.TCPRequestRule) uint64 {
	if v.hashTCPRequestRule != nil {
		return v.hashTCPRequestRule(m)
	}
	return 0
}

// HashTCPResponseRule computes a content hash for a TCPResponseRule model.
func (v *ValidatorSet) HashTCPResponseRule(m *models.TCPResponseRule) uint64 {
	if v.hashTCPResponseRule != nil {
		return v.hashTCPResponseRule(m)
	}
	return 0
}

// HashHTTPAfterResponseRule computes a content hash for an HTTPAfterResponseRule model.
func (v *ValidatorSet) HashHTTPAfterResponseRule(m *models.HTTPAfterResponseRule) uint64 {
	if v.hashHTTPAfterResponse != nil {
		return v.hashHTTPAfterResponse(m)
	}
	return 0
}

// HashHTTPErrorRule computes a content hash for an HTTPErrorRule model.
func (v *ValidatorSet) HashHTTPErrorRule(m *models.HTTPErrorRule) uint64 {
	if v.hashHTTPErrorRule != nil {
		return v.hashHTTPErrorRule(m)
	}
	return 0
}

// HashServerSwitchingRule computes a content hash for a ServerSwitchingRule model.
func (v *ValidatorSet) HashServerSwitchingRule(m *models.ServerSwitchingRule) uint64 {
	if v.hashServerSwitchingRule != nil {
		return v.hashServerSwitchingRule(m)
	}
	return 0
}

// HashBackendSwitchingRule computes a content hash for a BackendSwitchingRule model.
func (v *ValidatorSet) HashBackendSwitchingRule(m *models.BackendSwitchingRule) uint64 {
	if v.hashBackendSwitching != nil {
		return v.hashBackendSwitching(m)
	}
	return 0
}

// HashStickRule computes a content hash for a StickRule model.
func (v *ValidatorSet) HashStickRule(m *models.StickRule) uint64 {
	if v.hashStickRule != nil {
		return v.hashStickRule(m)
	}
	return 0
}

// HashACL computes a content hash for an ACL model.
func (v *ValidatorSet) HashACL(m *models.ACL) uint64 {
	if v.hashACL != nil {
		return v.hashACL(m)
	}
	return 0
}

// HashFilter computes a content hash for a Filter model.
func (v *ValidatorSet) HashFilter(m *models.Filter) uint64 {
	if v.hashFilter != nil {
		return v.hashFilter(m)
	}
	return 0
}

// HashLogTarget computes a content hash for a LogTarget model.
func (v *ValidatorSet) HashLogTarget(m *models.LogTarget) uint64 {
	if v.hashLogTarget != nil {
		return v.hashLogTarget(m)
	}
	return 0
}

// HashHTTPCheck computes a content hash for an HTTPCheck model.
func (v *ValidatorSet) HashHTTPCheck(m *models.HTTPCheck) uint64 {
	if v.hashHTTPCheck != nil {
		return v.hashHTTPCheck(m)
	}
	return 0
}

// HashTCPCheck computes a content hash for a TCPCheck model.
func (v *ValidatorSet) HashTCPCheck(m *models.TCPCheck) uint64 {
	if v.hashTCPCheck != nil {
		return v.hashTCPCheck(m)
	}
	return 0
}

// HashCapture computes a content hash for a Capture model.
func (v *ValidatorSet) HashCapture(m *models.Capture) uint64 {
	if v.hashCapture != nil {
		return v.hashCapture(m)
	}
	return 0
}

// ForVersion returns the ValidatorSet for a specific HAProxy version.
// Returns v32 for 3.2+, v31 for 3.1, v30 for 3.0 and below.
func ForVersion(major, minor int) *ValidatorSet {
	if major < 3 {
		return validatorSetV30
	}
	if major > 3 {
		return validatorSetV32
	}

	switch {
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
)

func init() {
	validatorSetV30 = &ValidatorSet{
		version:                     "v30",
		validateServer:              ValidateServerV30,
		validateServerTemplate:      ValidateServerTemplateV30,
		validateBind:                ValidateBindV30,
		validateHTTPRequestRule:     ValidateHttpRequestRuleV30,
		validateHTTPResponseRule:    ValidateHttpResponseRuleV30,
		validateTCPRequestRule:      ValidateTcpRequestRuleV30,
		validateTCPResponseRule:     ValidateTcpResponseRuleV30,
		validateHTTPAfterResponse:   ValidateHttpAfterResponseRuleV30,
		validateHTTPErrorRule:       ValidateHttpErrorRuleV30,
		validateServerSwitchingRule: ValidateServerSwitchingRuleV30,
		validateBackendSwitching:    ValidateBackendSwitchingRuleV30,
		validateStickRule:           ValidateStickRuleV30,
		validateACL:                 ValidateAclV30,
		validateFilter:              ValidateFilterV30,
		validateLogTarget:           ValidateLogTargetV30,
		validateHTTPCheck:           ValidateHttpCheckV30,
		validateTCPCheck:            ValidateTcpCheckV30,
		validateCapture:             ValidateCaptureV30,
		hashServer:                  HashServerV30,
		hashServerTemplate:          HashServerTemplateV30,
		hashBind:                    HashBindV30,
		hashHTTPRequestRule:         HashHttpRequestRuleV30,
		hashHTTPResponseRule:        HashHttpResponseRuleV30,
		hashTCPRequestRule:          HashTcpRequestRuleV30,
		hashTCPResponseRule:         HashTcpResponseRuleV30,
		hashHTTPAfterResponse:       HashHttpAfterResponseRuleV30,
		hashHTTPErrorRule:           HashHttpErrorRuleV30,
		hashServerSwitchingRule:     HashServerSwitchingRuleV30,
		hashBackendSwitching:        HashBackendSwitchingRuleV30,
		hashStickRule:               HashStickRuleV30,
		hashACL:                     HashAclV30,
		hashFilter:                  HashFilterV30,
		hashLogTarget:               HashLogTargetV30,
		hashHTTPCheck:               HashHttpCheckV30,
		hashTCPCheck:                HashTcpCheckV30,
		hashCapture:                 HashCaptureV30,
	}

	validatorSetV31 = &ValidatorSet{
		version:                     "v31",
		validateServer:              ValidateServerV31,
		validateServerTemplate:      ValidateServerTemplateV31,
		validateBind:                ValidateBindV31,
		validateHTTPRequestRule:     ValidateHttpRequestRuleV31,
		validateHTTPResponseRule:    ValidateHttpResponseRuleV31,
		validateTCPRequestRule:      ValidateTcpRequestRuleV31,
		validateTCPResponseRule:     ValidateTcpResponseRuleV31,
		validateHTTPAfterResponse:   ValidateHttpAfterResponseRuleV31,
		validateHTTPErrorRule:       ValidateHttpErrorRuleV31,
		validateServerSwitchingRule: ValidateServerSwitchingRuleV31,
		validateBackendSwitching:    ValidateBackendSwitchingRuleV31,
		validateStickRule:           ValidateStickRuleV31,
		validateACL:                 ValidateAclV31,
		validateFilter:              ValidateFilterV31,
		validateLogTarget:           ValidateLogTargetV31,
		validateHTTPCheck:           ValidateHttpCheckV31,
		validateTCPCheck:            ValidateTcpCheckV31,
		validateCapture:             ValidateCaptureV31,
		hashServer:                  HashServerV31,
		hashServerTemplate:          HashServerTemplateV31,
		hashBind:                    HashBindV31,
		hashHTTPRequestRule:         HashHttpRequestRuleV31,
		hashHTTPResponseRule:        HashHttpResponseRuleV31,
		hashTCPRequestRule:          HashTcpRequestRuleV31,
		hashTCPResponseRule:         HashTcpResponseRuleV31,
		hashHTTPAfterResponse:       HashHttpAfterResponseRuleV31,
		hashHTTPErrorRule:           HashHttpErrorRuleV31,
		hashServerSwitchingRule:     HashServerSwitchingRuleV31,
		hashBackendSwitching:        HashBackendSwitchingRuleV31,
		hashStickRule:               HashStickRuleV31,
		hashACL:                     HashAclV31,
		hashFilter:                  HashFilterV31,
		hashLogTarget:               HashLogTargetV31,
		hashHTTPCheck:               HashHttpCheckV31,
		hashTCPCheck:                HashTcpCheckV31,
		hashCapture:                 HashCaptureV31,
	}

	validatorSetV32 = &ValidatorSet{
		version:                     "v32",
		validateServer:              ValidateServerV32,
		validateServerTemplate:      ValidateServerTemplateV32,
		validateBind:                ValidateBindV32,
		validateHTTPRequestRule:     ValidateHttpRequestRuleV32,
		validateHTTPResponseRule:    ValidateHttpResponseRuleV32,
		validateTCPRequestRule:      ValidateTcpRequestRuleV32,
		validateTCPResponseRule:     ValidateTcpResponseRuleV32,
		validateHTTPAfterResponse:   ValidateHttpAfterResponseRuleV32,
		validateHTTPErrorRule:       ValidateHttpErrorRuleV32,
		validateServerSwitchingRule: ValidateServerSwitchingRuleV32,
		validateBackendSwitching:    ValidateBackendSwitchingRuleV32,
		validateStickRule:           ValidateStickRuleV32,
		validateACL:                 ValidateAclV32,
		validateFilter:              ValidateFilterV32,
		validateLogTarget:           ValidateLogTargetV32,
		validateHTTPCheck:           ValidateHttpCheckV32,
		validateTCPCheck:            ValidateTcpCheckV32,
		validateCapture:             ValidateCaptureV32,
		hashServer:                  HashServerV32,
		hashServerTemplate:          HashServerTemplateV32,
		hashBind:                    HashBindV32,
		hashHTTPRequestRule:         HashHttpRequestRuleV32,
		hashHTTPResponseRule:        HashHttpResponseRuleV32,
		hashTCPRequestRule:          HashTcpRequestRuleV32,
		hashTCPResponseRule:         HashTcpResponseRuleV32,
		hashHTTPAfterResponse:       HashHttpAfterResponseRuleV32,
		hashHTTPErrorRule:           HashHttpErrorRuleV32,
		hashServerSwitchingRule:     HashServerSwitchingRuleV32,
		hashBackendSwitching:        HashBackendSwitchingRuleV32,
		hashStickRule:               HashStickRuleV32,
		hashACL:                     HashAclV32,
		hashFilter:                  HashFilterV32,
		hashLogTarget:               HashLogTargetV32,
		hashHTTPCheck:               HashHttpCheckV32,
		hashTCPCheck:                HashTcpCheckV32,
		hashCapture:                 HashCaptureV32,
	}
}
