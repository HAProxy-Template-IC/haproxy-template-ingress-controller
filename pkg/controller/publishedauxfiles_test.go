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
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"gitlab.com/haproxy-haptic/haptic/pkg/compression"
	"gitlab.com/haproxy-haptic/haptic/pkg/k8s/store"
	"gitlab.com/haproxy-haptic/haptic/pkg/k8s/types"
)

type delayedPublishedSyncer struct {
	store types.Store
}

func (s *delayedPublishedSyncer) WaitForSync(context.Context) (int, error) {
	return 1, nil
}

func (s *delayedPublishedSyncer) Store() types.Store {
	return s.store
}

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

func publishedKind(t *testing.T, gvr string) *publishedAuxCRD {
	t.Helper()
	for _, kind := range publishedAuxCRDList() {
		if kind.gvr.String() == gvr {
			return &kind
		}
	}
	t.Fatalf("published kind %s not found", gvr)
	return nil
}

func publishedSnapshot(t *testing.T, published *publishedAuxFiles) map[string]string {
	t.Helper()
	files, err := published.get()
	require.NoError(t, err)
	return files
}

func TestSyncAndRefreshPublishedStoreDoesNotDependOnDelayedCallbacks(t *testing.T) {
	const (
		runtimeConfigName = "example-haproxycfg"
		setID             = "sha256:set-a"
	)
	mapGVR := haproxyMapFileGVR.String()
	published := newPublishedAuxFiles("haptic")

	childStore := store.NewMemoryStore(1)
	child := auxUnstructured("routes", "maps/routes.map", "entries", "example.test backend", false)
	child.SetAnnotations(map[string]string{"haproxy-haptic.org/auxiliary-set-id": setID})
	require.NoError(t, childStore.Add(child, []string{"routes"}))
	require.NoError(t, syncAndRefreshPublishedStore(t.Context(), &delayedPublishedSyncer{store: childStore},
		func(s types.Store, _ types.ChangeStats) {
			files, err := publishedAuxFilesFromStore(s, publishedKind(t, mapGVR))
			require.NoError(t, err)
			published.setForGVR(mapGVR, files)
		}))

	parentStore := store.NewMemoryStore(1)
	parent := &unstructured.Unstructured{Object: map[string]any{
		"metadata": map[string]any{"name": runtimeConfigName},
		"status": map[string]any{"auxiliaryFiles": map[string]any{
			"setID": setID,
			"mapFiles": []any{map[string]any{
				"kind": "HAProxyMapFile", "name": "routes", "namespace": "haptic",
			}},
		}},
	}}
	require.NoError(t, parentStore.Add(parent, []string{runtimeConfigName}))
	require.NoError(t, syncAndRefreshPublishedStore(t.Context(), &delayedPublishedSyncer{store: parentStore},
		func(s types.Store, _ types.ChangeStats) {
			commit, found, err := publishedAuxCommitFromStore(s, runtimeConfigName)
			require.NoError(t, err)
			require.True(t, found)
			published.setCommit(commit)
		}))

	require.NoError(t, published.readinessError())
	assert.Equal(t, map[string]string{"routes.map": "example.test backend"}, publishedSnapshot(t, published))
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

	got, err := publishedAuxFilesFromStore(s, publishedKind(t, haproxyGeneralFileGVR.String()))
	require.NoError(t, err)
	assert.Equal(t, plain, got["gf-ticket"].content)
	assert.Equal(t, body, got["gf-503"].content)
	assert.Len(t, got, 2)
}

// Map files store content under `entries`, not `content` — the extractor must
// read whichever field the kind declares.
func TestAuxFilesFromStore_EntriesField(t *testing.T) {
	s := store.NewMemoryStore(1)
	require.NoError(t, s.Add(auxUnstructured("mf-host", "maps/host.map", "entries", "example.com be_x\n", false), []string{"mf-host"}))

	got, err := publishedAuxFilesFromStore(s, publishedKind(t, haproxyMapFileGVR.String()))
	require.NoError(t, err)
	assert.Equal(t, "example.com be_x\n", got["mf-host"].content)
}

