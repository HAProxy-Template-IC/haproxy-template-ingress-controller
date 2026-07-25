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

package dataplane

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/auxiliaryfiles"
)

// CurrentFiles must expose the three CRD-backed aux kinds (map, general,
// crt-list) keyed by base filename, and must NOT expose SSL certificate or CA
// content — their private keys must never enter the render context.
func TestAuxiliaryFiles_CurrentFiles(t *testing.T) {
	af := &AuxiliaryFiles{
		MapFiles:        []auxiliaryfiles.MapFile{{Path: "maps/host.map", Content: "m"}},
		GeneralFiles:    []auxiliaryfiles.GeneralFile{{Filename: "tls-ticket-keys", Path: "general/tls-ticket-keys", Content: "g"}},
		CRTListFiles:    []auxiliaryfiles.CRTListFile{{Path: "ssl/https.crtlist", Content: "c"}},
		SSLCertificates: []auxiliaryfiles.SSLCertificate{{Path: "ssl/cert.pem", Content: "-----BEGIN PRIVATE KEY-----"}},
		SSLCaFiles:      []auxiliaryfiles.SSLCaFile{{Path: "ssl/ca.pem", Content: "-----BEGIN CERTIFICATE-----"}},
	}

	got := af.CurrentFiles()
	assert.Equal(t, "m", got["host.map"], "map file keyed by base filename")
	assert.Equal(t, "g", got["tls-ticket-keys"])
	assert.Equal(t, "c", got["https.crtlist"])
	assert.NotContains(t, got, "cert.pem", "SSL certificate content must be excluded")
	assert.NotContains(t, got, "ca.pem", "CA file content must be excluded")
	assert.Len(t, got, 3)
}

// A nil receiver returns nil (webhook dry-run / no prior render), never panics.
func TestAuxiliaryFiles_CurrentFiles_Nil(t *testing.T) {
	var af *AuxiliaryFiles
	assert.Nil(t, af.CurrentFiles())
}
