// Package sections provides factory functions for creating HAProxy configuration operations.
//
// This file contains helper functions to reduce repetition in factory functions.
package sections

import (
	"fmt"

	"github.com/haproxytech/client-native/v6/models"
)

// Identity returns the model as-is. Used as transform function for create/update operations.
func Identity[T any](model T) T { return model }

// Nil returns the zero value of T. Used as transform function for delete operations
// where the model isn't needed.
func Nil[T any](_ T) T { var zero T; return zero }

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

// Name extraction functions - each accesses a different struct field.

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