func TestPublishedSecretStoreReadsMetadataOnly(t *testing.T) {
	s := store.NewMemoryStore(1)
	secret := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "v1",
		"kind":       "Secret",
		"metadata": map[string]any{
			"name":            "certificate",
			"resourceVersion": "42",
			"annotations": map[string]any{
				"haproxy-haptic.org/auxiliary-set-id": "sha256:set-a",
				"haproxy-haptic.org/checksum":         "checksum-a",
			},
		},
		"data": map[string]any{"tls.key": []any{"must", "not", "be", "read"}},
	}}
	require.NoError(t, s.Add(secret, []string{"certificate"}))

	got, err := publishedAuxFilesFromStore(s, publishedKind(t, secretGVR.String()))
	require.NoError(t, err)
	assert.Equal(t, publishedAuxFile{
		setID:           "sha256:set-a",
		checksum:        "checksum-a",
		resourceVersion: "42",
	}, got["certificate"])
}

func TestPublishedAuxFilesAdvancesOnlyAtCommittedReferenceBoundary(t *testing.T) {
	const (
		setA = "sha256:set-a"
		setB = "sha256:set-b"
	)
	mapGVR := haproxyMapFileGVR.String()
	generalGVR := haproxyGeneralFileGVR.String()
	secretResourceGVR := secretGVR.String()
	published := newPublishedAuxFiles("haptic")
	published.setForGVR(mapGVR, map[string]publishedAuxFile{
		"map-a": {path: "maps/routes.map", content: "map-a", setID: setA},
	})
	published.setForGVR(generalGVR, map[string]publishedAuxFile{
		"general-a": {path: "general/error.http", content: "general-a", setID: setA},
	})
	published.setForGVR(secretResourceGVR, map[string]publishedAuxFile{
		"secret-a": {setID: setA},
	})
	published.setCommit(&publishedAuxCommit{setID: setA, refs: map[string][]publishedAuxRef{
		mapGVR:            {{name: "map-a", namespace: "haptic"}},
		generalGVR:        {{name: "general-a", namespace: "haptic"}},
		secretResourceGVR: {{name: "secret-a", namespace: "haptic"}},
	}})
	require.NoError(t, published.readinessError())
	assert.Equal(t, map[string]string{"routes.map": "map-a", "error.http": "general-a"}, publishedSnapshot(t, published))

	published.setForGVR(mapGVR, map[string]publishedAuxFile{
		"map-a": {path: "maps/routes.map", content: "map-a", setID: setA},
		"map-b": {path: "maps/routes.map", content: "map-b", setID: setB},
	})
	assert.Equal(t, "map-a", publishedSnapshot(t, published)["routes.map"], "uncommitted child must stay invisible")

	published.setCommit(&publishedAuxCommit{setID: setB, refs: map[string][]publishedAuxRef{
		mapGVR:            {{name: "map-b", namespace: "haptic"}},
		generalGVR:        {{name: "general-b", namespace: "haptic"}},
		secretResourceGVR: {{name: "secret-b", namespace: "haptic"}},
	}})
	assert.Equal(t, map[string]string{"routes.map": "map-a", "error.http": "general-a"}, publishedSnapshot(t, published),
		"a parent event arriving before every child watcher must retain the prior complete snapshot")

	published.setForGVR(generalGVR, map[string]publishedAuxFile{
		"general-a": {path: "general/error.http", content: "general-a", setID: setA},
		"general-b": {path: "general/error.http", content: "general-b", setID: setB},
	})
	assert.Equal(t, map[string]string{"routes.map": "map-a", "error.http": "general-a"}, publishedSnapshot(t, published),
		"missing committed Secret metadata must retain the prior snapshot")
	published.setForGVR(secretResourceGVR, map[string]publishedAuxFile{
		"secret-a": {setID: setA},
		"secret-b": {setID: "sha256:wrong"},
	})
	assert.Equal(t, map[string]string{"routes.map": "map-a", "error.http": "general-a"}, publishedSnapshot(t, published),
		"a Secret from another set must retain the prior snapshot")
	published.setForGVR(secretResourceGVR, map[string]publishedAuxFile{
		"secret-a": {setID: setA},
		"secret-b": {setID: setB},
	})
	assert.Equal(t, map[string]string{"routes.map": "map-b", "error.http": "general-b"}, publishedSnapshot(t, published))
}

