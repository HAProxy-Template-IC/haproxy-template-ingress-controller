// Copyright 2025 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package dataplane

import (
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/comparator"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/parser/parserconfig"
)

// parseAndCompareConfigs has THREE error-handling branches the
// existing tests don't cover. Each is load-bearing because it is
// the operator's only window into WHY a sync failed:
//
//  1. Parsing the CURRENT config fails → wrap as
//     NewParseError("current", snippet, err). Operators read the
//     wrapped error to know whether the failure is on the dataplane
//     side (current = dataplane API state, possibly corrupted) or
//     on the controller's render side (desired). A regression that
//     dropped the configType label or the snippet would force
//     operators to rebuild that context manually.
//
//  2. Parsing the DESIRED config fails → wrap as
//     NewParseError("desired", snippet, err). Same contract,
//     opposite source. Critical because the OperationStage label
//     ("parse-current" vs "parse-desired") routes alerts in the
//     metrics and commentator.
//
//  3. Snippet truncation: configs longer than 200 chars MUST be
//     truncated (operator logs would otherwise get megabytes of
//     HAProxy config dumped on every parse failure).
//
// All three branches use the existing mockConfigParser helper from
// orchestrator_version_cache_test.go.

// makeOrchForParseTest builds the minimal orchestrator the tested
// branch reaches: parser + comparator + logger only. None of the
// other dependencies are touched on the parse-error paths.
func makeOrchForParseTest(p *mockConfigParser) *orchestrator {
	return &orchestrator{
		parser:     p,
		comparator: comparator.New(),
		logger:     slog.Default(),
	}
}

func TestParseAndCompareConfigs_CurrentParseErrorWraps(t *testing.T) {
	const sentinelMsg = "current parse boom"
	parseErr := errors.New(sentinelMsg)
	parser := &mockConfigParser{
		parseFunc: func(_ string) (*parserconfig.StructuredConfig, error) {
			return nil, parseErr
		},
	}

	orch := makeOrchForParseTest(parser)
	currentCfg := "global\n  daemon\n"

	diff, err := orch.parseAndCompareConfigs(currentCfg, "irrelevant", nil, nil)

	require.Nil(t, diff)
	require.Error(t, err)

	// Operator-facing structure: SyncError wrapping ParseError.
	var syncErr *SyncError
	require.ErrorAs(t, err, &syncErr,
		"current-parse failure MUST be wrapped as *SyncError so the "+
			"orchestrator can route it through the standard "+
			"sync-failure handling — the sync flow keys on this type")
	assert.Equal(t, "parse-current", syncErr.Stage,
		"Stage MUST be parse-current — alerts/metrics route on this label")

	var parseErrTyped *ParseError
	require.ErrorAs(t, err, &parseErrTyped,
		"the cause MUST be a *ParseError so operators can read the snippet "+
			"and configType fields without re-parsing the error string")
	assert.Equal(t, "current", parseErrTyped.ConfigType,
		"ConfigType MUST be current to distinguish dataplane-side vs "+
			"controller-side parse failures (different on-call paths)")
	assert.Equal(t, currentCfg, parseErrTyped.ConfigSnippet,
		"the original config MUST be preserved as ConfigSnippet (under 200 "+
			"chars here, so no truncation) so operators can see the failing input")
	assert.True(t, errors.Is(parseErrTyped, parseErr),
		"the underlying parser error MUST be Unwrap-reachable so callers "+
			"can use errors.Is/As on the original failure")
}

func TestParseAndCompareConfigs_DesiredParseErrorWraps(t *testing.T) {
	const sentinelMsg = "desired parse boom"
	parseErr := errors.New(sentinelMsg)
	// Make the parser fail ONLY for the desired path. Since we pass
	// preParsedCurrent as non-nil, the only ParseFromString call
	// will be for the desired config.
	parser := &mockConfigParser{
		parseFunc: func(_ string) (*parserconfig.StructuredConfig, error) {
			return nil, parseErr
		},
	}

	orch := makeOrchForParseTest(parser)
	preParsedCurrent := &parserconfig.StructuredConfig{}
	desiredCfg := "frontend test\n  bind *:80\n"

	diff, err := orch.parseAndCompareConfigs("unused", desiredCfg, nil, preParsedCurrent)

	require.Nil(t, diff)
	require.Error(t, err)
	require.Equal(t, int32(1), parser.parseCalled.Load(),
		"parser MUST be called exactly once (for desired) — preParsedCurrent "+
			"short-circuits the current-path; a regression that always reparsed "+
			"would defeat the snapshot-cache optimization")

	var syncErr *SyncError
	require.ErrorAs(t, err, &syncErr)
	assert.Equal(t, "parse-desired", syncErr.Stage,
		"Stage MUST be parse-desired — distinct from parse-current so "+
			"operators can immediately tell which side failed")

	var parseErrTyped *ParseError
	require.ErrorAs(t, err, &parseErrTyped)
	assert.Equal(t, "desired", parseErrTyped.ConfigType)
	assert.Equal(t, desiredCfg, parseErrTyped.ConfigSnippet)
}

func TestParseAndCompareConfigs_LongCurrentConfigSnippetIsTruncated(t *testing.T) {
	// Config larger than 200 chars must be truncated. Without this,
	// operator logs would balloon every parse failure with the full
	// config (potentially MB) — a real performance / log-cost
	// concern given that parse failures can recur every reconcile
	// cycle.
	parser := &mockConfigParser{
		parseFunc: func(_ string) (*parserconfig.StructuredConfig, error) {
			return nil, errors.New("boom")
		},
	}
	orch := makeOrchForParseTest(parser)

	const longChunk = "X"
	longConfig := strings.Repeat(longChunk, 500) // 500 chars

	_, err := orch.parseAndCompareConfigs(longConfig, "unused", nil, nil)
	require.Error(t, err)

	var parseErrTyped *ParseError
	require.ErrorAs(t, err, &parseErrTyped)
	assert.Len(t, parseErrTyped.ConfigSnippet, 200,
		"ConfigSnippet MUST be truncated to 200 chars — without this, "+
			"every parse failure dumps the full config into the operator's "+
			"log stream (potentially MB per reconcile cycle)")
	assert.Equal(t, strings.Repeat(longChunk, 200), parseErrTyped.ConfigSnippet,
		"the truncation MUST keep the FIRST 200 chars (most-significant "+
			"portion of HAProxy configs is the global/defaults header) — a "+
			"regression that took a tail slice would lose the section name "+
			"that operators rely on to locate the failure")
}
