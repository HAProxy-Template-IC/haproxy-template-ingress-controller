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
	operations := make([]Operation, 0, len(desired.Mailers))

	// Convert slices to maps for easier comparison by Name
	currentMap := make(map[string]*models.MailersSection)
	for i := range current.Mailers {
		mailers := current.Mailers[i]
		if mailers.Name != "" {
			currentMap[mailers.Name] = mailers
		}
	}

	desiredMap := make(map[string]*models.MailersSection)
	for i := range desired.Mailers {
		mailers := desired.Mailers[i]
		if mailers.Name != "" {
			desiredMap[mailers.Name] = mailers
		}
	}

	// Find added mailers sections
	for name, mailers := range desiredMap {
		if _, exists := currentMap[name]; exists {
			continue
		}

		operations = append(operations, sections.NewMailersSectionCreate(mailers))

		// Also create mailer entries for this new mailers section using pointer index
		desiredEntries := desired.MailerEntryIndex[name]
		mailerEntryOps := c.compareMailerEntriesWithIndex(name, nil, desiredEntries)
		operations = append(operations, mailerEntryOps...)
	}

	// Find deleted mailers sections
	for name, mailers := range currentMap {
		if _, exists := desiredMap[name]; !exists {
			operations = append(operations, sections.NewMailersSectionDelete(mailers))
		}
	}

	// Find modified mailers sections
	for name, desiredMailers := range desiredMap {
		currentMailers, exists := currentMap[name]
		if !exists {
			continue
		}
		mailersModified := false

		// Compare mailer entries within this mailers section using pointer indexes
		currentEntries := current.MailerEntryIndex[name]
		desiredEntries := desired.MailerEntryIndex[name]
		mailerEntryOps := c.compareMailerEntriesWithIndex(name, currentEntries, desiredEntries)
		appendOperationsIfNotEmpty(&operations, mailerEntryOps, &mailersModified)

		// Compare mailers section attributes (excluding mailer entries which we already compared)
		if !mailersEqualWithoutMailerEntries(currentMailers, desiredMailers) {
			operations = append(operations, sections.NewMailersSectionUpdate(desiredMailers))
		}
	}

	return operations
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
	if currentEntries == nil {
		currentEntries = make(map[string]*models.MailerEntry)
	}
	if desiredEntries == nil {
		desiredEntries = make(map[string]*models.MailerEntry)
	}

	var operations []Operation

	// Find added entries
	for name, entry := range desiredEntries {
		if _, exists := currentEntries[name]; !exists {
			operations = append(operations, sections.NewMailerEntryCreate(mailersSection, entry))
		}
	}

	// Find deleted entries
	for name, entry := range currentEntries {
		if _, exists := desiredEntries[name]; !exists {
			operations = append(operations, sections.NewMailerEntryDelete(mailersSection, entry))
		}
	}

	// Find modified entries
	for name, desiredEntry := range desiredEntries {
		currentEntry, exists := currentEntries[name]
		if !exists {
			continue
		}
		if !currentEntry.Equal(*desiredEntry) {
			operations = append(operations, sections.NewMailerEntryUpdate(mailersSection, desiredEntry))
		}
	}

	return operations
}

// comparePeers compares peer sections between current and desired configurations.
// Uses pointer indexes for zero-copy iteration over peer entries.
func (c *Comparator) comparePeers(current, desired *parser.StructuredConfig) []Operation {
	operations := make([]Operation, 0, len(desired.Peers))

	// Convert slices to maps for easier comparison by Name
	currentMap := make(map[string]*models.PeerSection)
	for i := range current.Peers {
		peer := current.Peers[i]
		if peer.Name != "" {
			currentMap[peer.Name] = peer
		}
	}

	desiredMap := make(map[string]*models.PeerSection)
	for i := range desired.Peers {
		peer := desired.Peers[i]
		if peer.Name != "" {
			desiredMap[peer.Name] = peer
		}
	}

	// Find added peer sections
	for name, peer := range desiredMap {
		if _, exists := currentMap[name]; exists {
			continue
		}

		operations = append(operations, sections.NewPeerSectionCreate(peer))

		// Also create peer entries for this new peers section using pointer index
		desiredEntries := desired.PeerEntryIndex[name]
		peerEntryOps := c.comparePeerEntriesWithIndex(name, nil, desiredEntries)
		operations = append(operations, peerEntryOps...)
	}

	// Find deleted peer sections
	for name, peer := range currentMap {
		if _, exists := desiredMap[name]; !exists {
			operations = append(operations, sections.NewPeerSectionDelete(peer))
		}
	}

	// Find modified peer sections
	for name, desiredPeer := range desiredMap {
		currentPeer, exists := currentMap[name]
		if !exists {
			continue
		}
		peerModified := false

		// Compare peer entries within this peers section using pointer indexes
		currentEntries := current.PeerEntryIndex[name]
		desiredEntries := desired.PeerEntryIndex[name]
		peerEntryOps := c.comparePeerEntriesWithIndex(name, currentEntries, desiredEntries)
		appendOperationsIfNotEmpty(&operations, peerEntryOps, &peerModified)

		// Compare peers section attributes (excluding peer entries which we already compared)
		if !peersEqualWithoutPeerEntries(currentPeer, desiredPeer) {
			operations = append(operations, sections.NewPeerSectionUpdate(desiredPeer))
		}
	}

	return operations
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
	if currentEntries == nil {
		currentEntries = make(map[string]*models.PeerEntry)
	}
	if desiredEntries == nil {
		desiredEntries = make(map[string]*models.PeerEntry)
	}

	var operations []Operation

	// Find added entries
	for name, entry := range desiredEntries {
		if _, exists := currentEntries[name]; !exists {
			operations = append(operations, sections.NewPeerEntryCreate(peersSection, entry))
		}
	}

	// Find deleted entries
	for name, entry := range currentEntries {
		if _, exists := desiredEntries[name]; !exists {
			operations = append(operations, sections.NewPeerEntryDelete(peersSection, entry))
		}
	}

	// Find modified entries
	for name, desiredEntry := range desiredEntries {
		currentEntry, exists := currentEntries[name]
		if !exists {
			continue
		}
		if !currentEntry.Equal(*desiredEntry) {
			operations = append(operations, sections.NewPeerEntryUpdate(peersSection, desiredEntry))
		}
	}

	return operations
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
