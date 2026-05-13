// Package types defines Atara-Pay's unified API contracts.
// Adapters translate provider-specific shapes into these types so the public
// API never leaks rail details.
package types

import "time"

// Rail identifies a payment provider.
type Rail string

const (
	RailCrossMint Rail = "crossmint"
	RailTempo     Rail = "tempo"
	RailLokaLN    Rail = "loka-ln"
)

// Chain is the on-chain network a wallet/transaction lives on.
type Chain string

const (
	ChainBase     Chain = "base"
	ChainEthereum Chain = "ethereum"
	ChainPolygon  Chain = "polygon"
	ChainSolana   Chain = "solana"
	ChainStellar  Chain = "stellar"
	ChainSui      Chain = "sui"
	ChainTempo    Chain = "tempo"
	ChainBitcoin  Chain = "bitcoin"
)

// Asset is a token symbol. Atara-Pay uses uppercase tickers.
type Asset string

const (
	AssetUSDC    Asset = "USDC"
	AssetUSDT    Asset = "USDT"
	AssetPathUSD Asset = "pathUSD"
	AssetSats    Asset = "sats"
	AssetSUI     Asset = "SUI"
)

// Owner identifies who controls a wallet.
type Owner struct {
	Type  string `json:"type"`  // "email" | "userId" | "phone" | "external"
	Value string `json:"value"` // e.g. "alice@example.com"
}

// Wallet is the unified wallet representation across all rails.
type Wallet struct {
	ID        string    `json:"id"`         // Atara-Pay-issued id, e.g. wlt_01J...
	Rail      Rail      `json:"rail"`       // which provider owns it
	Chain     Chain     `json:"chain"`      // on-chain network
	Address   string    `json:"address"`    // on-chain address
	Owner     Owner     `json:"owner"`      // controller
	Locator   string    `json:"locator"`    // provider-side identifier (opaque)
	CreatedAt time.Time `json:"created_at"`
}

// CreateWalletRequest is the unified wallet-creation input.
type CreateWalletRequest struct {
	Rail  Rail   `json:"rail"`  // optional; router fills if absent
	Chain Chain  `json:"chain"` // optional; defaults per rail
	Owner Owner  `json:"owner"`
	Type  string `json:"type,omitempty"` // optional, rail-specific (e.g. "smart" | "evm-mpc")
}

// Balance reports holdings for one asset.
type Balance struct {
	Asset    Asset  `json:"asset"`
	Chain    Chain  `json:"chain"`
	Amount   string `json:"amount"`    // decimal string, e.g. "12.345"
	Decimals int    `json:"decimals"`
}

// Transaction is the unified transfer representation.
type Transaction struct {
	ID        string    `json:"id"`
	Rail      Rail      `json:"rail"`
	From      string    `json:"from"`    // Atara-Pay wallet id
	To        string    `json:"to"`      // address or wallet id
	Amount    string    `json:"amount"`
	Asset     Asset     `json:"asset"`
	Chain     Chain     `json:"chain"`
	Status    TxStatus  `json:"status"`
	TxHash    string    `json:"tx_hash,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

// TxStatus is the unified transfer status.
type TxStatus string

const (
	TxPending   TxStatus = "pending"
	TxSucceeded TxStatus = "succeeded"
	TxFailed    TxStatus = "failed"
)

// CreateTransactionRequest initiates a transfer.
type CreateTransactionRequest struct {
	From   string `json:"from"`
	To     string `json:"to"`
	Amount string `json:"amount"`
	Asset  Asset  `json:"asset"`
	Chain  Chain  `json:"chain,omitempty"` // optional; inferred from source wallet
}

// FiatAmount is a fiat money value.
type FiatAmount struct {
	Amount   string `json:"amount"`   // decimal string
	Currency string `json:"currency"` // ISO 4217, e.g. "USD"
}

// OnrampOrder represents a fiat → stablecoin order.
type OnrampOrder struct {
	ID          string     `json:"id"`
	Rail        Rail       `json:"rail"`
	WalletID    string     `json:"wallet_id"`
	Fiat        FiatAmount `json:"fiat"`
	Asset       Asset      `json:"asset"`
	Status      string     `json:"status"`
	CheckoutURL string     `json:"checkout_url,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
}

// CreateOnrampRequest starts an onramp checkout.
type CreateOnrampRequest struct {
	WalletID string     `json:"wallet_id"`
	Fiat     FiatAmount `json:"fiat"`
	Asset    Asset      `json:"asset"`
	Chain    Chain      `json:"chain,omitempty"`
}
