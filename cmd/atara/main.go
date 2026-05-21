// Command atara is the developer + agent-runtime CLI for Atara-Pay.
//
// It wraps the same REST API that lives at api/v1/openapi.yaml. Customers
// who already use the REST endpoints directly never need to install this;
// the value is in agent scenarios — Claude / Cursor / Cline runtimes work
// best when they can shell out to a stable `atara …` command with a
// predictable JSON output.
//
// Distribution model (parallels Kite AI's `kpass`):
//
//	curl -fsSL https://atara.xyz/install.sh | bash
//
// installs the binary to ~/.atara/bin and prints next-steps. The companion
// skills/ directory (consumed by Claude Code, Cursor, and Cline via their
// own skill-discovery formats) teaches the agent WHEN to invoke each
// command, parse outputs, and handle errors.
package main

import (
	"fmt"
	"os"

	"github.com/atara-xyz/atara-pay/cmd/atara/cli"
)

func main() {
	if err := cli.Root().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
