// Package sections provides factory functions for creating HAProxy configuration operations.
//
// This file contains helper functions to reduce repetition in factory functions.
package sections

import (
	"fmt"

	"github.com/haproxytech/client-native/v6/models"
)

// describeTopLevel returns a description function for top-level operations.
func describeTopLevel(op OperationType, section, name string) func() string {
	verb := opVerb(op)
	return func() string {
		return fmt.Sprintf("%s %s '%s'", verb, section, name)
	}
}

// describeNamedChild returns a description function for named child operations.
// Also used for container children (user, mailer entry, etc.) where the parent
// is a container section like userlist or mailers — the formatted output is
// identical, only the documented intent differs.
func describeNamedChild(op OperationType, childType, childName, parentType, parentName string) func() string {
	verb := opVerb(op)
	preposition := opPreposition(op)
	return func() string {
		return fmt.Sprintf("%s %s '%s' %s %s '%s'", verb, childType, childName, preposition, parentType, parentName)
	}
}

// describeACL returns a description function for ACL operations with ACL name.
func describeACL(op OperationType, aclName, parentType, parentName string) func() string {
	verb := opVerb(op)
	preposition := opPreposition(op)
	return func() string {
		return fmt.Sprintf("%s ACL '%s' %s %s '%s'", verb, aclName, preposition, parentType, parentName)
	}
}

// describeTypedChild returns a description function for typed child operations
// where the identifier is extracted from a model field.
// If identifier is empty, the fallback is used (e.g. "at index 3").
// Non-empty identifiers are wrapped in parentheses (e.g. "(request)").
func describeTypedChild(op OperationType, childType, identifier, fallback, parentType, parentName string) func() string {
	verb := opVerb(op)
	preposition := opPreposition(op)

	display := fallback
	if identifier != "" {
		display = fmt.Sprintf("(%s)", identifier)
	}

	return func() string {
		return fmt.Sprintf("%s %s %s %s %s '%s'", verb, childType, display, preposition, parentType, parentName)
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

// Name extraction functions - each accesses a different struct field.

// backendNameFn extracts the name from a Backend model.
func backendNameFn(b *models.Backend) string { return b.Name }

// frontendNameFn extracts the name from a Frontend model.
func frontendNameFn(f *models.Frontend) string { return f.Name }

// defaultsNameFn extracts the name from a Defaults model.
func defaultsNameFn(d *models.Defaults) string { return d.Name }

// cacheNameFn extracts the name from a Cache model.
func cacheNameFn(c *models.Cache) string { return ptrStr(c.Name) }

// httpErrorsSectionName extracts the name from an HTTPErrorsSection model.
func httpErrorsSectionName(h *models.HTTPErrorsSection) string { return h.Name }

// logForwardName extracts the name from a LogForward model.
func logForwardName(l *models.LogForward) string { return l.Name }

// mailersSectionName extracts the name from a MailersSection model.
func mailersSectionName(m *models.MailersSection) string { return m.Name }

// peerSectionName extracts the name from a PeerSection model.
func peerSectionName(p *models.PeerSection) string { return p.Name }

// resolverNameFn extracts the name from a Resolver model.
func resolverNameFn(r *models.Resolver) string { return r.Name }

// ringNameFn extracts the name from a Ring model.
func ringNameFn(r *models.Ring) string { return r.Name }

// crtStoreName extracts the name from a CrtStore model.
func crtStoreName(c *models.CrtStore) string { return c.Name }

// userlistName extracts the name from a Userlist model.
func userlistName(u *models.Userlist) string { return u.Name }

// fcgiAppName extracts the name from an FCGIApp model.
func fcgiAppName(f *models.FCGIApp) string { return f.Name }

// userNameFn extracts the name from a User model.
func userNameFn(u *models.User) string { return u.Username }

// mailerEntryName extracts the name from a MailerEntry model.
func mailerEntryName(m *models.MailerEntry) string { return m.Name }

// peerEntryName extracts the name from a PeerEntry model.
func peerEntryName(p *models.PeerEntry) string { return p.Name }

// nameserverNameFn extracts the name from a Nameserver model.
func nameserverNameFn(n *models.Nameserver) string { return n.Name }

// logProfileName extracts the name from a LogProfile model.
func logProfileName(l *models.LogProfile) string { return l.Name }

// acmeProviderName extracts the name from an AcmeProvider model.
func acmeProviderName(a *models.AcmeProvider) string { return a.Name }
