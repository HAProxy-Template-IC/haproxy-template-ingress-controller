package comparator

import (
	"github.com/haproxytech/client-native/v6/models"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/comparator/sections"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/parser"
)

// compareResolvers compares resolver sections between current and desired configurations.
// Uses pointer indexes for zero-copy iteration over nameservers.
func (c *Comparator) compareResolvers(current, desired *parser.StructuredConfig) []Operation {
	operations := make([]Operation, 0, len(desired.Resolvers))

	// Convert slices to maps for easier comparison by Name
	currentMap := make(map[string]*models.Resolver)
	for i := range current.Resolvers {
		resolver := current.Resolvers[i]
		if resolver.Name != "" {
			currentMap[resolver.Name] = resolver
		}
	}

	desiredMap := make(map[string]*models.Resolver)
	for i := range desired.Resolvers {
		resolver := desired.Resolvers[i]
		if resolver.Name != "" {
			desiredMap[resolver.Name] = resolver
		}
	}

	// Find added resolver sections
	for name, resolver := range desiredMap {
		if _, exists := currentMap[name]; exists {
			continue
		}

		operations = append(operations, sections.NewResolverCreate(resolver))

		// Also create nameserver entries for this new resolver section using pointer index
		desiredNameservers := desired.NameserverIndex[name]
		nameserverOps := c.compareNameserversWithIndex(name, nil, desiredNameservers)
		operations = append(operations, nameserverOps...)
	}

	// Find deleted resolver sections
	for name, resolver := range currentMap {
		if _, exists := desiredMap[name]; !exists {
			operations = append(operations, sections.NewResolverDelete(resolver))
		}
	}

	// Find modified resolver sections
	for name, desiredResolver := range desiredMap {
		currentResolver, exists := currentMap[name]
		if !exists {
			continue
		}
		// Compare nameserver entries within this resolver section using pointer indexes
		operations = append(operations, c.compareNameserversWithIndex(name, current.NameserverIndex[name], desired.NameserverIndex[name])...)

		// Compare resolver section attributes (excluding nameserver entries which we already compared)
		if !resolversEqualWithoutNameservers(currentResolver, desiredResolver) {
			operations = append(operations, sections.NewResolverUpdate(desiredResolver))
		}
	}

	return operations
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
		func(n *models.Nameserver) Operation { return sections.NewNameserverCreate(resolverSection, n) },
		func(n *models.Nameserver) Operation { return sections.NewNameserverDelete(resolverSection, n) },
		func(n *models.Nameserver) Operation { return sections.NewNameserverUpdate(resolverSection, n) },
	)
}
