package main

import (
	"io"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/apis/haproxytemplate/v1alpha1"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane"
	"gitlab.com/haproxy-haptic/haptic/pkg/templating"
)

func TestValidateRunsEverySelectedTest(t *testing.T) {
	previous := validateTestNames
	t.Cleanup(func() { validateTestNames = previous })
	assert.Equal(t, "stringArray", validateCmd.Flags().Lookup("test").Value.Type())
	const template = "global\n  maxconn 1000\n"
	spec := &v1alpha1.HAProxyTemplateConfigSpec{
		HAProxyConfig: v1alpha1.HAProxyConfig{Template: template},
		ValidationTests: map[string]v1alpha1.ValidationTest{
			"first":         {Assertions: []v1alpha1.ValidationAssertion{{Type: "contains", Target: "haproxy.cfg", Pattern: "maxconn 1000"}}},
			"literal,comma": {Assertions: []v1alpha1.ValidationAssertion{{Type: "contains", Target: "haproxy.cfg", Pattern: "maxconn 1000"}}},
		},
	}
	engine, err := templating.New(map[string]string{"haproxy.cfg": template}, nil)
	require.NoError(t, err)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	for _, names := range [][]string{nil, {""}, {"first", "literal,comma"}, {"first", "missing"}} {
		t.Run("selection", func(t *testing.T) {
			validateTestNames = names
			results, err := runValidationTests(t.Context(), spec, engine, &dataplane.ValidationPaths{},
				dataplane.Capabilities{}, nil, nil, logger)
			if len(names) == 2 && names[1] == "missing" {
				require.ErrorContains(t, err, `test "missing" not found`)
				assert.Nil(t, results)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, 2, results.PassedTests)
			assert.Zero(t, results.FailedTests)
		})
	}
}
