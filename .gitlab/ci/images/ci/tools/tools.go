//go:build tools

// Package tools imports the CI image's tool binaries so `go mod tidy` keeps
// their requirements: nothing else in this module references them.
package tools

import (
	_ "github.com/boumenot/gocover-cobertura"
	_ "github.com/google/go-containerregistry/cmd/crane"
	_ "oras.land/oras/cmd/oras"
)
