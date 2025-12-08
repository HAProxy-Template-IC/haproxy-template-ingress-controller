// Package sections provides factory functions for creating HAProxy configuration operations.
//
// This file contains helper functions to reduce repetition in factory functions.
package sections

import (
	"fmt"

	"github.com/haproxytech/client-native/v6/models"
)

// =============================================================================
// Description Helpers
// =============================================================================

// DescribeTopLevel returns a description function for top-level operations.
func DescribeTopLevel(op OperationType, section, name string) func() string {
	verb := opVerb(op)
	return func() string {
		return fmt.Sprintf("%s %s '%s'", verb, section, name)
	}
}

// DescribeIndexChild returns a description function for indexed child operations.
func DescribeIndexChild(op OperationType, childType string, index int, parentType, parentName string) func() string {
	verb := opVerb(op)
	preposition := opPreposition(op)
	return func() string {
		return fmt.Sprintf("%s %s at index %d %s %s '%s'", verb, childType, index, preposition, parentType, parentName)
	}
}

// DescribeNamedChild returns a description function for named child operations.
func DescribeNamedChild(op OperationType, childType, childName, parentType, parentName string) func() string {
	verb := opVerb(op)
	preposition := opPreposition(op)
	return func() string {
		return fmt.Sprintf("%s %s '%s' %s %s '%s'", verb, childType, childName, preposition, parentType, parentName)
	}
}

// DescribeContainerChild returns a description function for container child operations.
func DescribeContainerChild(op OperationType, childType, childName, containerType, containerName string) func() string {
	verb := opVerb(op)
	preposition := opPreposition(op)
	return func() string {
		return fmt.Sprintf("%s %s '%s' %s %s '%s'", verb, childType, childName, preposition, containerType, containerName)
	}
}

// DescribeACL returns a description function for ACL operations with ACL name.
func DescribeACL(op OperationType, aclName, parentType, parentName string) func() string {
	verb := opVerb(op)
	preposition := opPreposition(op)
	return func() string {
		return fmt.Sprintf("%s ACL '%s' %s %s '%s'", verb, aclName, preposition, parentType, parentName)
	}
}

// Prepositions for description text.
const (
	prepositionIn   = "in"
	prepositionFrom = "from"
)

// opVerb returns the verb for an operation type.
func opVerb(op OperationType) string {
	switch op {
	case OperationCreate:
		return "Create"
	case OperationUpdate:
		return "Update"
	case OperationDelete:
		return "Delete"
	default:
		return "Process"
	}
}

// opPreposition returns the appropriate preposition for the operation type.
func opPreposition(op OperationType) string {
	if op == OperationDelete {
		return prepositionFrom
	}
	return prepositionIn
}

// =============================================================================
// Name Extractors
// =============================================================================

// BackendName extracts the name from a Backend model.
func BackendName(b *models.Backend) string { return b.Name }

// FrontendName extracts the name from a Frontend model.
func FrontendName(f *models.Frontend) string { return f.Name }

// DefaultsName extracts the name from a Defaults model.
func DefaultsName(d *models.Defaults) string { return d.Name }

// CacheName extracts the name from a Cache model.
func CacheName(c *models.Cache) string { return ptrStr(c.Name) }

// HTTPErrorsSectionName extracts the name from an HTTPErrorsSection model.
func HTTPErrorsSectionName(h *models.HTTPErrorsSection) string { return h.Name }

// LogForwardName extracts the name from a LogForward model.
func LogForwardName(l *models.LogForward) string { return l.Name }

// MailersSectionName extracts the name from a MailersSection model.
func MailersSectionName(m *models.MailersSection) string { return m.Name }

// PeerSectionName extracts the name from a PeerSection model.
func PeerSectionName(p *models.PeerSection) string { return p.Name }

// ProgramName extracts the name from a Program model.
func ProgramName(p *models.Program) string { return p.Name }

// ResolverName extracts the name from a Resolver model.
func ResolverName(r *models.Resolver) string { return r.Name }

// RingName extracts the name from a Ring model.
func RingName(r *models.Ring) string { return r.Name }

// CrtStoreName extracts the name from a CrtStore model.
func CrtStoreName(c *models.CrtStore) string { return c.Name }

// UserlistName extracts the name from a Userlist model.
func UserlistName(u *models.Userlist) string { return u.Name }

// FCGIAppName extracts the name from an FCGIApp model.
func FCGIAppName(f *models.FCGIApp) string { return f.Name }

