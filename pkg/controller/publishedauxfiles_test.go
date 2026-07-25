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

package controller

import (
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"gitlab.com/haproxy-haptic/haptic/pkg/compression"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/events"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/auxiliaryfiles"
	busevents "gitlab.com/haproxy-haptic/haptic/pkg/events"
	"gitlab.com/haproxy-haptic/haptic/pkg/k8s/store"
)

func auxUnstructured(name, filePath, contentField, content string, compressed bool) *unstructured.Unstructured {
	spec := map[string]any{"path": filePath, contentField: content}
	if compressed {
		spec["compressed"] = true
	}
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "haproxy-haptic.org/v1alpha1",
		"kind":       "HAProxyGeneralFile",
		"metadata":   map[string]any{"name": name, "namespace": "haptic"},
		"spec":       spec,
	}}
}

// auxFilesFromStore must flatten aux CRD objects into base-filename → content,
// reading content from the kind's content field, decompressing compressed values
// and skipping objects with no path.
func TestAuxFilesFromStore(t *testing.T) {
	plain := "key-a\nkey-b\nkey-c\n"
	body := "HTTP/1.0 503 Service Unavailable\r\n\r\nlong error page body ...\n"

	s := store.NewMemoryStore(1)
	require.NoError(t, s.Add(auxUnstructured("gf-ticket", "general/tls-ticket-keys", "content", plain, false), []string{"gf-ticket"}))
	require.NoError(t, s.Add(auxUnstructured("gf-503", "general/503.http", "content", compression.Compress(body), true), []string{"gf-503"}))
	// No path → skipped.
	require.NoError(t, s.Add(auxUnstructured("gf-bad", "", "content", "orphan", false), []string{"gf-bad"}))

	got := auxFilesFromStore(s, "content", slog.Default())
	assert.Equal(t, plain, got["tls-ticket-keys"], "keyed by base filename")
	assert.Equal(t, body, got["503.http"], "compressed content decompressed")
	assert.Len(t, got, 2)
}

// Map files store content under `entries`, not `content` — the extractor must
// read whichever field the kind declares.
func TestAuxFilesFromStore_EntriesField(t *testing.T) {
	s := store.NewMemoryStore(1)
	require.NoError(t, s.Add(auxUnstructured("mf-host", "maps/host.map", "entries", "example.com be_x\n", false), []string{"mf-host"}))

	got := auxFilesFromStore(s, "entries", slog.Default())
	assert.Equal(t, "example.com be_x\n", got["host.map"])
}

// currentFilesFromAux must expose the three CRD-backed aux kinds (map, general,
// crt-list) keyed by base filename, and must NOT expose SSL certificate content
// (private keys stay out of the render context).
func TestCurrentFilesFromAux_CRDBackedTypesExcludeSSL(t *testing.T) {
	af := &dataplane.AuxiliaryFiles{
		MapFiles:        []auxiliaryfiles.MapFile{{Path: "host.map", Content: "m"}},
		GeneralFiles:    []auxiliaryfiles.GeneralFile{{Filename: "tls-ticket-keys", Path: "general/tls-ticket-keys", Content: "g"}},
		CRTListFiles:    []auxiliaryfiles.CRTListFile{{Path: "ssl/https.crtlist", Content: "c"}},
		SSLCertificates: []auxiliaryfiles.SSLCertificate{{Path: "ssl/cert.pem", Content: "-----BEGIN PRIVATE KEY-----"}},
	}

	got := currentFilesFromAux(af)
	assert.Equal(t, "m", got["host.map"])
	assert.Equal(t, "g", got["tls-ticket-keys"])
	assert.Equal(t, "c", got["https.crtlist"])
	assert.NotContains(t, got, "cert.pem", "SSL certificate content must not reach currentFiles")
	assert.Len(t, got, 3)
}

// The provider returns the merged published snapshot before any render (cold
// start / follower), then the live render output afterward.
func TestCurrentAuxFilesProvider_PublishedFallbackThenRenderWins(t *testing.T) {
	published := newPublishedAuxFiles()
	published.setForGVR(haproxyGeneralFileGVR.String(), map[string]string{"tls-ticket-keys": "published-keys"})
	published.setForGVR(haproxyMapFileGVR.String(), map[string]string{"host.map": "published-map"})

	sc := NewStateCache(busevents.NewEventBus(10), nil, slog.Default())
	provider := currentAuxFilesProvider(sc, published)

	cold := provider()
	assert.Equal(t, "published-keys", cold["tls-ticket-keys"])
	assert.Equal(t, "published-map", cold["host.map"], "snapshot merges across aux kinds")

	sc.handleTemplateRendered(events.NewTemplateRenderedEvent(
		"global\n",
		&dataplane.AuxiliaryFiles{
			GeneralFiles: []auxiliaryfiles.GeneralFile{{Filename: "tls-ticket-keys", Path: "general/tls-ticket-keys", Content: "render-keys"}},
		},
		nil, nil, 1, 100, "", "", true,
	))
	assert.Equal(t, "render-keys", provider()["tls-ticket-keys"], "live render wins over published snapshot")
}

// A nil published store degrades to the pre-existing empty behavior without
// panicking (e.g. the webhook dry-run path never wires a store).
func TestCurrentAuxFilesProvider_NilPublishedStore(t *testing.T) {
	sc := NewStateCache(busevents.NewEventBus(10), nil, slog.Default())
	assert.Nil(t, currentAuxFilesProvider(sc, nil)())
}
