//go:build tools
// +build tools

// Package tools tracks tool dependencies for the project.
// This ensures that `go mod tidy` doesn't remove tool dependencies.
package tools

import (
	_ "github.com/arch-go/arch-go/v2"
	_ "github.com/golangci/golangci-lint/cmd/golangci-lint"
	_ "github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen"
	_ "golang.org/x/vuln/cmd/govulncheck"
	_ "sigs.k8s.io/controller-tools/cmd/controller-gen"
)