// ACLName extracts the name from an ACL model.
func ACLName(a *models.ACL) string { return a.ACLName }

// BindName extracts the name from a Bind model.
func BindName(b *models.Bind) string { return b.Name }

// ServerName extracts the name from a Server model.
func ServerName(s *models.Server) string { return s.Name }

// ServerTemplateName extracts the prefix from a ServerTemplate model.
func ServerTemplateName(s *models.ServerTemplate) string { return s.Prefix }

// FilterType extracts the type from a Filter model (for description purposes).
func FilterType(f *models.Filter) string { return f.Type }

// UserName extracts the name from a User model.
func UserName(u *models.User) string { return u.Username }

// MailerEntryName extracts the name from a MailerEntry model.
func MailerEntryName(m *models.MailerEntry) string { return m.Name }

// PeerEntryName extracts the name from a PeerEntry model.
func PeerEntryName(p *models.PeerEntry) string { return p.Name }

// NameserverName extracts the name from a Nameserver model.
func NameserverName(n *models.Nameserver) string { return n.Name }

// LogProfileName extracts the name from a LogProfile model.
func LogProfileName(l *models.LogProfile) string { return l.Name }

// AcmeProviderName extracts the name from an AcmeProvider model.
func AcmeProviderName(a *models.AcmeProvider) string { return a.Name }

// =============================================================================
// Nil Transform Functions (for delete operations)
// =============================================================================

// NilBackend returns nil, used for delete operations where model isn't needed.
func NilBackend(_ *models.Backend) *models.Backend { return nil }

// NilFrontend returns nil, used for delete operations where model isn't needed.
func NilFrontend(_ *models.Frontend) *models.Frontend { return nil }

// NilDefaults returns nil, used for delete operations where model isn't needed.
func NilDefaults(_ *models.Defaults) *models.Defaults { return nil }

// NilCache returns nil, used for delete operations where model isn't needed.
func NilCache(_ *models.Cache) *models.Cache { return nil }

// NilHTTPErrorsSection returns nil, used for delete operations where model isn't needed.
func NilHTTPErrorsSection(_ *models.HTTPErrorsSection) *models.HTTPErrorsSection { return nil }

// NilLogForward returns nil, used for delete operations where model isn't needed.
func NilLogForward(_ *models.LogForward) *models.LogForward { return nil }

// NilMailersSection returns nil, used for delete operations where model isn't needed.
func NilMailersSection(_ *models.MailersSection) *models.MailersSection { return nil }

// NilPeerSection returns nil, used for delete operations where model isn't needed.
func NilPeerSection(_ *models.PeerSection) *models.PeerSection { return nil }

// NilProgram returns nil, used for delete operations where model isn't needed.
func NilProgram(_ *models.Program) *models.Program { return nil }

// NilResolver returns nil, used for delete operations where model isn't needed.
func NilResolver(_ *models.Resolver) *models.Resolver { return nil }

// NilRing returns nil, used for delete operations where model isn't needed.
func NilRing(_ *models.Ring) *models.Ring { return nil }

// NilCrtStore returns nil, used for delete operations where model isn't needed.
func NilCrtStore(_ *models.CrtStore) *models.CrtStore { return nil }

// NilUserlist returns nil, used for delete operations where model isn't needed.
func NilUserlist(_ *models.Userlist) *models.Userlist { return nil }

// NilFCGIApp returns nil, used for delete operations where model isn't needed.
func NilFCGIApp(_ *models.FCGIApp) *models.FCGIApp { return nil }

// NilACL returns nil, used for delete operations where model isn't needed.
func NilACL(_ *models.ACL) *models.ACL { return nil }

// NilBind returns nil, used for delete operations where model isn't needed.
func NilBind(_ *models.Bind) *models.Bind { return nil }

// NilServer returns nil, used for delete operations where model isn't needed.
func NilServer(_ *models.Server) *models.Server { return nil }

// NilServerTemplate returns nil, used for delete operations where model isn't needed.
func NilServerTemplate(_ *models.ServerTemplate) *models.ServerTemplate { return nil }

// NilFilter returns nil, used for delete operations where model isn't needed.
func NilFilter(_ *models.Filter) *models.Filter { return nil }

// NilUser returns nil, used for delete operations where model isn't needed.
func NilUser(_ *models.User) *models.User { return nil }

