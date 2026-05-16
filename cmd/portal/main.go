// Command portal is the composition root for the Portal binary.
// All real wiring lives in wire.go; this file only parses flags and dispatches subcommands.
package main

import (
	"fmt"
	"os"
)

// version is set via -ldflags at build time.
var version = "dev"

func main() {
	if err := newRootCmd().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "portal:", err)
		os.Exit(1)
	}
}
