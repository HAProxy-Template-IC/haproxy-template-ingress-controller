package comparator

import (
	"github.com/haproxytech/client-native/v6/models"

	"gitlab.com/haproxy-template-ic/haproxy-template-ingress-controller/pkg/dataplane/comparator/sections"
	"gitlab.com/haproxy-template-ic/haproxy-template-ingress-controller/pkg/dataplane/parser"
)

// compareHTTPErrors compares http-errors sections between current and desired configurations.
func (c *Comparator) compareHTTPErrors(current, desired *parser.StructuredConfig) []Operation {
	var operations []Operation

	// Convert slices to maps for easier comparison by Name
	currentMap := make(map[string]*models.HTTPErrorsSection)
	for i := range current.HTTPErrors {
		httpError := current.HTTPErrors[i]
		if httpError.Name != "" {
			currentMap[httpError.Name] = httpError
		}
	}

	desiredMap := make(map[string]*models.HTTPErrorsSection)
	for i := range desired.HTTPErrors {
		httpError := desired.HTTPErrors[i]
		if httpError.Name != "" {
			desiredMap[httpError.Name] = httpError
		}
	}

	// Find added http-errors sections
	for name, httpError := range desiredMap {
		if _, exists := currentMap[name]; !exists {
			operations = append(operations, sections.NewHTTPErrorsSectionCreate(httpError))
		}
	}

	// Find deleted http-errors sections
	for name, httpError := range currentMap {
		if _, exists := desiredMap[name]; !exists {
			operations = append(operations, sections.NewHTTPErrorsSectionDelete(httpError))
		}
	}

	// Find modified http-errors sections
	for name, desiredHTTPError := range desiredMap {
		if currentHTTPError, exists := currentMap[name]; exists {
			// Compare http-errors sections using Equal() method
			if !httpErrorsEqual(currentHTTPError, desiredHTTPError) {
				operations = append(operations, sections.NewHTTPErrorsSectionUpdate(desiredHTTPError))
			}
		}
	}

	return operations
}

// httpErrorsEqual compares two http-errors sections for equality.
func httpErrorsEqual(h1, h2 *models.HTTPErrorsSection) bool {
	return h1.Equal(*h2)
}

