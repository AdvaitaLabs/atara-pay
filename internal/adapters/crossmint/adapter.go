package crossmint

import (
	"context"
	"fmt"
	"strings"
	"time"

	paygwerr "github.com/atara-xyz/atara-pay/internal/errors"
	"github.com/atara-xyz/atara-pay/internal/types"
)

// Adapter implements adapters.Adapter for CrossMint.
type Adapter struct {
	client *Client
}

// New returns a CrossMint Adapter.
func New(cfg Config) (*Adapter, error) {
	c, err := NewClient(cfg)
	if err != nil {
		return nil, err
	}
	return &Adapter{client: c}, nil
}

func (a *Adapter) Rail() types.Rail { return types.RailCrossMint }

func (a *Adapter) Chains() []types.Chain {
	return []types.Chain{
		types.ChainBase, types.ChainEthereum, types.ChainPolygon,
		types.ChainSolana, types.ChainStellar,
	}
}

// CreateWallet provisions a CrossMint smart wallet for the given owner.
func (a *Adapter) CreateWallet(ctx context.Context, req types.CreateWalletRequest) (*types.Wallet, error) {
	wt := req.Type
	if wt == "" {
		wt = "smart"
	}
	chain := string(req.Chain)
	if chain == "" {
		chain = string(types.ChainBase)
	}

	body := walletRequest{Type: wt, Chain: chain}
	switch req.Owner.Type {
	case "email":
		body.Linked.Email = req.Owner.Value
	case "userId":
		body.Linked.UserID = req.Owner.Value
	case "phone":
		body.Linked.Phone = req.Owner.Value
	case "external", "":
		body.Linked.External = req.Owner.Value
	default:
		return nil, fmt.Errorf("%w: unknown owner type %q", paygwerr.ErrBadRequest, req.Owner.Type)
	}

	var resp walletResponse
	if err := a.client.do(ctx, "POST", "/api/"+apiVersion+"/wallets", body, &resp); err != nil {
		return nil, err
	}

	return toWallet(resp, req.Owner), nil
}

// GetWallet fetches a wallet by its CrossMint id or address.
func (a *Adapter) GetWallet(ctx context.Context, locator string) (*types.Wallet, error) {
	var resp walletResponse
	if err := a.client.do(ctx, "GET", "/api/"+apiVersion+"/wallets/"+locator, nil, &resp); err != nil {
		return nil, err
	}
	return toWallet(resp, types.Owner{}), nil
}

// GetBalances returns balances across all chains/tokens for the wallet.
func (a *Adapter) GetBalances(ctx context.Context, locator string) ([]types.Balance, error) {
	var resp balancesResponse
	if err := a.client.do(ctx, "GET", "/api/"+apiVersion+"/wallets/"+locator+"/balances", nil, &resp); err != nil {
		return nil, err
	}
	out := make([]types.Balance, 0, len(resp))
	for _, b := range resp {
		out = append(out, types.Balance{
			Asset:    types.Asset(strings.ToUpper(b.Token)),
			Chain:    types.Chain(b.Chain),
			Amount:   b.Amount,
			Decimals: b.Decimals,
		})
	}
	return out, nil
}

// CreateTransaction sends a token transfer via CrossMint's typed
// /tokens/{locator}/transfers endpoint when possible — it handles gas, gas
// sponsoring, and approvals for us.
func (a *Adapter) CreateTransaction(
	ctx context.Context,
	req types.CreateTransactionRequest,
	fromLocator string,
) (*types.Transaction, error) {
	chain := req.Chain
	if chain == "" {
		chain = types.ChainBase
	}
	tokenLocator := fmt.Sprintf("%s:%s", strings.ToLower(string(chain)), strings.ToLower(string(req.Asset)))

	body := tokenTransferRequest{Recipient: req.To, Amount: req.Amount}
	path := fmt.Sprintf("/api/%s/wallets/%s/tokens/%s/transfers",
		apiVersion, fromLocator, tokenLocator)

	var resp txResponse
	if err := a.client.do(ctx, "POST", path, body, &resp); err != nil {
		return nil, err
	}

	return &types.Transaction{
		ID:        resp.ID,
		Rail:      types.RailCrossMint,
		From:      fromLocator,
		To:        req.To,
		Amount:    req.Amount,
		Asset:     req.Asset,
		Chain:     chain,
		Status:    mapTxStatus(resp.Status),
		TxHash:    txHash(resp),
		CreatedAt: parseTime(resp.CreatedAt),
	}, nil
}