func TestPublishedAuxFilesRejectsFailedPartialSet(t *testing.T) {
	mapGVR := haproxyMapFileGVR.String()
	published := newPublishedAuxFiles("haptic")
	published.setForGVR(mapGVR, map[string]publishedAuxFile{
		"map-a": {path: "maps/routes.map", content: "committed", setID: "sha256:set-a"},
	})
	published.setCommit(&publishedAuxCommit{setID: "sha256:set-a", refs: map[string][]publishedAuxRef{
		mapGVR: {{name: "map-a", namespace: "haptic"}},
	}})

	published.setForGVR(mapGVR, map[string]publishedAuxFile{
		"map-a": {path: "maps/routes.map", content: "committed", setID: "sha256:set-a"},
		"map-b": {path: "maps/routes.map", content: "failed-partial", setID: "sha256:set-b"},
	})
	assert.Equal(t, map[string]string{"routes.map": "committed"}, publishedSnapshot(t, published))

	published.setForGVR(mapGVR, map[string]publishedAuxFile{
		"map-a": {path: "maps/routes.map", content: "committed", setID: "sha256:set-a"},
	})
	published.setCommit(&publishedAuxCommit{setID: "sha256:set-b", refs: map[string][]publishedAuxRef{
		mapGVR: {{name: "map-b", namespace: "haptic"}},
	}})
	published.setForGVR(mapGVR, map[string]publishedAuxFile{
		"map-a": {path: "maps/routes.map", content: "committed", setID: "sha256:set-a"},
		"map-b": {path: "maps/routes.map", content: "wrong-set", setID: "sha256:set-c"},
	})
	assert.Equal(t, map[string]string{"routes.map": "committed"}, publishedSnapshot(t, published))
}

func TestPublishedAuxFilesFailsColdStartOnIncompleteCommit(t *testing.T) {
	mapGVR := haproxyMapFileGVR.String()
	published := newPublishedAuxFiles("haptic")
	published.setForGVR(mapGVR, map[string]publishedAuxFile{})
	published.setCommit(&publishedAuxCommit{setID: "sha256:set-a", refs: map[string][]publishedAuxRef{
		mapGVR: {{name: "missing", namespace: "haptic"}},
	}})

	require.EqualError(t, published.readinessError(), "committed HAProxyMapFile haptic/missing is unavailable")
	assert.Empty(t, publishedSnapshot(t, published))
}

func TestPublishedAuxFilesRejectsCrossNamespaceSecretReference(t *testing.T) {
	secretResourceGVR := secretGVR.String()
	published := newPublishedAuxFiles("haptic")
	published.setForGVR(secretResourceGVR, map[string]publishedAuxFile{
		"certificate": {setID: "sha256:set-a"},
	})
	published.setCommit(&publishedAuxCommit{setID: "sha256:set-a", refs: map[string][]publishedAuxRef{
		secretResourceGVR: {{name: "certificate", namespace: "other"}},
	}})

	require.EqualError(t, published.readinessError(), "committed Secret other/certificate is outside namespace haptic")
}

