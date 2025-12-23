// Package enterprise provides client operations for HAProxy Enterprise-only DataPlane API endpoints.
//
// This package is organized by feature domain:
//   - waf.go: Web Application Firewall operations (waf_profiles, waf_body_rules, waf/rulesets)
//   - botmgmt.go: Bot management (botmgmt_profiles, captchas)
//   - udp.go: UDP load balancing (udp_lbs with child resources)
//   - keepalived.go: Keepalived/VRRP operations (vrrp_instances, vrrp_sync_groups)
//   - logging.go: Advanced logging (logs/config, logs/inputs, logs/outputs)
//   - git.go: Git integration (git/settings, git/actions)
//   - dynamic_update.go: Dynamic update rules
//   - aloha.go: ALOHA features
//   - misc.go: Other endpoints (facts, ping, summary, structured config)
//
// All operations in this package require HAProxy Enterprise edition. When called
// against a Community edition instance, operations will return ErrEnterpriseRequired.
//
// Usage:
//
//	wafOps := enterprise.NewWAFOperations(dataplaneClient)
//	profiles, err := wafOps.GetAllProfiles(ctx, txID)
//	if errors.Is(err, client.ErrEnterpriseRequired) {
//	    log.Warn("WAF features require HAProxy Enterprise")
//	}
package enterprise

import (
	"errors"

	"haptic/pkg/dataplane/client"
)

// ErrNotFound is returned when a requested resource does not exist.
var ErrNotFound = errors.New("resource not found")

// Operations provides access to all enterprise-only operations.
// This is the main entry point for enterprise features.
type Operations struct {
	client *client.DataplaneClient
}

// NewOperations creates a new enterprise operations client.
// All operations will check for enterprise edition before executing.
func NewOperations(c *client.DataplaneClient) *Operations {
	return &Operations{client: c}
}

// Client returns the underlying DataplaneClient for direct access.
func (o *Operations) Client() *client.DataplaneClient {
	return o.client
}

// IsAvailable returns true if enterprise features are available.
// This checks whether the connected HAProxy instance is Enterprise edition.
func (o *Operations) IsAvailable() bool {
	return o.client.Clientset().IsEnterprise()
}

// Capabilities returns the enterprise capability flags.
// Use this to check which enterprise features are available.
func (o *Operations) Capabilities() client.Capabilities {
	return o.client.Clientset().Capabilities()
}
