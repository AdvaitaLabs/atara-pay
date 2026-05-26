package cli

import (
	"github.com/spf13/cobra"
)

func newSessionKeysCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "session-keys",
		Short: "Mint, list, and revoke session keys scoped to a wallet group",
	}
	cmd.AddCommand(
		sessionKeysCreateCmd(),
		sessionKeysListCmd(),
		sessionKeysRevokeCmd(),
		sessionKeysSubmitAuthorizeCmd(),
	)
	return cmd
}

// sessionKeysSubmitAuthorizeCmd activates a pending_authorize session key
// minted against a user-custody wallet. The `create` call returned both the
// session key id and an `unsigned_authorize.raw_unsigned_hex` blob; the
// caller signs that with the WALLET MASTER KEY (off-server) and passes the
// resulting RLP back here.
func sessionKeysSubmitAuthorizeCmd() *cobra.Command {
	var groupID, signedHex string
	c := &cobra.Command{
		Use:   "submit-authorize <session-key-id>",
		Short: "POST /v1/wallet-groups/{group}/session-keys/{id}/submit-authorize",
		Long: `Activate a pending_authorize session key by broadcasting the
wallet-owner-signed authorizeKey tx. Pair with "atara session-keys create"
against a user-custody wallet: create returns the unsigned authorize bytes;
sign them externally (hardware wallet, MetaMask, Privy...); pass the signed
RLP back here.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cl, err := newClient(cmd, true)
			if err != nil {
				return err
			}
			body, status, err := cl.do(cmd.Context(), "POST",
				"/v1/wallet-groups/"+groupID+"/session-keys/"+args[0]+"/submit-authorize",
				map[string]any{"signed_tx_hex": signedHex})
			if err != nil {
				return err
			}
			if status != 200 && status != 201 {
				return failResponse(status, body)
			}
			emit(cmd, body)
			return nil
		},
	}
	c.Flags().StringVar(&groupID, "group", "", "wallet group id (required)")
	c.Flags().StringVar(&signedHex, "signed-tx-hex", "", "0x-prefixed signed authorizeKey RLP (required)")
	_ = c.MarkFlagRequired("group")
	_ = c.MarkFlagRequired("signed-tx-hex")
	return c
}

func sessionKeysCreateCmd() *cobra.Command {
	var (
		groupID, scope, expiresAt   string
		perTxLimit, dailyLimit      string
		recipientAllowlist          []string
		onchain                     bool
	)
	c := &cobra.Command{
		Use:   "create",
		Short: "POST /v1/wallet-groups/{id}/session-keys",
		RunE: func(cmd *cobra.Command, _ []string) error {
			cl, err := newClient(cmd, true)
			if err != nil {
				return err
			}
			body := map[string]any{
				"scope":         scope,
				"expires_at":    expiresAt,
				"on_chain":      onchain,
			}
			limits := map[string]any{}
			if perTxLimit != "" {
				limits["per_tx"] = perTxLimit
			}
			if dailyLimit != "" {
				limits["daily"] = dailyLimit
			}
			if len(recipientAllowlist) > 0 {
				limits["recipient_allowlist"] = recipientAllowlist
			}
			if len(limits) > 0 {
				body["limits"] = limits
			}
			resp, status, err := cl.do(cmd.Context(), "POST",
				"/v1/wallet-groups/"+groupID+"/session-keys", body)
			if err != nil {
				return err
			}
			if status != 201 {
				return failResponse(status, resp)
			}
			emit(cmd, resp)
			return nil
		},
	}
	c.Flags().StringVar(&groupID, "group", "", "wallet group id (required)")
	c.Flags().StringVar(&scope, "scope", "spend", "session key scope")
	c.Flags().StringVar(&expiresAt, "expires-at", "", "RFC3339 expiry (required)")
	c.Flags().StringVar(&perTxLimit, "per-tx", "", "per-transaction cap (decimal string)")
	c.Flags().StringVar(&dailyLimit, "daily", "", "rolling-day cap (decimal string)")
	c.Flags().StringSliceVar(&recipientAllowlist, "to", nil, "allowed recipient (repeatable)")
	c.Flags().BoolVar(&onchain, "on-chain", false,
		"authorize on Tempo via authorizeKey instead of gateway-only enforcement")
	_ = c.MarkFlagRequired("group")
	_ = c.MarkFlagRequired("expires-at")
	return c
}

func sessionKeysListCmd() *cobra.Command {
	var groupID string
	var limit, offset int
	c := &cobra.Command{
		Use:   "list",
		Short: "GET /v1/wallet-groups/{id}/session-keys",
		RunE: func(cmd *cobra.Command, _ []string) error {
			cl, err := newClient(cmd, true)
			if err != nil {
				return err
			}
			path := paginated("/v1/wallet-groups/"+groupID+"/session-keys", limit, offset)
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
	c.Flags().StringVar(&groupID, "group", "", "wallet group id (required)")
	c.Flags().IntVar(&limit, "limit", 50, "rows to return")
	c.Flags().IntVar(&offset, "offset", 0, "rows to skip")
	_ = c.MarkFlagRequired("group")
	return c
}

func sessionKeysRevokeCmd() *cobra.Command {
	var groupID, reason string
	c := &cobra.Command{
		Use:   "revoke <session-key-id>",
		Short: "DELETE /v1/wallet-groups/{group}/session-keys/{id}",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cl, err := newClient(cmd, true)
			if err != nil {
				return err
			}
			body, status, err := cl.do(cmd.Context(), "DELETE",
				"/v1/wallet-groups/"+groupID+"/session-keys/"+args[0],
				map[string]any{"reason": reason})
			if err != nil {
				return err
			}
			if status != 200 && status != 204 {
				return failResponse(status, body)
			}
			emit(cmd, body)
			return nil
		},
	}
	c.Flags().StringVar(&groupID, "group", "", "wallet group id (required)")
	c.Flags().StringVar(&reason, "reason", "", "audit-trail note")
	_ = c.MarkFlagRequired("group")
	return c
}