func TestPublishedAuxFilesRejectsLegacyMutationUntilSetIDAppears(t *testing.T) {
	mapGVR := haproxyMapFileGVR.String()
	published := newPublishedAuxFiles("haptic")
	published.setForGVR(mapGVR, map[string]publishedAuxFile{
		"map": {path: "maps/routes.map", content: "legacy"},
	})
	published.setCommit(&publishedAuxCommit{refs: map[string][]publishedAuxRef{
		mapGVR: {{name: "map", namespace: "haptic"}},
	}})
	assert.Equal(t, "legacy", publishedSnapshot(t, published)["routes.map"])

	published.setForGVR(mapGVR, map[string]publishedAuxFile{
		"map":        {path: "maps/routes.map", content: "legacy"},
		"modern-map": {path: "maps/routes.map", content: "modern", setID: "sha256:set-b"},
	})
	assert.Equal(t, "legacy", publishedSnapshot(t, published)["routes.map"],
		"an immutable modern child is not authoritative before its parent commit")

	published.setForGVR(mapGVR, map[string]publishedAuxFile{
		"map":        {path: "maps/routes.map", content: "legacy-updated"},
		"modern-map": {path: "maps/routes.map", content: "modern", setID: "sha256:set-b"},
	})
	_, err := published.get()
	require.ErrorContains(t, err, "legacy auxiliary publication changed without a set ID")

	published.setCommit(&publishedAuxCommit{setID: "sha256:set-b", refs: map[string][]publishedAuxRef{
		mapGVR: {{name: "modern-map", namespace: "haptic"}},
	}})
	assert.Equal(t, "modern", publishedSnapshot(t, published)["routes.map"])
}

func TestPublishedAuxFilesLeaderTermLiftsLegacyLatch(t *testing.T) {
	mapGVR := haproxyMapFileGVR.String()
	legacyCommit := &publishedAuxCommit{refs: map[string][]publishedAuxRef{
		mapGVR: {{name: "map", namespace: "haptic"}},
	}}
	published := newPublishedAuxFiles("haptic")
	published.setForGVR(mapGVR, map[string]publishedAuxFile{
		"map": {path: "maps/routes.map", content: "legacy-1"},
	})
	published.setCommit(legacyCommit)
	assert.Equal(t, "legacy-1", publishedSnapshot(t, published)["routes.map"])

	// A standby latches when an old-version leader mutates the legacy set.
	published.setForGVR(mapGVR, map[string]publishedAuxFile{
		"map": {path: "maps/routes.map", content: "legacy-2"},
	})
	_, err := published.get()
	require.ErrorContains(t, err, "legacy auxiliary publication changed without a set ID")

	// Becoming leader accepts the visible legacy snapshot, exactly as a
	// restarted process would, instead of failing every render until a restart.
	published.beginLeaderTerm()
	assert.Equal(t, "legacy-2", publishedSnapshot(t, published)["routes.map"])

	// A late legacy write landing during the term must not re-latch the leader.
	published.setForGVR(mapGVR, map[string]publishedAuxFile{
		"map": {path: "maps/routes.map", content: "legacy-3"},
	})
	assert.Equal(t, "legacy-3", publishedSnapshot(t, published)["routes.map"])
	published.setError(errors.New("transient list failure"))
	assert.Equal(t, "legacy-3", publishedSnapshot(t, published)["routes.map"])

	// Back to standby: legacy mutations latch again until a set ID appears.
	published.endLeaderTerm()
	published.setForGVR(mapGVR, map[string]publishedAuxFile{
		"map": {path: "maps/routes.map", content: "legacy-4"},
	})
	_, err = published.get()
	require.ErrorContains(t, err, "legacy auxiliary publication changed without a set ID")
	published.setForGVR(mapGVR, map[string]publishedAuxFile{
		"map":        {path: "maps/routes.map", content: "legacy-4"},
		"modern-map": {path: "maps/routes.map", content: "modern", setID: "sha256:set-b"},
	})
	published.setCommit(&publishedAuxCommit{setID: "sha256:set-b", refs: map[string][]publishedAuxRef{
		mapGVR: {{name: "modern-map", namespace: "haptic"}},
	}})
	assert.Equal(t, "modern", publishedSnapshot(t, published)["routes.map"])
}

