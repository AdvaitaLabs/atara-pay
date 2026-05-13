// Package adapters defines the Adapter interface that every rail must implement.
// Adapters are pure protocol clients: they translate Atara-Pay's unified types to and
// from provider-specific shapes and own no business state.
package adapters

import (
	"context"

	"github.com/atara-xyz/atara-pay/internal/types"
)

// Adapter is the contract every payment rail must satisfy.
//
// Methods that a rail does not natively support should return ErrUnsupported
// (see internal/errors). The router will then either skip that rail or fail
// fast with a 4xx surfacing the gap to the caller.
type Adapter interface {
	// Rail returns the rail identifier this adapter implements.
	Rail() types.Rail

	// Chains lists the on-chain networks this adapter can operate on.
	Chains() []types.Chain

	// CreateWallet provisions a new wallet on the rail.
	CreateWallet(ctx context.Context, req types.CreateWalletRequest) (*types.Wallet, error)

	// GetWallet fetches an existing wallet by its rail-side locator.
	GetWallet(ctx context.Context, locator string) (*types.Wallet, error)

	// GetBalances reads the balances of a wallet.
	GetBalances(ctx context.Context, locator string) ([]types.Balance, error)

	// CreateTransaction submits a transfer. The returned Transaction may be in
	// the "pending" state — callers poll GetTransaction or rely on webhooks.
	CreateTransaction(ctx context.Context, req types.CreateTransactionRequest, fromLocator string) (*types.Transaction, error)

	// GetTransaction fetches a transaction by its rail-side id.
	GetTransaction(ctx context.Context, txID string) (*types.Transaction, error)

	// CreateOnrampOrder starts a fiat → stablecoin checkout flow.
	// Rails without a fiat ramp return ErrUnsupported.
	CreateOnrampOrder(ctx context.Context, req types.CreateOnrampRequest, walletLocator string) (*types.OnrampOrder, error)
}
