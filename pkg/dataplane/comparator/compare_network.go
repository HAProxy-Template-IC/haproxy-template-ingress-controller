package comparator

import (
	"github.com/haproxytech/client-native/v6/models"

	"haptic/pkg/dataplane/comparator/sections"
	"haptic/pkg/dataplane/parser"
)

// compareResolvers compares resolver sections between current and desired configurations.
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

		// Also create nameserver entries for this new resolver section
		// Compare against an empty resolver section to get all nameserver create operations
		emptyResolver := &models.Resolver{}
		emptyResolver.Name = name
		emptyResolver.Nameservers = make(map[string]models.Nameserver)
		nameserverOps := c.compareNameservers(name, emptyResolver, resolver)
		if len(nameserverOps) > 0 {
			operations = append(operations, nameserverOps...)
		}
	}

	// Find deleted resolver sections
	for name, resolver := range currentMap {
		if _, exists := desiredMap[name]; !exists {
			operations = append(operations, sections.NewResolverDelete(resolver))
		}
	}

	// Find modified resolver sections
	for name, desiredResolver := range desiredMap {
		if currentResolver, exists := currentMap[name]; exists {
			resolverModified := false

			// Compare nameserver entries within this resolver section
			nameserverOps := c.compareNameservers(name, currentResolver, desiredResolver)
			appendOperationsIfNotEmpty(&operations, nameserverOps, &resolverModified)

			// Compare resolver section attributes (excluding nameserver entries which we already compared)
			if !resolversEqualWithoutNameservers(currentResolver, desiredResolver) {
				operations = append(operations, sections.NewResolverUpdate(desiredResolver))
			}
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

// compareNameservers compares nameserver configurations within a resolver section.
func (c *Comparator) compareNameservers(resolverSection string, currentResolver, desiredResolver *models.Resolver) []Operation {
	return compareMapEntries(
		currentResolver.Nameservers,
		desiredResolver.Nameservers,
		func(entry *models.Nameserver) Operation {
			return sections.NewNameserverCreate(resolverSection, entry)
		},
		func(entry *models.Nameserver) Operation {
			return sections.NewNameserverDelete(resolverSection, entry)
		},
		func(entry *models.Nameserver) Operation {
			return sections.NewNameserverUpdate(resolverSection, entry)
		},
		nameserversEqual,
	)
}

// nameserversEqual checks if two nameserver entries are equal.
// Uses the HAProxy models' built-in Equal() method to compare ALL attributes.
func nameserversEqual(n1, n2 *models.Nameserver) bool {
	return n1.Equal(*n2)
}
