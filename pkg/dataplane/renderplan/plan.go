// Copyright 2025 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// Package renderplan holds the structure a render declares about its own
// output: the ordered sections of haproxy.cfg, the backend records the
// generator emitted them from, the map entries, and the file set.
//
// The structure is produced by the generator (chart macros through
// pkg/controller/rendercontext.PlanRegistry), never by parsing HAProxy
// configuration. Pure data: no controller, templating or client-native imports.
package renderplan

import (
	"encoding/json"
	"fmt"

	"github.com/cespare/xxhash/v2"
)

// SchemaVersion identifies the plan format. A consumer reading a foreign
// version must treat the plan as unknown rather than decode it partially.
const SchemaVersion = 1

// Section kinds.
const (
	SectionKindCore    = "core"
	SectionKindProfile = "profile"
	SectionKindBackend = "backend"
)

// Backend shapes: a dynamic backend can be created and populated over the
// runtime API, a structural one needs a reload.
const (
	ShapeDynamic    = "dynamic"
	ShapeStructural = "structural"
)

// File kinds.
const (
	FileKindConfig  = "config"
	FileKindMap     = "map"
	FileKindCert    = "cert"
	FileKindCA      = "ca"
	FileKindCRTList = "crtlist"
	FileKindGeneral = "general"
)

// Plan is the immutable structure of one render.
type Plan struct {
	SchemaVersion int                `json:"schemaVersion"`
	ID            string             `json:"id"`
	Sections      []Section          `json:"sections"`
	Backends      map[string]Backend `json:"backends"`
	Profiles      map[string]Profile `json:"profiles"`
	Maps          map[string]Map     `json:"maps"`
	CRTLists      map[string]CRTList `json:"crtLists,omitempty"`
	Files         []File             `json:"files"`
}

// Section is one contiguous run of haproxy.cfg. The sections of a plan
// partition the file: concatenating their text reproduces it byte for byte.
type Section struct {
	Kind       string `json:"kind"`
	Name       string `json:"name"`
	TextDigest string `json:"textDigest"`
	Length     int    `json:"length"`
}

// Backend is the record a generator macro declared alongside the backend
// section text it emitted.
type Backend struct {
	Name          string       `json:"name"`
	Profile       string       `json:"profile,omitempty"`
	Mode          string       `json:"mode,omitempty"`
	GUID          string       `json:"guid,omitempty"`
	Balance       string       `json:"balance,omitempty"`
	HashType      string       `json:"hashType,omitempty"`
	Shape         string       `json:"shape"`
	ShapeReason   string       `json:"shapeReason,omitempty"`
	Servers       []Server     `json:"servers,omitempty"`
	DefaultServer []KeywordArg `json:"defaultServer,omitempty"`

	BodyDigest     string `json:"bodyDigest"`
	CommentsDigest string `json:"commentsDigest"`
	RecordDigest   string `json:"recordDigest"`
	TextDigest     string `json:"textDigest"`
}

// Server is one server line, declared as data.
type Server struct {
	Name     string       `json:"name"`
	Address  string       `json:"address"`
	Port     int          `json:"port"`
	Weight   *int         `json:"weight,omitempty"` // nil: no weight keyword; 0 is a real weight
	Disabled bool         `json:"disabled,omitempty"`
	GUID     string       `json:"guid,omitempty"`
	Comment  string       `json:"comment,omitempty"`
	Extra    []KeywordArg `json:"extra,omitempty"`
}

// KeywordArg is a server or default-server keyword with its arguments,
// structured so no consumer has to scan the emitted line.
type KeywordArg struct {
	Name string   `json:"name"`
	Args []string `json:"args,omitempty"`
}

// Profile is a named defaults section shared by backends.
type Profile struct {
	Name       string `json:"name"`
	BodyDigest string `json:"bodyDigest"`
	HasRules   bool   `json:"hasRules,omitempty"`
}

// Map is the entry list of one map file, derived from its rendered content.
type Map struct {
	Path    string  `json:"path"`
	Ordered bool    `json:"ordered"`
	Entries []Entry `json:"entries,omitempty"`
}

// Entry is one map-file line: the key and the rest of the line.
type Entry struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// CRTList is the entry list of one crt-list file, declared by the generator
// that emitted the file.
type CRTList struct {
	Path    string         `json:"path"`
	Entries []CRTListEntry `json:"entries,omitempty"`
}

// CRTListEntry is one crt-list line: the certificate, its ssl options and the
// SNI filters that select it.
type CRTListEntry struct {
	Cert       string       `json:"cert"`
	Options    []KeywordArg `json:"options,omitempty"`
	SNIFilters []string     `json:"sniFilters,omitempty"`
}

// File is one file the render produced.
type File struct {
	Path           string `json:"path"`
	Kind           string `json:"kind"`
	ReloadOnChange bool   `json:"reloadOnChange"`
	Digest         string `json:"digest"`
	Size           int64  `json:"size"`
}

// CurrentConfig is the client-native-free view of the running configuration
// that templates read as `currentConfig`.
type CurrentConfig struct {
	ServerIndex map[string]map[string]ServerAddr `json:"serverIndex"`
}

// ServerAddr is one server's address as templates see it. Port is a pointer
// because a server line may omit it and templates dereference it.
type ServerAddr struct {
	Address string
	Port    *int64
}

// Canonical returns the deterministic JSON encoding of the plan with the ID
// cleared, so the ID can be a digest over it. encoding/json sorts map keys and
// keeps struct fields in declaration order.
func (p *Plan) Canonical() []byte {
	clone := *p
	clone.ID = ""
	encoded, err := json.Marshal(&clone)
	if err != nil {
		// Only a new field that is not JSON-safe can get here, and digesting a
		// partial encoding would make two different plans compare equal.
		panic(fmt.Sprintf("renderplan: encoding the plan failed: %v", err))
	}
	return encoded
}

// ComputeID sets the plan ID to the digest of its canonical encoding.
func (p *Plan) ComputeID() {
	p.ID = Digest(p.Canonical())
}

// CurrentConfig projects the plan's backend servers into the shape templates
// read as `currentConfig`.
func (p *Plan) CurrentConfig() CurrentConfig {
	index := make(map[string]map[string]ServerAddr, len(p.Backends))
	for name := range p.Backends {
		declared := p.Backends[name].Servers
		if len(declared) == 0 {
			continue
		}
		servers := make(map[string]ServerAddr, len(declared))
		for _, server := range declared {
			port := int64(server.Port)
			servers[server.Name] = ServerAddr{Address: server.Address, Port: &port}
		}
		index[name] = servers
	}
	return CurrentConfig{ServerIndex: index}
}

// Digest returns the xxhash64 of b as fixed-width hex. It is the only hash the
// plan uses, so digests from different producers are comparable.
func Digest(b []byte) string {
	return fmt.Sprintf("%016x", xxhash.Sum64(b))
}

// DigestString is Digest over a string, without copying it to a byte slice.
func DigestString(s string) string {
	return fmt.Sprintf("%016x", xxhash.Sum64String(s))
}
