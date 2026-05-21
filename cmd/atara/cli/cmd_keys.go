package cli

import (
	"github.com/spf13/cobra"
)

func newKeysCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "keys",
		Short: "Manage API keys",
	}
	cmd.AddCommand(
		keysListCmd(),
		keysCreateCmd(),
		keysRevokeCmd(),
	)
	return cmd
}

func keysListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "GET /v1/api-keys",
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, err := newClient(cmd, true)
			if err != nil {
				return err
			}
			body, status, err := c.do(cmd.Context(), "GET", "/v1/api-keys", nil)
			if err != nil {
				return err
			}
			if status != 200 {
				return failResponse(status, body)
			}
			emit(cmd, body)
			return nil
		},
	}
}

func keysCreateCmd() *cobra.Command {
	var name, env string
	c := &cobra.Command{
		Use:   "create",
		Short: "Mint a new API key (returns the raw secret ONCE)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			cl, err := newClient(cmd, true)
			if err != nil {
				return err
			}
			body, status, err := cl.do(cmd.Context(), "POST", "/v1/api-keys", map[string]any{
				"name":        name,
				"environment": env,
			})
			if err != nil {
				return err
			}
			if status != 201 {
				return failResponse(status, body)
			}
			emit(cmd, body)
			return nil
		},
	}
	c.Flags().StringVar(&name, "name", "", "human label (required)")
	c.Flags().StringVar(&env, "env", "test", "test | live")
	_ = c.MarkFlagRequired("name")
	return c
}

func keysRevokeCmd() *cobra.Command {
	var reason string
	c := &cobra.Command{
		Use:   "revoke <id>",
		Short: "Revoke an API key (idempotent)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cl, err := newClient(cmd, true)
			if err != nil {
				return err
			}
			body, status, err := cl.do(cmd.Context(), "DELETE", "/v1/api-keys/"+args[0],
				map[string]any{"reason": reason})
			if err != nil {
				return err
			}
			if status != 200 {
				return failResponse(status, body)
			}
			emit(cmd, body)
			return nil
		},
	}
	c.Flags().StringVar(&reason, "reason", "", "audit-trail note (optional)")
	return c
}