// GetTransaction looks up a CrossMint transaction. Note: CrossMint scopes
// transactions to a wallet; this helper requires "<walletId>/<txId>".
func (a *Adapter) GetTransaction(ctx context.Context, txID string) (*types.Transaction, error) {
	parts := strings.SplitN(txID, "/", 2)
	if len(parts) != 2 {
		return nil, fmt.Errorf("%w: crossmint txID must be \"<walletId>/<txId>\"", paygwerr.ErrBadRequest)
	}
	var resp txResponse
	path := fmt.Sprintf("/api/%s/wallets/%s/transactions/%s", apiVersion, parts[0], parts[1])
	if err := a.client.do(ctx, "GET", path, nil, &resp); err != nil {
		return nil, err
	}
	return &types.Transaction{
		ID:        resp.ID,
		Rail:      types.RailCrossMint,
		Status:    mapTxStatus(resp.Status),
		TxHash:    txHash(resp),
		CreatedAt: parseTime(resp.CreatedAt),
	}, nil
}

// CreateOnrampOrder starts a fiat → stablecoin hosted-checkout order.
func (a *Adapter) CreateOnrampOrder(
	ctx context.Context,
	req types.CreateOnrampRequest,
	walletAddress string,
) (*types.OnrampOrder, error) {
	chain := req.Chain
	if chain == "" {
		chain = types.ChainBase
	}
	tokenLocator := fmt.Sprintf("%s:%s", strings.ToLower(string(chain)), strings.ToLower(string(req.Asset)))

	body := onrampOrderRequest{
		Recipient: onrampRecipient{WalletAddress: walletAddress},
		Payment:   onrampPayment{Method: "fiat", Currency: strings.ToLower(req.Fiat.Currency)},
		LineItems: []onrampItem{{
			TokenLocator:        tokenLocator,
			ExecutionParameters: onrampExec{Mode: "exact-in", Amount: req.Fiat.Amount},
		}},
	}

	var resp onrampOrderResponse
	// 2022-06-09 is the still-current Orders API namespace per CrossMint docs.
	if err := a.client.do(ctx, "POST", "/api/2022-06-09/orders", body, &resp); err != nil {
		return nil, err
	}

	return &types.OnrampOrder{
		ID:          resp.Order.OrderID,
		Rail:        types.RailCrossMint,
		WalletID:    walletAddress,
		Fiat:        req.Fiat,
		Asset:       req.Asset,
		Status:      resp.Order.Phase,
		CheckoutURL: resp.HostedCheckoutURL,
		CreatedAt:   time.Now().UTC(),
	}, nil
}

// --- helpers ---

func toWallet(r walletResponse, owner types.Owner) *types.Wallet {
	return &types.Wallet{
		ID:        r.ID,
		Rail:      types.RailCrossMint,
		Chain:     types.Chain(r.Chain),
		Address:   r.Address,
		Owner:     owner,
		Locator:   r.ID,
		CreatedAt: parseTime(r.CreatedAt),
	}
}

func mapTxStatus(s string) types.TxStatus {
	switch strings.ToLower(s) {
	case "success", "succeeded", "completed":
		return types.TxSucceeded
	case "failed", "rejected", "cancelled":
		return types.TxFailed
	default:
		return types.TxPending
	}
}

func txHash(r txResponse) string {
	if r.OnChain == nil {
		return ""
	}
	return r.OnChain.TxHash
}

func parseTime(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}
	}
	return t
}
