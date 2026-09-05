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

//go:build playground

package validators

import (
	"errors"

	"github.com/haproxytech/client-native/v6/models"

	genvalidators "gitlab.com/haproxy-haptic/haptic/pkg/generated/validators"
)

var errValidatorUnavailable = errors.New("validator unavailable")

// ValidatorSet provides type-specific validation functions for a HAProxy version.
// Each method validates a model directly without JSON conversion.
type ValidatorSet struct {
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
}

func validate[T any](model T, validator func(T) error) error {
	if validator == nil {
		return errValidatorUnavailable
	}
	return validator(model)
}

// ValidateServer validates a Server model.
func (v *ValidatorSet) ValidateServer(model *models.Server) error {
	return validate(model, v.validateServer)
}

// ValidateServerTemplate validates a ServerTemplate model.
func (v *ValidatorSet) ValidateServerTemplate(model *models.ServerTemplate) error {
	return validate(model, v.validateServerTemplate)
}

// ValidateBind validates a Bind model.
func (v *ValidatorSet) ValidateBind(model *models.Bind) error {
	return validate(model, v.validateBind)
}

// ValidateHTTPRequestRule validates an HTTPRequestRule model.
func (v *ValidatorSet) ValidateHTTPRequestRule(model *models.HTTPRequestRule) error {
	return validate(model, v.validateHTTPRequestRule)
}

// ValidateHTTPResponseRule validates an HTTPResponseRule model.
func (v *ValidatorSet) ValidateHTTPResponseRule(model *models.HTTPResponseRule) error {
	return validate(model, v.validateHTTPResponseRule)
}

// ValidateTCPRequestRule validates a TCPRequestRule model.
func (v *ValidatorSet) ValidateTCPRequestRule(model *models.TCPRequestRule) error {
	return validate(model, v.validateTCPRequestRule)
}

// ValidateTCPResponseRule validates a TCPResponseRule model.
func (v *ValidatorSet) ValidateTCPResponseRule(model *models.TCPResponseRule) error {
	return validate(model, v.validateTCPResponseRule)
}

// ValidateHTTPAfterResponseRule validates an HTTPAfterResponseRule model.
func (v *ValidatorSet) ValidateHTTPAfterResponseRule(model *models.HTTPAfterResponseRule) error {
	return validate(model, v.validateHTTPAfterResponse)
}

// ValidateHTTPErrorRule validates an HTTPErrorRule model.
func (v *ValidatorSet) ValidateHTTPErrorRule(model *models.HTTPErrorRule) error {
	return validate(model, v.validateHTTPErrorRule)
}

// ValidateServerSwitchingRule validates a ServerSwitchingRule model.
func (v *ValidatorSet) ValidateServerSwitchingRule(model *models.ServerSwitchingRule) error {
	return validate(model, v.validateServerSwitchingRule)
}

// ValidateBackendSwitchingRule validates a BackendSwitchingRule model.
func (v *ValidatorSet) ValidateBackendSwitchingRule(model *models.BackendSwitchingRule) error {
	return validate(model, v.validateBackendSwitching)
}

// ValidateStickRule validates a StickRule model.
func (v *ValidatorSet) ValidateStickRule(model *models.StickRule) error {
	return validate(model, v.validateStickRule)
}

// ValidateACL validates an ACL model.
func (v *ValidatorSet) ValidateACL(model *models.ACL) error {
	return validate(model, v.validateACL)
}

// ValidateFilter validates a Filter model.
func (v *ValidatorSet) ValidateFilter(model *models.Filter) error {
	return validate(model, v.validateFilter)
}

// ValidateLogTarget validates a LogTarget model.
func (v *ValidatorSet) ValidateLogTarget(model *models.LogTarget) error {
	return validate(model, v.validateLogTarget)
}

// ValidateHTTPCheck validates an HTTPCheck model.
func (v *ValidatorSet) ValidateHTTPCheck(model *models.HTTPCheck) error {
	return validate(model, v.validateHTTPCheck)
}

// ValidateTCPCheck validates a TCPCheck model.
func (v *ValidatorSet) ValidateTCPCheck(model *models.TCPCheck) error {
	return validate(model, v.validateTCPCheck)
}

// ValidateCapture validates a Capture model.
func (v *ValidatorSet) ValidateCapture(model *models.Capture) error {
	return validate(model, v.validateCapture)
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
	}

	validatorSetV31 = &ValidatorSet{
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
	}

	validatorSetV32 = &ValidatorSet{
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
	}

	validatorSetV33 = &ValidatorSet{
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
	}
}
