// Copyright 2025 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

//go:build playground

package parser_test

import (
	"fmt"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"unsafe"

	"github.com/haproxytech/client-native/v6/models"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/parser"
)

// retainedBytes reports the heap still held after fn returns, with the value fn
// produced kept alive across the measurement.
func retainedBytes(t *testing.T, fn func() any) uint64 {
	t.Helper()

	runtime.GC()
	runtime.GC()
	var before runtime.MemStats
	runtime.ReadMemStats(&before)

	v := fn()

	runtime.GC()
	runtime.GC()
	var after runtime.MemStats
	runtime.ReadMemStats(&after)
	runtime.KeepAlive(v)

	if after.HeapAlloc < before.HeapAlloc {
		return 0
	}
	return after.HeapAlloc - before.HeapAlloc
}

// configWithServers renders a config shaped like the chart's output: reserved
// server slots per backend, which is what dominates a real rendered config.
func configWithServers(backends, serversPerBackend int) string {
	var b strings.Builder
	b.WriteString("global\n  daemon\n\ndefaults\n  mode http\n\n")
	for i := range backends {
		fmt.Fprintf(&b, "backend be_%d\n  default-server check\n", i)
		for j := 1; j <= serversPerBackend; j++ {
			fmt.Fprintf(&b, "  server SRV_%d 10.%d.%d.%d:8080 enabled\n",
				j, i/256, i%256, j%256)
		}
		b.WriteByte('\n')
	}
	return b.String()
}

// The parsed configuration is held for the lifetime of a deployed config, so
// its cost per server is resident memory proportional to the fleet. This pins
// where that cost comes from: the size of the model, not the data in it.
func TestParsedConfigRetentionPerServer(t *testing.T) {
	const (
		backends          = 100
		serversPerBackend = 20
		totalServers      = backends * serversPerBackend
	)

	cfg := configWithServers(backends, serversPerBackend)

	full := retainedBytes(t, func() any {
		p, err := parser.New()
		require.NoError(t, err)
		parsed, err := p.ParseFromString(cfg)
		require.NoError(t, err)
		return parsed
	})

	var srv models.Server
	structSize := unsafe.Sizeof(srv)
	paramsSize := unsafe.Sizeof(srv.ServerParams)

	// Count how much of the model is declared but unset on a parsed server: the
	// fields cost their width whether or not the config mentions them.
	rv := reflect.ValueOf(srv.ServerParams)
	total, zero := rv.NumField(), 0
	for i := range rv.NumField() {
		if rv.Field(i).IsZero() {
			zero++
		}
	}

	t.Logf("config text            : %d bytes, %d servers", len(cfg), totalServers)
	t.Logf("retained parsed config : %.2f MiB (%.0f B/server)",
		float64(full)/(1<<20), float64(full)/totalServers)
	t.Logf("sizeof(models.Server)  : %d B  (ServerParams alone: %d B)", structSize, paramsSize)
	t.Logf("ServerParams fields    : %d, of which zero on a bare server: %d", total, zero)

	require.Positive(t, full)
}
