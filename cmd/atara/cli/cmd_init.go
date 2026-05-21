package cli

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
)

func newInitCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Configure base URL + API key (writes ~/.atara/config.json)",
		Long: `Interactive bootstrap. Run once per new machine / new tenant.

You can also bypass this by exporting:
  ATARA_BASE_URL=https://api.atara.xyz
  ATARA_API_KEY=sk_test_…
or passing --base-url / --api-key on every command.`,
		RunE: runInit,
	}
	return cmd
}

func runInit(cmd *cobra.Command, _ []string) error {
	existing, _ := loadConfig()

	reader := bufio.NewReader(os.Stdin)

	fmt.Printf("Atara-Pay base URL [%s]: ", defaulted(existing.BaseURL, "http://localhost:8080"))
	base, _ := reader.ReadString('\n')
	base = strings.TrimSpace(base)
	if base == "" {
		base = defaulted(existing.BaseURL, "http://localhost:8080")
	}

	fmt.Print("API key (sk_test_… or sk_live_…): ")
	key, _ := reader.ReadString('\n')
	key = strings.TrimSpace(key)
	if key == "" && existing.APIKey != "" {
		fmt.Println("(keeping existing key)")
		key = existing.APIKey
	}
	if key == "" {
		return fmt.Errorf("api key required — paste it from /signup response or the dashboard")
	}
	if !strings.HasPrefix(key, "sk_test_") && !strings.HasPrefix(key, "sk_live_") {
		return fmt.Errorf("api key must start with sk_test_ or sk_live_")
	}

	c := Config{BaseURL: base, APIKey: key}
	if err := saveConfig(c); err != nil {
		return err
	}
	p, _ := configPath()
	fmt.Printf("✓ wrote %s\n", p)
	fmt.Printf("✓ base URL: %s\n", base)
	fmt.Printf("✓ key:      %s…%s\n", key[:12], key[len(key)-4:])
	fmt.Println("Next: `atara health` to verify connectivity")
	return nil
}

func defaulted(v, fallback string) string {
	if v == "" {
		return fallback
	}
	return v
}
