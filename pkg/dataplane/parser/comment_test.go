package parser

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// between http-request rules to understand the comment indexing bug.
func TestCommentParsing(t *testing.T) {
	// This is the SYNCED format (wrong - all comments first)
	syncedConfig := `
global
    daemon

defaults
    mode http

frontend test_frontend
  bind *:80
  # gateway/frontend-matchers-advanced-gateway-route-id-setup
  # Rule: HTTPRoute echo/echo-paths rule[0]
  # Rule: HTTPRoute echo/echo-combined rule[0] - method POST
  http-request set-var(req.gw_route_type) var(txn.path_match),field(2,:) if { var(txn.path_match),field(1,:) -m str GW_ROUTE_ID }
  http-request set-var(req.gw_rule_id) str(echo_echo-paths_0) if !{ var(req.gw_rule_id) -m found } { var(req.gw_route_id) -m str "echo_echo-paths_0" }
  http-request set-var(req.gw_rule_id) str(echo_echo-combined_0) if !{ var(req.gw_rule_id) -m found } { var(req.gw_route_id) -m str "echo_echo-combined_0" } { method POST }
`

	// This is the DESIRED format (correct - comments intermingled)
	desiredConfig := `
global
    daemon

defaults
    mode http

frontend test_frontend
    bind :80
    # gateway/frontend-matchers-advanced-gateway-route-id-setup
    http-request set-var(req.gw_route_type) var(txn.path_match),field(2,:) if { var(txn.path_match),field(1,:) -m str GW_ROUTE_ID }
    # Rule: HTTPRoute echo/echo-paths rule[0]
    http-request set-var(req.gw_rule_id) str(echo_echo-paths_0) if !{ var(req.gw_rule_id) -m found } { var(req.gw_route_id) -m str "echo_echo-paths_0" }
    # Rule: HTTPRoute echo/echo-combined rule[0] - method POST
    http-request set-var(req.gw_rule_id) str(echo_echo-combined_0) if !{ var(req.gw_rule_id) -m found } { var(req.gw_route_id) -m str "echo_echo-combined_0" } { method POST }
`

	t.Run("synced_config", func(t *testing.T) {
		testCommentParsing(t, syncedConfig, "synced")
	})

	t.Run("desired_config", func(t *testing.T) {
		testCommentParsing(t, desiredConfig, "desired")
	})
}

func testCommentParsing(t *testing.T, config, label string) {
	t.Helper()

	parser, err := New()
	require.NoError(t, err)

	parsed, err := parser.ParseFromString(config)
	require.NoError(t, err)

	frontends := parsed.Frontends
	require.Len(t, frontends, 1, "Should have one frontend")

	frontend := frontends[0]
	t.Logf("\n[%s] Frontend %s has %d http-request rules", label, frontend.Name, len(frontend.HTTPRequestRuleList))

	commentsFound := 0
	for i, rule := range frontend.HTTPRequestRuleList {
		t.Logf("\n[%s] Rule %d:", label, i)
		t.Logf("  Type: %s", rule.Type)
		t.Logf("  VarExpr: %s", rule.VarExpr)
		if len(rule.Metadata) > 0 {
			t.Logf("  Metadata keys: %v", getKeys(rule.Metadata))
			if comment, ok := rule.Metadata["comment"]; ok {
				t.Logf("  >>> Comment: %v", comment)
				commentsFound++
			}
		} else {
			t.Logf("  Metadata: (empty)")
		}
	}

	t.Logf("\n[%s] === Summary: Found %d rules with %d comments in metadata ===", label, len(frontend.HTTPRequestRuleList), commentsFound)
}

func getKeys(m map[string]interface{}) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}
