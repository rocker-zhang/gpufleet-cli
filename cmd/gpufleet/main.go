// Command gpufleet is the local CLI/TUI entrypoint. It is a read-only bypass
// viewer over the agent's local HTTP API; no control plane is required and no
// closed logic is present.
package main

import (
	"fmt"
	"os"

	"github.com/rocker-zhang/gpufleet-cli"
)

func main() {
	if err := cli.NewRootCmd().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "gpufleet:", err)
		os.Exit(1)
	}
}
