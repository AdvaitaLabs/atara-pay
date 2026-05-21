// Package cli holds the cobra commands that compose the `atara` CLI.
//
// Layout:
//
//	root.go       top-level cobra.Command + global flags
//	config.go     persistent settings (base URL + API key)
//	client.go     thin HTTP client around the REST API
//	cmd_init.go   `atara init`            interactive bootstrap
//	cmd_health.go `atara health`          connectivity check
//	cmd_keys.go   `atara keys …`          API-key CRUD
//	cmd_wallets.go
//	cmd_tx.go
//	cmd_session.go
//	cmd_webhooks.go
//	cmd_limits.go
//
// Every subcommand returns JSON to stdout — agent runtimes parse it.
// Human-friendly output is opt-in via --pretty.
package cli

import (
	"github.com/spf13/cobra"
)

// Version is the semantic version of the CLI. Overridden at link time via
//
//	go build -ldflags "-X github.com/atara-xyz/atara-pay/cmd/atara/cli.Version=v0.4.0"
//
// Release tarballs set this; `go build` / `make build-cli` leave it as "dev"
// so a local build is easy to spot. install.sh and `atara --version` both
// read this string.
var Version = "dev"

// Root builds the top-level command tree. Called once from main.
func Root() *cobra.Command {
	root := &cobra.Command{
		Use:           "atara",
		Short:         "Atara-Pay CLI — manage wallets, transfers, session keys, webhooks",
		Version:       Version,
		SilenceUsage:  true, // errors don't blast usage banner; agents prefer terse failures
		SilenceErrors: true, // main prints the error itself once
		Long: `atara is the developer + agent-runtime CLI for Atara-Pay.

Every command outputs JSON by default so AI agents can parse it.
Run with --pretty for human-friendly output.

Examples:
  atara init                          first-time setup
  atara health                        connectivity check
  atara keys list
  atara wallets create --owner-type user --owner-ref alice-123
  atara tx send --from wg_... --to merchant:openai --amount 0.05 --asset USDC
`,
	}

	// Global flags. cobra spreads these to children automatically.
	root.PersistentFlags().Bool("pretty", false,
		"format JSON for humans (indent + colorize if tty)")
	root.PersistentFlags().String("base-url", "",
		"Atara-Pay server URL (overrides config + ATARA_BASE_URL env)")
	root.PersistentFlags().String("api-key", "",
		"API key (overrides config + ATARA_API_KEY env)")

	root.AddCommand(
		newInitCmd(),
		newHealthCmd(),
		newKeysCmd(),
		newWalletsCmd(),
		newTxCmd(),
		newSessionKeysCmd(),
		newWebhooksCmd(),
		newLimitsCmd(),
	)
	return root
}
