package comparator

import (
	"github.com/haproxytech/client-native/v6/models"

	"haptic/pkg/dataplane/comparator/sections"
	"haptic/pkg/dataplane/parser"
)

// compareAcmeProviders compares acme sections between current and desired configurations.
// ACME providers are only available in HAProxy DataPlane API v3.2+.
func (c *Comparator) compareAcmeProviders(current, desired *parser.StructuredConfig) []Operation {
	return compareNamedSections(
		current.AcmeProviders,
		desired.AcmeProviders,
		func(ap *models.AcmeProvider) string { return ap.Name },
		func(a1, a2 *models.AcmeProvider) bool { return a1.Equal(*a2) },
		func(ap *models.AcmeProvider) Operation { return sections.NewAcmeProviderCreate(ap) },
		func(ap *models.AcmeProvider) Operation { return sections.NewAcmeProviderDelete(ap) },
		func(ap *models.AcmeProvider) Operation { return sections.NewAcmeProviderUpdate(ap) },
	)
}
