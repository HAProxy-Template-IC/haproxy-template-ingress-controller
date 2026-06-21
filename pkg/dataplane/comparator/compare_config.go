package comparator

import (
	"github.com/haproxytech/client-native/v6/models"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/comparator/sections"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/parser"
)

// compareHTTPErrors compares http-errors sections between current and desired configurations.
func (c *Comparator) compareHTTPErrors(current, desired *parser.StructuredConfig) []Operation {
	return compareNamedSections(
		current.HTTPErrors,
		desired.HTTPErrors,
		func(s *models.HTTPErrorsSection) string { return s.Name },
		func(a, b *models.HTTPErrorsSection) bool { return a.Equal(*b) },
		sections.NewHTTPErrorsSectionCreate,
		sections.NewHTTPErrorsSectionDelete,
		sections.NewHTTPErrorsSectionUpdate,
	)
}

// compareMailers compares mailers sections between current and desired configurations.
// Uses pointer indexes for zero-copy iteration over mailer entries.
func (c *Comparator) compareMailers(current, desired *parser.StructuredConfig) []Operation {
	return compareContainerSection(
		current.Mailers, desired.Mailers,
		current.MailerEntryIndex, desired.MailerEntryIndex,
		func(m *models.MailersSection) string { return m.Name },
		mailersEqualWithoutMailerEntries,
		sections.NewMailersSectionCreate,
		sections.NewMailersSectionDelete,
		sections.NewMailersSectionUpdate,
		c.compareMailerEntriesWithIndex,
	)
}

// mailersEqualWithoutMailerEntries checks if two mailers sections are equal, excluding mailer entries.
// Uses the HAProxy models' built-in Equal() method to compare mailers section attributes
// (name, timeout, etc.) automatically, excluding mailer entries we compare separately.
func mailersEqualWithoutMailerEntries(m1, m2 *models.MailersSection) bool {
	// Create copies to avoid modifying originals
	m1Copy := *m1
	m2Copy := *m2

	// Clear mailer entries so they don't affect comparison
	m1Copy.MailerEntries = nil
	m2Copy.MailerEntries = nil

	return m1Copy.Equal(m2Copy)
}

// compareMailerEntriesWithIndex compares mailer entry configurations using pointer indexes.
func (c *Comparator) compareMailerEntriesWithIndex(mailersSection string, currentEntries, desiredEntries map[string]*models.MailerEntry) []Operation {
	return compareNamedMaps(
		currentEntries, desiredEntries,
		func(a, b *models.MailerEntry) bool { return a.Equal(*b) },
		func(e *models.MailerEntry) Operation { return sections.NewMailerEntryCreate(mailersSection, e) },
		func(e *models.MailerEntry) Operation { return sections.NewMailerEntryDelete(mailersSection, e) },
		func(e *models.MailerEntry) Operation { return sections.NewMailerEntryUpdate(mailersSection, e) },
	)
}

// comparePeers compares peer sections between current and desired configurations.
// Uses pointer indexes for zero-copy iteration over peer entries.
func (c *Comparator) comparePeers(current, desired *parser.StructuredConfig) []Operation {
	return compareContainerSection(
		current.Peers, desired.Peers,
		current.PeerEntryIndex, desired.PeerEntryIndex,
		func(p *models.PeerSection) string { return p.Name },
		peersEqualWithoutPeerEntries,
		sections.NewPeerSectionCreate,
		sections.NewPeerSectionDelete,
		sections.NewPeerSectionUpdate,
		c.comparePeerEntriesWithIndex,
	)
}

// peersEqualWithoutPeerEntries checks if two peer sections are equal, excluding peer entries.
// Uses the HAProxy models' built-in Equal() method to compare peer section attributes
// automatically, excluding peer entries we compare separately.
func peersEqualWithoutPeerEntries(p1, p2 *models.PeerSection) bool {
	// Create copies to avoid modifying originals
	p1Copy := *p1
	p2Copy := *p2

	// Clear peer entries so they don't affect comparison
	p1Copy.PeerEntries = nil
	p2Copy.PeerEntries = nil

	return p1Copy.Equal(p2Copy)
}

// comparePeerEntriesWithIndex compares peer entry configurations using pointer indexes.
func (c *Comparator) comparePeerEntriesWithIndex(peersSection string, currentEntries, desiredEntries map[string]*models.PeerEntry) []Operation {
	return compareNamedMaps(
		currentEntries, desiredEntries,
		func(a, b *models.PeerEntry) bool { return a.Equal(*b) },
		func(e *models.PeerEntry) Operation { return sections.NewPeerEntryCreate(peersSection, e) },
		func(e *models.PeerEntry) Operation { return sections.NewPeerEntryDelete(peersSection, e) },
		func(e *models.PeerEntry) Operation { return sections.NewPeerEntryUpdate(peersSection, e) },
	)
}

// compareCaches compares cache sections between current and desired configurations.
func (c *Comparator) compareCaches(current, desired *parser.StructuredConfig) []Operation {
	return compareNamedSections(
		current.Caches,
		desired.Caches,
		func(s *models.Cache) string {
			if s.Name == nil {
				return ""
			}
			return *s.Name
		},
		func(a, b *models.Cache) bool { return a.Equal(*b) },
		sections.NewCacheCreate,
		sections.NewCacheDelete,
		sections.NewCacheUpdate,
	)
}

// compareRings compares ring sections between current and desired configurations.
func (c *Comparator) compareRings(current, desired *parser.StructuredConfig) []Operation {
	return compareNamedSections(
		current.Rings,
		desired.Rings,
		func(r *models.Ring) string { return r.Name },
		func(a, b *models.Ring) bool { return a.Equal(*b) },
		sections.NewRingCreate,
		sections.NewRingDelete,
		sections.NewRingUpdate,
	)
}

// comparePrograms compares program sections between current and desired configurations.
func (c *Comparator) comparePrograms(current, desired *parser.StructuredConfig) []Operation {
	return compareNamedSections(
		current.Programs,
		desired.Programs,
		func(p *models.Program) string { return p.Name },
		func(a, b *models.Program) bool { return a.Equal(*b) },
		sections.NewProgramCreate,
		sections.NewProgramDelete,
		sections.NewProgramUpdate,
	)
}

// compareFCGIApps compares fcgi-app sections between current and desired configurations.
func (c *Comparator) compareFCGIApps(current, desired *parser.StructuredConfig) []Operation {
	return compareNamedSections(
		current.FCGIApps,
		desired.FCGIApps,
		func(a *models.FCGIApp) string { return a.Name },
		func(a, b *models.FCGIApp) bool { return a.Equal(*b) },
		sections.NewFCGIAppCreate,
		sections.NewFCGIAppDelete,
		sections.NewFCGIAppUpdate,
	)
}
