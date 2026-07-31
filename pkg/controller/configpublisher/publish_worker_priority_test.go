// Copyright 2025 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package configpublisher

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/apis/haproxytemplate/v1alpha1"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/throttle"
)

// syncBuffer is an io.Writer safe for the worker goroutine to write while the
// test reads.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.String()
}

// publishWorker drains deploy-driven work ahead of validation-driven work.
//
// The two used to be equal cases of one select, and a select picks uniformly at
// random among ready cases — so with validation publishes arriving
// continuously, a deployed item could sit unread. Measured on a real run: 5.2s,
// with three validation publishes going out ahead of it.
//
// That window is not cosmetic. `status.deployedToPods[].checksum` is written by
// an independent path, so for as long as the deployed item waits, the CR
// advertises a pod running a config whose content `spec` has not published — a
// checksum no reader can resolve. That is what strands `waitForControllerDeployed`
// in the e2e suite, and what a GitOps reader would see too.
func TestPublishWorker_DrainsDeployedWorkFirst(t *testing.T) {
	logBuf := &syncBuffer{}

	// Every item carries the same content checksum as lastPublishedChecksum, so
	// skipIfAlreadyPublished short-circuits each one *after* processPublishWork
	// logs "Processing publish work" — the worker loop is exercised end to end
	// while the (nil) publisher is never reached. The log order is therefore the
	// order the worker took items off the channels.
	const published = "same-content"
	c := &Component{
		logger: slog.New(slog.NewTextHandler(logBuf, &slog.HandlerOptions{
			Level: slog.LevelDebug,
		})),
		renderedConfigs:       make(map[string]*renderedConfigEntry),
		publishWork:           make(chan *publishWorkItem, 4),
		deployedPublishWork:   make(chan *publishWorkItem, 4),
		lastPublishedChecksum: published,
		publishThrottle:       throttle.New(time.Hour),
	}

	item := func(id string, deployDriven bool) *publishWorkItem {
		return &publishWorkItem{
			correlationID:  id,
			templateConfig: &v1alpha1.HAProxyTemplateConfig{},
			entry:          &renderedConfigEntry{contentChecksum: published},
			deployDriven:   deployDriven,
		}
	}

	// Both channels ready before the worker starts. Under a fair select the
	// validation items would win roughly half the time.
	c.publishWork <- item("validation-1", false)
	c.publishWork <- item("validation-2", false)
	c.deployedPublishWork <- item("deployed:abc", true)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go c.publishWorker(ctx)

	require.Eventually(t, func() bool {
		return len(processedOrder(logBuf.String())) >= 3
	}, 5*time.Second, 5*time.Millisecond, "publishWorker did not process all queued work")

	order := processedOrder(logBuf.String())
	assert.Equal(t, "deployed:abc", order[0],
		"the deployed checksum must be published before validation work, since "+
			"status.deployedToPods already advertises it")
}

// processedOrder extracts the correlation IDs of "Processing publish work"
// records, in the order the worker emitted them.
func processedOrder(logText string) []string {
	var ids []string
	for line := range strings.SplitSeq(logText, "\n") {
		if !strings.Contains(line, `msg="Processing publish work"`) {
			continue
		}
		_, rest, found := strings.Cut(line, "correlation_id=")
		if !found {
			continue
		}
		id, _, _ := strings.Cut(rest, " ")
		ids = append(ids, strings.Trim(id, `"`))
	}
	return ids
}
