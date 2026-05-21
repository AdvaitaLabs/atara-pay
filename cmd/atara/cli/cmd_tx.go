package cli

import (
	"github.com/spf13/cobra"
)

func newTxCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "tx",
		Short: "Send transfers + on-ramp orders against a wallet group",
	}
	cmd.AddCommand(txSendCmd(), txOnrampCmd())
	return cmd
}

func txSendCmd() *cobra.Command {
	var (
		from, to, amount, asset, rail, signerID, idempotencyKey, memo string
	)
	c := &cobra.Command{
		Use:   "send",
		Short: "POST /v1/wallet-groups/{from}/transactions",
		RunE: func(cmd *cobra.Command, _ []string) error {
			cl, err := newClient(cmd, true)
			if err != nil {
				return err
			}
			body := map[string]any{
				"to":     to,
				"amount": amount,
				"asset":  asset,
			}
			if rail != "" {
				body["rail"] = rail
			}
			if signerID != "" {
				body["session_key_id"] = signerID
			}
			if idempotencyKey != "" {
				body["idempotency_key"] = idempotencyKey
			}
			if memo != "" {
				body["memo"] = memo
			}
			resp, status, err := cl.do(cmd.Context(), "POST",
				"/v1/wallet-groups/"+from+"/transactions", body)
			if err != nil {
				return err
			}
			if status != 200 && status != 201 && status != 202 {
				return failResponse(status, resp)
			}
			emit(cmd, resp)
			return nil
		},
	}
	c.Flags().StringVar(&from, "from", "", "source wallet group id (required)")
	c.Flags().StringVar(&to, "to", "", "destination address or merchant alias (required)")
	c.Flags().StringVar(&amount, "amount", "", "decimal amount as string (required)")
	c.Flags().StringVar(&asset, "asset", "", "asset symbol e.g. USDC (required)")
	c.Flags().StringVar(&rail, "rail", "", "force a specific rail (crossmint|tempo); empty = server picks")
	c.Flags().StringVar(&signerID, "session-key-id", "", "session key id authorizing the spend")
	c.Flags().StringVar(&idempotencyKey, "idempotency-key", "", "client-supplied dedup key")
	c.Flags().StringVar(&memo, "memo", "", "free-form memo (optional)")
	_ = c.MarkFlagRequired("from")
	_ = c.MarkFlagRequired("to")
	_ = c.MarkFlagRequired("amount")
	_ = c.MarkFlagRequired("asset")
	return c
}

func txOnrampCmd() *cobra.Command {
	var from, fiat, fiatCurrency, asset, chain string
	c := &cobra.Command{
		Use:   "onramp",
		Short: "POST /v1/wallet-groups/{from}/onramp",
		RunE: func(cmd *cobra.Command, _ []string) error {
			cl, err := newClient(cmd, true)
			if err != nil {
				return err
			}
			body := map[string]any{
				"fiat_amount":   fiat,
				"fiat_currency": fiatCurrency,
				"asset":         asset,
			}
			if chain != "" {
				body["chain"] = chain
			}
			resp, status, err := cl.do(cmd.Context(), "POST",
				"/v1/wallet-groups/"+from+"/onramp", body)
			if err != nil {
				return err
			}
			if status != 200 && status != 201 {
				return failResponse(status, resp)
			}
			emit(cmd, resp)
			return nil
		},
	}
	c.Flags().StringVar(&from, "from", "", "destination wallet group id (required)")
	c.Flags().StringVar(&fiat, "fiat-amount", "", "fiat amount as decimal string (required)")
	c.Flags().StringVar(&fiatCurrency, "fiat-currency", "USD", "ISO fiat currency code")
	c.Flags().StringVar(&asset, "asset", "USDC", "crypto asset to receive")
	c.Flags().StringVar(&chain, "chain", "", "CrossMint chain override (base|polygon|solana)")
	_ = c.MarkFlagRequired("from")
	_ = c.MarkFlagRequired("fiat-amount")
	return c
}
