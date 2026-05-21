package cli

import (
	"github.com/spf13/cobra"
)

func newWalletsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "wallets",
		Short: "Manage dual-rail wallet groups",
	}
	cmd.AddCommand(
		walletsCreateCmd(),
		walletsListCmd(),
		walletsGetCmd(),
		walletsBalanceCmd(),
	)
	return cmd
}

func walletsCreateCmd() *cobra.Command {
	var ownerType, ownerRef, displayName, chain string
	c := &cobra.Command{
		Use:   "create",
		Short: "POST /v1/wallet-groups (idempotent on owner-ref)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			cl, err := newClient(cmd, true)
			if err != nil {
				return err
			}
			body, status, err := cl.do(cmd.Context(), "POST", "/v1/wallet-groups", map[string]any{
				"owner":           map[string]string{"type": ownerType, "ref": ownerRef},
				"display_name":    displayName,
				"crossmint_chain": chain,
			})
			if err != nil {
				return err
			}
			// 200 = idempotent reuse, 201 = new
			if status != 200 && status != 201 {
				return failResponse(status, body)
			}
			emit(cmd, body)
			return nil
		},
	}
	c.Flags().StringVar(&ownerType, "owner-type", "user", "user | agent | merchant | treasury")
	c.Flags().StringVar(&ownerRef, "owner-ref", "", "customer's external id (required)")
	c.Flags().StringVar(&displayName, "display-name", "", "human label")
	c.Flags().StringVar(&chain, "crossmint-chain", "base", "CrossMint chain (base|polygon|solana)")
	_ = c.MarkFlagRequired("owner-ref")
	return c
}

func walletsListCmd() *cobra.Command {
	var limit, offset int
	c := &cobra.Command{
		Use:   "list",
		Short: "GET /v1/wallet-groups",
		RunE: func(cmd *cobra.Command, _ []string) error {
			cl, err := newClient(cmd, true)
			if err != nil {
				return err
			}
			path := paginated("/v1/wallet-groups", limit, offset)
			body, status, err := cl.do(cmd.Context(), "GET", path, nil)
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
	c.Flags().IntVar(&limit, "limit", 50, "rows to return (max 200)")
	c.Flags().IntVar(&offset, "offset", 0, "rows to skip")
	return c
}

func walletsGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "get <wallet-group-id>",
		Short: "GET /v1/wallet-groups/{id}",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cl, err := newClient(cmd, true)
			if err != nil {
				return err
			}
			body, status, err := cl.do(cmd.Context(), "GET", "/v1/wallet-groups/"+args[0], nil)
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

func walletsBalanceCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "balance <wallet-group-id>",
		Short: "GET /v1/wallet-groups/{id}/balance",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cl, err := newClient(cmd, true)
			if err != nil {
				return err
			}
			body, status, err := cl.do(cmd.Context(), "GET",
				"/v1/wallet-groups/"+args[0]+"/balance", nil)
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
