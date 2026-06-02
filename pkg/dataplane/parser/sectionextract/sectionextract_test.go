// Copyright 2025 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package sectionextract

import (
	"sort"
	"strings"
	"testing"

	parser "github.com/haproxytech/client-native/v6/config-parser"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/parser/parserconfig"
)

// All() is the single CE-section extraction pass shared by both the CE parser
// (pkg/dataplane/parser) and the Enterprise parser (pkg/dataplane/parser/enterprise).
// These tests exercise it directly on a raw config-parser so the shared contract
// is pinned independently of either caller.

func parseConfig(t *testing.T, cfg string) parser.Parser {
	t.Helper()
	p, err := parser.New()
	require.NoError(t, err)
	require.NoError(t, p.Process(strings.NewReader(cfg)))
	return p
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// TestAll_PopulatesEverySectionStyle covers both extraction styles in one pass:
//   - ParseSection-style sections (defaults, frontend, backend, peers, userlist)
//     where the section name is assigned AFTER parsing, and
//   - fill-style sections (resolvers, mailers) where the name MUST be assigned
//     BEFORE the parse call or client-native reports "section missing".
//
// The mailers/resolvers name-ordering is load-bearing: getting it wrong makes
// the whole section silently drop out (regression guard for exactly that bug).
func TestAll_PopulatesEverySectionStyle(t *testing.T) {
	const cfg = `
global
    daemon

defaults
    mode http
    timeout connect 5s
    timeout client 50s
    timeout server 50s

userlist mylist
    user alice insecure-password secret
    group admins

frontend fe_main
    bind *:80
    acl is_api path_beg /api
    default_backend be_api

backend be_api
    balance roundrobin
    server srv1 10.0.0.1:8080 check

peers mypeers
    peer haproxy1 192.168.0.1:1024

resolvers mydns
    nameserver dns_a 10.0.0.1:53

mailers mymail
    mailer smtp1 192.168.0.1:587
`

	conf := parserconfig.NewStructuredConfig()
	require.NoError(t, All(parseConfig(t, cfg), conf))

	require.NotNil(t, conf.Global, "global section must be extracted")

	require.Len(t, conf.Defaults, 1)
	assert.Equal(t, "http", conf.Defaults[0].Mode)

	require.Len(t, conf.Frontends, 1)
	assert.Equal(t, "fe_main", conf.Frontends[0].Name)
	assert.Equal(t, "be_api", conf.Frontends[0].DefaultBackend)
	require.Len(t, conf.Frontends[0].ACLList, 1)
	assert.Contains(t, sortedKeys(conf.BindIndex["fe_main"]), "*:80")

	require.Len(t, conf.Backends, 1)
	assert.Equal(t, "be_api", conf.Backends[0].Name)
	assert.Equal(t, []string{"srv1"}, sortedKeys(conf.ServerIndex["be_api"]))

	require.Len(t, conf.Userlists, 1)
	assert.Equal(t, "mylist", conf.Userlists[0].Name)

	// ParseSection-style named section: name assigned after parse.
	require.Len(t, conf.Peers, 1)
	assert.Equal(t, "mypeers", conf.Peers[0].Name)
	assert.Equal(t, []string{"haproxy1"}, sortedKeys(conf.PeerEntryIndex["mypeers"]))

	// Fill-style named sections: name MUST be set before the parse call.
	require.Len(t, conf.Resolvers, 1, "resolvers must survive extraction")
	assert.Equal(t, "mydns", conf.Resolvers[0].Name)
	assert.Equal(t, []string{"dns_a"}, sortedKeys(conf.NameserverIndex["mydns"]))

	require.Len(t, conf.Mailers, 1, "mailers must survive extraction (name set before ParseMailersSection)")
	assert.Equal(t, "mymail", conf.Mailers[0].Name)
	assert.Equal(t, []string{"smtp1"}, sortedKeys(conf.MailerEntryIndex["mymail"]))
}

// TestAll_EmptyConfigYieldsEmptySections confirms a minimal config produces no
// spurious sections and never errors (every "no such section" path is benign).
func TestAll_EmptyConfigYieldsEmptySections(t *testing.T) {
	conf := parserconfig.NewStructuredConfig()
	require.NoError(t, All(parseConfig(t, "global\n    daemon\n"), conf))

	assert.NotNil(t, conf.Global)
	assert.Empty(t, conf.Frontends)
	assert.Empty(t, conf.Backends)
	assert.Empty(t, conf.Peers)
	assert.Empty(t, conf.Resolvers)
	assert.Empty(t, conf.Mailers)
}
