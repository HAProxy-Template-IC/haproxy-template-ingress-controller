package validator

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/configtest"
)

func TestValidationTestsVerdictRejectsIncompleteAndUnexplainedFailures(t *testing.T) {
	for _, tt := range []struct {
		name     string
		result   configtest.Result
		err      error
		valid    bool
		failures []string
	}{
		{name: "passed", result: configtest.Result{Passed: true}, valid: true},
		{name: "zero result"},
		{name: "failure", result: configtest.Result{Failures: []string{"assertion failed"}}, failures: []string{"assertion failed"}},
		{name: "incomplete pass", result: configtest.Result{Passed: true, Incomplete: true}, failures: []string{"suite deadline"}},
		{name: "incomplete failure", result: configtest.Result{Incomplete: true, Failures: []string{"partial failure"}}, failures: []string{"suite deadline"}},
		{name: "error overrides pass", result: configtest.Result{Passed: true}, err: errors.New("schema unavailable"), failures: []string{"schema unavailable"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			valid, failures := validationTestsVerdict(tt.result, tt.err, "suite deadline")
			assert.Equal(t, tt.valid, valid)
			assert.Equal(t, tt.failures, failures)
		})
	}
}