// NilMailerEntry returns nil, used for delete operations where model isn't needed.
func NilMailerEntry(_ *models.MailerEntry) *models.MailerEntry { return nil }

// NilPeerEntry returns nil, used for delete operations where model isn't needed.
func NilPeerEntry(_ *models.PeerEntry) *models.PeerEntry { return nil }

// NilNameserver returns nil, used for delete operations where model isn't needed.
func NilNameserver(_ *models.Nameserver) *models.Nameserver { return nil }

// NilHTTPRequestRule returns nil, used for delete operations where model isn't needed.
func NilHTTPRequestRule(_ *models.HTTPRequestRule) *models.HTTPRequestRule { return nil }

// NilHTTPResponseRule returns nil, used for delete operations where model isn't needed.
func NilHTTPResponseRule(_ *models.HTTPResponseRule) *models.HTTPResponseRule { return nil }

// NilHTTPAfterResponseRule returns nil, used for delete operations where model isn't needed.
func NilHTTPAfterResponseRule(_ *models.HTTPAfterResponseRule) *models.HTTPAfterResponseRule {
	return nil
}

// NilHTTPCheck returns nil, used for delete operations where model isn't needed.
func NilHTTPCheck(_ *models.HTTPCheck) *models.HTTPCheck { return nil }

// NilTCPRequestRule returns nil, used for delete operations where model isn't needed.
func NilTCPRequestRule(_ *models.TCPRequestRule) *models.TCPRequestRule { return nil }

// NilTCPResponseRule returns nil, used for delete operations where model isn't needed.
func NilTCPResponseRule(_ *models.TCPResponseRule) *models.TCPResponseRule { return nil }

// NilTCPCheck returns nil, used for delete operations where model isn't needed.
func NilTCPCheck(_ *models.TCPCheck) *models.TCPCheck { return nil }

// NilBackendSwitchingRule returns nil, used for delete operations where model isn't needed.
func NilBackendSwitchingRule(_ *models.BackendSwitchingRule) *models.BackendSwitchingRule { return nil }

// NilServerSwitchingRule returns nil, used for delete operations where model isn't needed.
func NilServerSwitchingRule(_ *models.ServerSwitchingRule) *models.ServerSwitchingRule { return nil }

// NilStickRule returns nil, used for delete operations where model isn't needed.
func NilStickRule(_ *models.StickRule) *models.StickRule { return nil }

// NilLogTarget returns nil, used for delete operations where model isn't needed.
func NilLogTarget(_ *models.LogTarget) *models.LogTarget { return nil }

// NilCapture returns nil, used for delete operations where model isn't needed.
func NilCapture(_ *models.Capture) *models.Capture { return nil }

// NilLogProfile returns nil, used for delete operations where model isn't needed.
func NilLogProfile(_ *models.LogProfile) *models.LogProfile { return nil }

// NilAcmeProvider returns nil, used for delete operations where model isn't needed.
func NilAcmeProvider(_ *models.AcmeProvider) *models.AcmeProvider { return nil }

// =============================================================================
// Identity Transform Functions (for direct model passthrough)
// These replace the old transform.ToAPI* functions since executors now accept
// client-native models directly.
// =============================================================================

// IdentityBackend returns the model as-is.
func IdentityBackend(b *models.Backend) *models.Backend { return b }

// IdentityFrontend returns the model as-is.
func IdentityFrontend(f *models.Frontend) *models.Frontend { return f }

// IdentityDefaults returns the model as-is.
func IdentityDefaults(d *models.Defaults) *models.Defaults { return d }

// IdentityGlobal returns the model as-is.
func IdentityGlobal(g *models.Global) *models.Global { return g }

// IdentityCache returns the model as-is.
func IdentityCache(c *models.Cache) *models.Cache { return c }

// IdentityHTTPErrorsSection returns the model as-is.
func IdentityHTTPErrorsSection(h *models.HTTPErrorsSection) *models.HTTPErrorsSection { return h }

// IdentityLogForward returns the model as-is.
func IdentityLogForward(l *models.LogForward) *models.LogForward { return l }

// IdentityMailersSection returns the model as-is.
func IdentityMailersSection(m *models.MailersSection) *models.MailersSection { return m }

// IdentityPeerSection returns the model as-is.
func IdentityPeerSection(p *models.PeerSection) *models.PeerSection { return p }

// IdentityProgram returns the model as-is.
func IdentityProgram(p *models.Program) *models.Program { return p }

