//go:build ignore

// Command gen_playground_schema_bundle serializes a kubeconform-style schema
// directory (e.g. tests/schemas/) into the JSON bundle the browser playground's
// wasm consumes for typed-resource access.
//
// The bundle is keyed by "<apiVersion>|<plural>" so the wasm resolves each
// watched resource to a GVK exactly (no singularization guessing), then builds
// an in-memory schemafetcher.MapFetcher from it. Generation runs on the host
// (which has a filesystem) via the same schemafetcher.DirFetcher the offline
// `controller validate --schema-dir` path uses, so the wasm needs no CRD-parsing
// code of its own.
//
// Usage:
//
//	go run scripts/gen_playground_schema_bundle.go tests/schemas > schemas.json
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"gitlab.com/haproxy-haptic/haptic/pkg/k8s/schemafetcher"
)

type entry struct {
	Group   string          `json:"group"`
	Version string          `json:"version"`
	Kind    string          `json:"kind"`
	Schema  json.RawMessage `json:"schema"`
}

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: go run scripts/gen_playground_schema_bundle.go <schema-dir>")
		os.Exit(2)
	}

	fetcher, err := schemafetcher.NewDirFetcher(os.Args[1])
	if err != nil {
		fmt.Fprintln(os.Stderr, "loading schema dir:", err)
		os.Exit(1)
	}

	bundle := map[string]entry{}
	for apiVersion, plurals := range fetcher.PluralsFor() {
		for plural, gvk := range plurals {
			sch, _, err := fetcher.Fetch(context.Background(), gvk)
			if err != nil {
				fmt.Fprintf(os.Stderr, "fetching %s/%s: %v\n", apiVersion, plural, err)
				os.Exit(1)
			}
			raw, err := json.Marshal(sch)
			if err != nil {
				fmt.Fprintf(os.Stderr, "marshalling %s/%s: %v\n", apiVersion, plural, err)
				os.Exit(1)
			}
			bundle[apiVersion+"|"+plural] = entry{Group: gvk.Group, Version: gvk.Version, Kind: gvk.Kind, Schema: raw}
		}
	}

	if err := json.NewEncoder(os.Stdout).Encode(bundle); err != nil {
		fmt.Fprintln(os.Stderr, "encoding bundle:", err)
		os.Exit(1)
	}
}
