// Copyright 2026 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package configpublisher

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/rest"

	haproxyv1alpha1 "gitlab.com/haproxy-haptic/haptic/pkg/apis/haproxytemplate/v1alpha1"
	"gitlab.com/haproxy-haptic/haptic/pkg/generated/clientset/versioned"
)

func TestAuxiliaryPodCleanupUsesBoundedConcurrentRequests(t *testing.T) {
	const files = 24
	entered := make(chan struct{}, files)
	release := make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	var active, maximum, updates atomic.Int32
	probe := &podCleanupRequestProbe{entered: entered, release: release, active: &active, maximum: &maximum, updates: &updates}
	server := httptest.NewServer(probe)
	t.Cleanup(server.Close)
	t.Cleanup(unblock)
	client, err := versioned.NewForConfig(&rest.Config{Host: server.URL, QPS: -1})
	require.NoError(t, err)
	publisher := &Publisher{crdClient: client}
	refs := &haproxyv1alpha1.AuxiliaryFileReferences{}
	for i := range files {
		refs.MapFiles = append(refs.MapFiles, haproxyv1alpha1.ResourceReference{Namespace: "default", Name: fmt.Sprintf("map-%d", i)})
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- publisher.cleanupAuxiliaryFilePodReferences(ctx, refs, &PodCleanupRequest{PodName: "departed"})
	}()
	for range auxiliaryCleanupConcurrency {
		select {
		case <-entered:
		case <-ctx.Done():
			t.Fatal("cleanup serialized the requests instead of starting concurrent work")
		}
	}
	unblock()
	require.NoError(t, <-done)
	assert.Equal(t, int32(files), updates.Load())
	assert.Equal(t, int32(auxiliaryCleanupConcurrency), maximum.Load())
}

type podCleanupRequestProbe struct {
	entered                  chan<- struct{}
	release                  <-chan struct{}
	active, maximum, updates *atomic.Int32
}

func (p *podCleanupRequestProbe) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	n := p.active.Add(1)
	defer p.active.Add(-1)
	for old := p.maximum.Load(); n > old; old = p.maximum.Load() {
		if p.maximum.CompareAndSwap(old, n) {
			break
		}
	}
	obj := haproxyv1alpha1.HAProxyMapFile{
		TypeMeta:   metav1.TypeMeta{APIVersion: "haproxy-haptic.org/v1alpha1", Kind: "HAProxyMapFile"},
		ObjectMeta: metav1.ObjectMeta{Name: path.Base(r.URL.Path), Namespace: "default", ResourceVersion: "1"},
	}
	switch r.Method {
	case http.MethodGet:
		p.entered <- struct{}{}
		select {
		case <-p.release:
		case <-r.Context().Done():
			return
		}
		obj.Status.DeployedToPods = []haproxyv1alpha1.PodDeploymentStatus{{PodName: "departed"}, {PodName: "running"}}
	case http.MethodPut:
		if err := json.NewDecoder(r.Body).Decode(&obj); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if len(obj.Status.DeployedToPods) != 1 || obj.Status.DeployedToPods[0].PodName != "running" {
			http.Error(w, "incorrect surviving pod", http.StatusBadRequest)
			return
		}
		p.updates.Add(1)
	default:
		http.Error(w, "unexpected request", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(&obj); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}
