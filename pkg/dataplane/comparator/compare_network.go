package comparator

import (
	"github.com/haproxytech/client-native/v6/models"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/comparator/sections"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/parser"
)

// compareResolvers compares resolver sections between current and desired configurations.
// Uses pointer indexes for zero-copy iteration over nameservers.
func (c *Comparator) compareResolvers(current, desired *parser.StructuredConfig) []Operation {
	return compareContainerSection(
		current.Resolvers, desired.Resolvers,
		current.NameserverIndex, desired.NameserverIndex,
		func(r *models.Resolver) string { return r.Name },
		resolversEqualWithoutNameservers,
		sections.ResolverOps.Create,
		sections.ResolverOps.Delete,
		sections.ResolverOps.Update,
		c.compareNameserversWithIndex,
	)
}

// resolversEqualWithoutNameservers checks if two resolver sections are equal, excluding nameserver entries.
// Uses the HAProxy models' built-in Equal() method to compare resolver section attributes
// (name, timeouts, etc.) automatically, excluding nameserver entries we compare separately.
func resolversEqualWithoutNameservers(r1, r2 *models.Resolver) bool {
	// Create copies to avoid modifying originals
	r1Copy := *r1
	r2Copy := *r2

	// Clear nameserver entries so they don't affect comparison
	r1Copy.Nameservers = nil
	r2Copy.Nameservers = nil

	return r1Copy.Equal(r2Copy)
}

// compareNameserversWithIndex compares nameserver configurations using pointer indexes.
func (c *Comparator) compareNameserversWithIndex(resolverSection string, currentNameservers, desiredNameservers map[string]*models.Nameserver) []Operation {
	return compareNamedMaps(
		currentNameservers, desiredNameservers,
		func(a, b *models.Nameserver) bool { return a.Equal(*b) },
		func(n *models.Nameserver) Operation { return sections.NameserverOps.Create(resolverSection, n) },
		func(n *models.Nameserver) Operation { return sections.NameserverOps.Delete(resolverSection, n) },
		func(n *models.Nameserver) Operation { return sections.NameserverOps.Update(resolverSection, n) },
	)
}