func TestPublishedAuxFilesLeaderTermKeepsModernDowngradeLatch(t *testing.T) {
	mapGVR := haproxyMapFileGVR.String()
	published := newPublishedAuxFiles("haptic")
	published.setForGVR(mapGVR, map[string]publishedAuxFile{
		"map-a": {path: "maps/routes.map", content: "modern-a", setID: "sha256:set-a"},
	})
	published.setCommit(&publishedAuxCommit{setID: "sha256:set-a", refs: map[string][]publishedAuxRef{
		mapGVR: {{name: "map-a", namespace: "haptic"}},
	}})
	assert.Equal(t, "modern-a", publishedSnapshot(t, published)["routes.map"])

	published.setCommit(&publishedAuxCommit{refs: map[string][]publishedAuxRef{
		mapGVR: {{name: "map-a", namespace: "haptic"}},
	}})
	published.beginLeaderTerm()
	_, err := published.get()
	require.ErrorContains(t, err, "auxiliary publication lost its set ID",
		"a set that lost its ID means another writer exists; leadership must not accept it")
}

func TestPublishedAuxFilesRejectsLegacySecretMutationByResourceVersion(t *testing.T) {
	secretResourceGVR := secretGVR.String()
	secretKind := publishedKind(t, secretResourceGVR)
	parse := func(resourceVersion, certificate string) publishedAuxFile {
		t.Helper()
		_, file, found, err := publishedAuxFileFromObject(map[string]any{
			"metadata": map[string]any{
				"name":            "certificate",
				"resourceVersion": resourceVersion,
				"annotations": map[string]any{
					"haproxy-haptic.org/checksum": "unchanged",
				},
			},
			"data": map[string]any{"certificate": certificate},
		}, secretKind)
		require.NoError(t, err)
		require.True(t, found)
		return file
	}
	published := newPublishedAuxFiles("haptic")
	published.setForGVR(secretResourceGVR, map[string]publishedAuxFile{
		"certificate": parse("1", "legacy-a"),
	})
	published.setCommit(&publishedAuxCommit{refs: map[string][]publishedAuxRef{
		secretResourceGVR: {{name: "certificate", namespace: "haptic"}},
	}})
	require.NoError(t, published.readinessError())

	published.setForGVR(secretResourceGVR, map[string]publishedAuxFile{
		"certificate": parse("2", "legacy-b"),
	})
	_, err := published.get()
	require.ErrorContains(t, err, "legacy auxiliary publication changed without a set ID")
}

func TestPublishedAuxFilesNeverDowngradesAfterModernCommit(t *testing.T) {
	mapGVR := haproxyMapFileGVR.String()
	published := newPublishedAuxFiles("haptic")
	published.setForGVR(mapGVR, map[string]publishedAuxFile{
		"map-a": {path: "maps/routes.map", content: "modern-a", setID: "sha256:set-a"},
	})
	published.setCommit(&publishedAuxCommit{setID: "sha256:set-a", refs: map[string][]publishedAuxRef{
		mapGVR: {{name: "map-a", namespace: "haptic"}},
	}})
	assert.Equal(t, "modern-a", publishedSnapshot(t, published)["routes.map"])

	published.setCommit(nil)
	_, err := published.get()
	require.ErrorContains(t, err, "auxiliary publication lost its set ID")
	published.setCommit(&publishedAuxCommit{refs: map[string][]publishedAuxRef{
		mapGVR: {{name: "map-a", namespace: "haptic"}},
	}})
	_, err = published.get()
	require.ErrorContains(t, err, "auxiliary publication lost its set ID")

	published.setForGVR(mapGVR, map[string]publishedAuxFile{
		"map-a": {path: "maps/routes.map", content: "modern-a", setID: "sha256:set-a"},
		"map-b": {path: "maps/routes.map", content: "modern-b", setID: "sha256:set-b"},
	})
	published.setCommit(&publishedAuxCommit{setID: "sha256:set-b", refs: map[string][]publishedAuxRef{
		mapGVR: {{name: "map-b", namespace: "haptic"}},
	}})
	assert.Equal(t, "modern-b", publishedSnapshot(t, published)["routes.map"])
}

func TestPublishedAuxFilesAllowsInitialMissingParent(t *testing.T) {
	published := newPublishedAuxFiles("haptic")
	published.setCommit(nil)

	require.NoError(t, published.readinessError())
	assert.Empty(t, publishedSnapshot(t, published))
}
