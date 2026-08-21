//go:build playground

// Package parserconfig holds StructuredConfig, what the client-native parse of
// a HAProxy configuration produces. It exists for the browser playground's
// schema check only — no production binary parses HAProxy configuration.
package parserconfig

import (
	"github.com/haproxytech/client-native/v6/models"
)

// StructuredConfig holds all parsed configuration sections.
type StructuredConfig struct {
	Global      *models.Global
	Defaults    []*models.Defaults
	Frontends   []*models.Frontend
	Backends    []*models.Backend
	Peers       []*models.PeerSection
	Resolvers   []*models.Resolver
	Mailers     []*models.MailersSection
	Caches      []*models.Cache
	Rings       []*models.Ring
	HTTPErrors  []*models.HTTPErrorsSection
	Userlists   []*models.Userlist
	LogForwards []*models.LogForward
	FCGIApps    []*models.FCGIApp
	CrtStores   []*models.CrtStore

	// Observability sections (v3.1+ features)
	LogProfiles []*models.LogProfile // log-profile sections
	Traces      *models.Traces       // traces section (singleton)

	// Certificate automation (v3.2+ features)
	AcmeProviders []*models.AcmeProvider // acme sections for Let's Encrypt/ACME automation

	// Pointer-based indexes for zero-copy iteration
	//
	// These indexes store pointers to nested elements, enabling zero-copy iteration
	// during comparison and validation. The upstream client-native library uses
	// value maps (map[string]T) which cause struct copies on every access.
	// By storing pointers, we avoid copying large structs (e.g., Server is 1504 bytes).
	//
	// These indexes are built during parsing and should be used by comparators
	// and validators instead of the value maps in the parent models.
	// The value maps in models (e.g., Backend.Servers) remain nil.

	// ServerIndex maps backend name -> server name -> server pointer
	ServerIndex map[string]map[string]*models.Server

	// ServerTemplateIndex maps backend name -> template prefix -> server template pointer
	ServerTemplateIndex map[string]map[string]*models.ServerTemplate

	// BindIndex maps frontend name -> bind name -> bind pointer
	BindIndex map[string]map[string]*models.Bind

	// PeerEntryIndex maps peer section name -> peer entry name -> peer entry pointer
	PeerEntryIndex map[string]map[string]*models.PeerEntry

	// NameserverIndex maps resolver name -> nameserver name -> nameserver pointer
	NameserverIndex map[string]map[string]*models.Nameserver

	// MailerEntryIndex maps mailers section name -> mailer entry name -> mailer entry pointer
	MailerEntryIndex map[string]map[string]*models.MailerEntry

	// UserIndex maps userlist name -> username -> user pointer
	UserIndex map[string]map[string]*models.User

	// GroupIndex maps userlist name -> group name -> group pointer
	GroupIndex map[string]map[string]*models.Group
}

// NewStructuredConfig allocates a StructuredConfig with all pointer-based
// indexes pre-initialised so callers can write to them without an additional
// nil-check.
func NewStructuredConfig() *StructuredConfig {
	return &StructuredConfig{
		ServerIndex:         make(map[string]map[string]*models.Server),
		ServerTemplateIndex: make(map[string]map[string]*models.ServerTemplate),
		BindIndex:           make(map[string]map[string]*models.Bind),
		PeerEntryIndex:      make(map[string]map[string]*models.PeerEntry),
		NameserverIndex:     make(map[string]map[string]*models.Nameserver),
		MailerEntryIndex:    make(map[string]map[string]*models.MailerEntry),
		UserIndex:           make(map[string]map[string]*models.User),
		GroupIndex:          make(map[string]map[string]*models.Group),
	}
}

// BuildPointerIndex builds a pointer index from a slice of items, keyed by
// the value returned by getKey. Nil items and items with an empty key are
// skipped. Returns nil if the input slice is nil so callers can keep the
// section's index unset when there is nothing to record.
func BuildPointerIndex[T any](items []*T, getKey func(*T) string) map[string]*T {
	if items == nil {
		return nil
	}
	index := make(map[string]*T, len(items))
	for _, item := range items {
		if item == nil {
			continue
		}
		if key := getKey(item); key != "" {
			index[key] = item
		}
	}
	return index
}
