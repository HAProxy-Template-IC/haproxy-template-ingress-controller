// Copyright 2026 Philipp Hossner
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"errors"
	"flag"
	"os"
	"time"

	"gitlab.com/haproxy-haptic/haptic/tests/kindutil"
	"gitlab.com/haproxy-haptic/haptic/tests/process"
)

func runBlackhole(ctx context.Context, args []string) error {
	flags := flag.NewFlagSet("blackhole-backends", flag.ContinueOnError)
	name := flags.String("cluster", "", "existing Kind cluster")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *name == "" || flags.NArg() != 0 {
		return errors.New("blackhole-backends requires --cluster <name>")
	}
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	return kindutil.BlackholeCluster(ctx, process.Executor{}, *name, kindutil.DockerEnvironment(os.Getenv))
}