// IdentityResolver returns the model as-is.
func IdentityResolver(r *models.Resolver) *models.Resolver { return r }

// IdentityRing returns the model as-is.
func IdentityRing(r *models.Ring) *models.Ring { return r }

// IdentityCrtStore returns the model as-is.
func IdentityCrtStore(c *models.CrtStore) *models.CrtStore { return c }

// IdentityUserlist returns the model as-is.
func IdentityUserlist(u *models.Userlist) *models.Userlist { return u }

// IdentityFCGIApp returns the model as-is.
func IdentityFCGIApp(f *models.FCGIApp) *models.FCGIApp { return f }

// IdentityACL returns the model as-is.
func IdentityACL(a *models.ACL) *models.ACL { return a }

// IdentityBind returns the model as-is.
func IdentityBind(b *models.Bind) *models.Bind { return b }

// IdentityServer returns the model as-is.
func IdentityServer(s *models.Server) *models.Server { return s }

// IdentityServerTemplate returns the model as-is.
func IdentityServerTemplate(s *models.ServerTemplate) *models.ServerTemplate { return s }

// IdentityFilter returns the model as-is.
func IdentityFilter(f *models.Filter) *models.Filter { return f }

// IdentityUser returns the model as-is.
func IdentityUser(u *models.User) *models.User { return u }

// IdentityMailerEntry returns the model as-is.
func IdentityMailerEntry(m *models.MailerEntry) *models.MailerEntry { return m }

// IdentityPeerEntry returns the model as-is.
func IdentityPeerEntry(p *models.PeerEntry) *models.PeerEntry { return p }

// IdentityNameserver returns the model as-is.
func IdentityNameserver(n *models.Nameserver) *models.Nameserver { return n }

// IdentityHTTPRequestRule returns the model as-is.
func IdentityHTTPRequestRule(r *models.HTTPRequestRule) *models.HTTPRequestRule { return r }

// IdentityHTTPResponseRule returns the model as-is.
func IdentityHTTPResponseRule(r *models.HTTPResponseRule) *models.HTTPResponseRule { return r }

// IdentityHTTPAfterResponseRule returns the model as-is.
func IdentityHTTPAfterResponseRule(r *models.HTTPAfterResponseRule) *models.HTTPAfterResponseRule {
	return r
}

// IdentityHTTPCheck returns the model as-is.
func IdentityHTTPCheck(c *models.HTTPCheck) *models.HTTPCheck { return c }

// IdentityTCPRequestRule returns the model as-is.
func IdentityTCPRequestRule(r *models.TCPRequestRule) *models.TCPRequestRule { return r }

// IdentityTCPResponseRule returns the model as-is.
func IdentityTCPResponseRule(r *models.TCPResponseRule) *models.TCPResponseRule { return r }

// IdentityTCPCheck returns the model as-is.
func IdentityTCPCheck(c *models.TCPCheck) *models.TCPCheck { return c }

// IdentityBackendSwitchingRule returns the model as-is.
func IdentityBackendSwitchingRule(r *models.BackendSwitchingRule) *models.BackendSwitchingRule {
	return r
}

// IdentityServerSwitchingRule returns the model as-is.
func IdentityServerSwitchingRule(r *models.ServerSwitchingRule) *models.ServerSwitchingRule { return r }

// IdentityStickRule returns the model as-is.
func IdentityStickRule(r *models.StickRule) *models.StickRule { return r }

// IdentityLogTarget returns the model as-is.
func IdentityLogTarget(l *models.LogTarget) *models.LogTarget { return l }

// IdentityCapture returns the model as-is.
func IdentityCapture(c *models.Capture) *models.Capture { return c }

// IdentityLogProfile returns the model as-is.
func IdentityLogProfile(l *models.LogProfile) *models.LogProfile { return l }

// IdentityTraces returns the model as-is.
func IdentityTraces(t *models.Traces) *models.Traces { return t }

// IdentityAcmeProvider returns the model as-is.
func IdentityAcmeProvider(a *models.AcmeProvider) *models.AcmeProvider { return a }

// NilQUICInitialRule returns nil, used for delete operations where model isn't needed.
func NilQUICInitialRule(_ *models.QUICInitialRule) *models.QUICInitialRule { return nil }

// IdentityQUICInitialRule returns the model as-is.
func IdentityQUICInitialRule(r *models.QUICInitialRule) *models.QUICInitialRule { return r }