// compareMailers compares mailers sections between current and desired configurations.
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

		// Also create mailer entries for this new mailers section
		// Compare against an empty mailers section to get all mailer entry create operations
		emptyMailers := &models.MailersSection{}
		emptyMailers.Name = name
		emptyMailers.MailerEntries = make(map[string]models.MailerEntry)
		mailerEntryOps := c.compareMailerEntries(name, emptyMailers, mailers)
		if len(mailerEntryOps) > 0 {
			operations = append(operations, mailerEntryOps...)
		}
	}

	// Find deleted mailers sections
	for name, mailers := range currentMap {
		if _, exists := desiredMap[name]; !exists {
			operations = append(operations, sections.NewMailersSectionDelete(mailers))
		}
	}

	// Find modified mailers sections
	for name, desiredMailers := range desiredMap {
		if currentMailers, exists := currentMap[name]; exists {
			mailersModified := false

			// Compare mailer entries within this mailers section
			mailerEntryOps := c.compareMailerEntries(name, currentMailers, desiredMailers)
			appendOperationsIfNotEmpty(&operations, mailerEntryOps, &mailersModified)

			// Compare mailers section attributes (excluding mailer entries which we already compared)
			if !mailersEqualWithoutMailerEntries(currentMailers, desiredMailers) {
				operations = append(operations, sections.NewMailersSectionUpdate(desiredMailers))
			}
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

// compareMailerEntries compares mailer entry configurations within a mailers section.
func (c *Comparator) compareMailerEntries(mailersSection string, currentMailers, desiredMailers *models.MailersSection) []Operation {
	return compareMapEntries(
		currentMailers.MailerEntries,
		desiredMailers.MailerEntries,
		func(entry *models.MailerEntry) Operation {
			return sections.NewMailerEntryCreate(mailersSection, entry)
		},
		func(entry *models.MailerEntry) Operation {
			return sections.NewMailerEntryDelete(mailersSection, entry)
		},
		func(entry *models.MailerEntry) Operation {
			return sections.NewMailerEntryUpdate(mailersSection, entry)
		},
		mailerEntriesEqual,
	)
}

// mailerEntriesEqual checks if two mailer entries are equal.
// Uses the HAProxy models' built-in Equal() method to compare ALL attributes.
func mailerEntriesEqual(e1, e2 *models.MailerEntry) bool {
	return e1.Equal(*e2)
}

// comparePeers compares peer sections between current and desired configurations.
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

		// Also create peer entries for this new peers section
		// Compare against an empty peers section to get all peer entry create operations
		emptyPeer := &models.PeerSection{}
		emptyPeer.Name = name
		emptyPeer.PeerEntries = make(map[string]models.PeerEntry)
		peerEntryOps := c.comparePeerEntries(name, emptyPeer, peer)
		if len(peerEntryOps) > 0 {
			operations = append(operations, peerEntryOps...)
		}
	}

	// Find deleted peer sections
	for name, peer := range currentMap {
		if _, exists := desiredMap[name]; !exists {
			operations = append(operations, sections.NewPeerSectionDelete(peer))
		}
	}

	// Find modified peer sections
	for name, desiredPeer := range desiredMap {
		if currentPeer, exists := currentMap[name]; exists {
			peerModified := false

			// Compare peer entries within this peers section
			peerEntryOps := c.comparePeerEntries(name, currentPeer, desiredPeer)
			appendOperationsIfNotEmpty(&operations, peerEntryOps, &peerModified)

			// Compare peers section attributes (excluding peer entries which we already compared)
			if !peersEqualWithoutPeerEntries(currentPeer, desiredPeer) {
				operations = append(operations, sections.NewPeerSectionUpdate(desiredPeer))
			}
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

// comparePeerEntries compares peer entry configurations within a peers section.
func (c *Comparator) comparePeerEntries(peersSection string, currentPeer, desiredPeer *models.PeerSection) []Operation {
	return compareMapEntries(
		currentPeer.PeerEntries,
		desiredPeer.PeerEntries,
		func(entry *models.PeerEntry) Operation {
			return sections.NewPeerEntryCreate(peersSection, entry)
		},
		func(entry *models.PeerEntry) Operation {
			return sections.NewPeerEntryDelete(peersSection, entry)
		},
		func(entry *models.PeerEntry) Operation {
			return sections.NewPeerEntryUpdate(peersSection, entry)
		},
		peerEntriesEqual,
	)
}

// peerEntriesEqual checks if two peer entries are equal.
// Uses the HAProxy models' built-in Equal() method to compare ALL attributes.
func peerEntriesEqual(p1, p2 *models.PeerEntry) bool {
	return p1.Equal(*p2)
}

// compareCaches compares cache sections between current and desired configurations.
func (c *Comparator) compareCaches(current, desired *parser.StructuredConfig) []Operation {
	var operations []Operation

	// Convert slices to maps for easier comparison by Name
	currentMap := make(map[string]*models.Cache)
	for i := range current.Caches {
		cache := current.Caches[i]
		if cache.Name != nil && *cache.Name != "" {
			currentMap[*cache.Name] = cache
		}
	}

	desiredMap := make(map[string]*models.Cache)
	for i := range desired.Caches {
		cache := desired.Caches[i]
		if cache.Name != nil && *cache.Name != "" {
			desiredMap[*cache.Name] = cache
		}
	}

	// Find added cache sections
	for name, cache := range desiredMap {
		if _, exists := currentMap[name]; !exists {
			operations = append(operations, sections.NewCacheCreate(cache))
		}
	}

	// Find deleted cache sections
	for name, cache := range currentMap {
		if _, exists := desiredMap[name]; !exists {
			operations = append(operations, sections.NewCacheDelete(cache))
		}
	}

	// Find modified cache sections
	for name, desiredCache := range desiredMap {
		if currentCache, exists := currentMap[name]; exists {
			if !cacheEqual(currentCache, desiredCache) {
				operations = append(operations, sections.NewCacheUpdate(desiredCache))
			}
		}
	}

	return operations
}

// cacheEqual compares two cache sections for equality.
func cacheEqual(c1, c2 *models.Cache) bool {
	return c1.Equal(*c2)
}

// compareRings compares ring sections between current and desired configurations.
func (c *Comparator) compareRings(current, desired *parser.StructuredConfig) []Operation {
	var operations []Operation

	// Convert slices to maps for easier comparison by Name
	currentMap := make(map[string]*models.Ring)
	for i := range current.Rings {
		ring := current.Rings[i]
		if ring.Name != "" {
			currentMap[ring.Name] = ring
		}
	}

	desiredMap := make(map[string]*models.Ring)
	for i := range desired.Rings {
		ring := desired.Rings[i]
		if ring.Name != "" {
			desiredMap[ring.Name] = ring
		}
	}

	// Find added ring sections
	for name, ring := range desiredMap {
		if _, exists := currentMap[name]; !exists {
			operations = append(operations, sections.NewRingCreate(ring))
		}
	}

	// Find deleted ring sections
	for name, ring := range currentMap {
		if _, exists := desiredMap[name]; !exists {
			operations = append(operations, sections.NewRingDelete(ring))
		}
	}

	// Find modified ring sections
	for name, desiredRing := range desiredMap {
		if currentRing, exists := currentMap[name]; exists {
			if !ringEqual(currentRing, desiredRing) {
				operations = append(operations, sections.NewRingUpdate(desiredRing))
			}
		}
	}

	return operations
}

// ringEqual compares two ring sections for equality.
func ringEqual(r1, r2 *models.Ring) bool {
	return r1.Equal(*r2)
}

// comparePrograms compares program sections between current and desired configurations.
func (c *Comparator) comparePrograms(current, desired *parser.StructuredConfig) []Operation {
	var operations []Operation

	// Convert slices to maps for easier comparison by Name
	currentMap := make(map[string]*models.Program)
	for i := range current.Programs {
		program := current.Programs[i]
		if program.Name != "" {
			currentMap[program.Name] = program
		}
	}

	desiredMap := make(map[string]*models.Program)
	for i := range desired.Programs {
		program := desired.Programs[i]
		if program.Name != "" {
			desiredMap[program.Name] = program
		}
	}

	// Find added program sections
	for name, program := range desiredMap {
		if _, exists := currentMap[name]; !exists {
			operations = append(operations, sections.NewProgramCreate(program))
		}
	}

	// Find deleted program sections
	for name, program := range currentMap {
		if _, exists := desiredMap[name]; !exists {
			operations = append(operations, sections.NewProgramDelete(program))
		}
	}

	// Find modified program sections
	for name, desiredProgram := range desiredMap {
		if currentProgram, exists := currentMap[name]; exists {
			if !programEqual(currentProgram, desiredProgram) {
				operations = append(operations, sections.NewProgramUpdate(desiredProgram))
			}
		}
	}

	return operations
}

// programEqual compares two program sections for equality.
func programEqual(p1, p2 *models.Program) bool {
	return p1.Equal(*p2)
}

// compareFCGIApps compares fcgi-app sections between current and desired configurations.
func (c *Comparator) compareFCGIApps(current, desired *parser.StructuredConfig) []Operation {
	var operations []Operation

	// Convert slices to maps for easier comparison by Name
	currentMap := make(map[string]*models.FCGIApp)
	for i := range current.FCGIApps {
		fcgiApp := current.FCGIApps[i]
		if fcgiApp.Name != "" {
			currentMap[fcgiApp.Name] = fcgiApp
		}
	}

	desiredMap := make(map[string]*models.FCGIApp)
	for i := range desired.FCGIApps {
		fcgiApp := desired.FCGIApps[i]
		if fcgiApp.Name != "" {
			desiredMap[fcgiApp.Name] = fcgiApp
		}
	}

	// Find added fcgi-app sections
	for name, fcgiApp := range desiredMap {
		if _, exists := currentMap[name]; !exists {
			operations = append(operations, sections.NewFCGIAppCreate(fcgiApp))
		}
	}

	// Find deleted fcgi-app sections
	for name, fcgiApp := range currentMap {
		if _, exists := desiredMap[name]; !exists {
			operations = append(operations, sections.NewFCGIAppDelete(fcgiApp))
		}
	}

	// Find modified fcgi-app sections
	for name, desiredFCGIApp := range desiredMap {
		if currentFCGIApp, exists := currentMap[name]; exists {
			if !fcgiAppEqual(currentFCGIApp, desiredFCGIApp) {
				operations = append(operations, sections.NewFCGIAppUpdate(desiredFCGIApp))
			}
		}
	}

	return operations
}

// fcgiAppEqual compares two fcgi-app sections for equality.
func fcgiAppEqual(f1, f2 *models.FCGIApp) bool {
	return f1.Equal(*f2)
}
