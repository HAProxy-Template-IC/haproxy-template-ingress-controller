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

package main

import (
	"fmt"
	"runtime"

	"github.com/spf13/cobra"
)

// Version information. Set via ldflags at build time.
var (
	// version is the semantic version of the controller.
	// Set via -ldflags "-X main.version=...".
	version = "dev"

	// commit is the git commit SHA.
	// Set via -ldflags "-X main.commit=...".
	commit = "unknown"

	// date is the build date.
	// Set via -ldflags "-X main.date=...".
	date = "unknown"
)

// versionCmd represents the version command.
var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print version information",
	Long:  `Print the version, commit SHA, and build date of the controller.`,
	Run: func(_ *cobra.Command, _ []string) {
		printVersion()
	},
}

func init() {
	rootCmd.AddCommand(versionCmd)
}

// printVersion outputs version information to stdout.
func printVersion() {
	fmt.Printf("haproxy-template-ingress-controller\n")
	fmt.Printf("  Version:    %s\n", version)
	fmt.Printf("  Commit:     %s\n", commit)
	fmt.Printf("  Built:      %s\n", date)
	fmt.Printf("  Go version: %s\n", runtime.Version())
	fmt.Printf("  OS/Arch:    %s/%s\n", runtime.GOOS, runtime.GOARCH)
}

// GetVersion returns the version string.
func GetVersion() string {
	return version
}
