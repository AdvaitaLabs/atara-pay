package tempo

import (
	"context"
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	gethTypes "github.com/ethereum/go-ethereum/core/types"

	paygwerr "github.com/atara-xyz/atara-pay/internal/errors"
	apitypes "github.com/atara-xyz/atara-pay/internal/types"
)

// Adapter implements adapters.Adapter for the Tempo L1.
type Adapter struct {
	client *Client
	ks     Keystore
}

// New builds a Tempo Adapter. cfg.RPCURL and cfg.ChainID are required.
func New(cfg Config, ks Keystore) (*Adapter, error) {
	if ks == nil {
		ks = NewMemoryKeystore()
	}
	c, err := NewClient(cfg)
	if err != nil {
		return nil, err
	}
	return &Adapter{client: c, ks: ks}, nil
}

func (a *Adapter) Rail() apitypes.Rail { return apitypes.RailTempo }

func (a *Adapter) Chains() []apitypes.Chain {
	return []apitypes.Chain{apitypes.ChainTempo}
}

// CreateWallet provisions a Tempo wallet. We generate a fresh secp256k1
// key in the keystore and return the derived EOA address as the wallet id.
//
// For the MVP, the keystore is in-memory (see keystore.go). Swap it for a
// KMS/Vault implementation in production.
func (a *Adapter) CreateWallet(
	ctx context.Context,
	req apitypes.CreateWalletRequest,
) (*apitypes.Wallet, error) {
	addr, err := a.ks.Generate()
	if err != nil {
		return nil, err
	}
	return &apitypes.Wallet{
		ID:        addr,
		Rail:      apitypes.RailTempo,
		Chain:     apitypes.ChainTempo,
		Address:   addr,
		Owner:     req.Owner,
		Locator:   addr,
		CreatedAt: time.Now().UTC(),
	}, nil
}

// GetWallet returns the wallet if it lives in this gateway's keystore.
func (a *Adapter) GetWallet(ctx context.Context, locator string) (*apitypes.Wallet, error) {
	if !a.ks.Has(locator) {
		return nil, fmt.Errorf("%w: wallet %s", paygwerr.ErrNotFound, locator)
	}
	return &apitypes.Wallet{
		ID:      locator,
		Rail:    apitypes.RailTempo,
		Chain:   apitypes.ChainTempo,
		Address: common.HexToAddress(locator).Hex(),
		Locator: locator,
	}, nil
}

// GetBalances reads the pathUSD balance for the wallet. Extend asset list as
// new TIP-20 tokens are supported.
func (a *Adapter) GetBalances(ctx context.Context, locator string) ([]apitypes.Balance, error) {
	owner := common.HexToAddress(locator)
	bal, err := a.tokenBalance(ctx, common.HexToAddress(PathUSDAddress), owner)
	if err != nil {
		return nil, err
	}
	return []apitypes.Balance{{
		Asset:    apitypes.AssetPathUSD,
		Chain:    apitypes.ChainTempo,
		Amount:   formatUnits(bal, TIP20Decimals),
		Decimals: TIP20Decimals,
	}}, nil
}

// CreateTransaction signs and broadcasts a TIP-20 transfer.
func (a *Adapter) CreateTransaction(
	ctx context.Context,
	req apitypes.CreateTransactionRequest,
	fromLocator string,
) (*apitypes.Transaction, error) {
	priv, err := a.ks.Get(fromLocator)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", paygwerr.ErrBadRequest, err)
	}
	from := common.HexToAddress(fromLocator)
	to := common.HexToAddress(req.To)

	tokenAddr, err := tokenAddressFor(string(req.Asset))
	if err != nil {
		return nil, fmt.Errorf("%w: %v", paygwerr.ErrBadRequest, err)
	}

	amount, err := parseUnits(req.Amount, TIP20Decimals)
	if err != nil {
		return nil, fmt.Errorf("%w: amount %q: %v", paygwerr.ErrBadRequest, req.Amount, err)
	}

	data, err := packTransfer(to, amount)
	if err != nil {
		return nil, fmt.Errorf("pack transfer: %w", err)
	}

	nonce, err := a.client.Eth().PendingNonceAt(ctx, from)
	if err != nil {
		return nil, fmt.Errorf("nonce: %w", err)
	}

	gasPrice, err := a.client.Eth().SuggestGasPrice(ctx)
	if err != nil {
		return nil, fmt.Errorf("gas price: %w", err)
	}

	gasLimit, err := a.client.Eth().EstimateGas(ctx, ethereum.CallMsg{
		From: from, To: &tokenAddr, Data: data,
	})
	if err != nil {
		// Fall back to a generous default — Tempo gas estimation can be picky
		// on cold-start tokens. 200k is well over a TIP-20 transfer's cost.
		gasLimit = 200_000
	}

	tx := types.NewTx(&types.LegacyTx{
		Nonce:    nonce,
		To:       &tokenAddr,
		Value:    big.NewInt(0),
		Gas:      gasLimit,
		GasPrice: gasPrice,
		Data:     data,
	})

	signer := gethTypes.LatestSignerForChainID(a.client.ChainID())
	signed, err := gethTypes.SignTx(tx, signer, priv)
	if err != nil {
		return nil, fmt.Errorf("sign tx: %w", err)
	}

	if err := a.client.Eth().SendTransaction(ctx, signed); err != nil {
		return nil, fmt.Errorf("send tx: %w", err)
	}

	return &apitypes.Transaction{
		ID:        signed.Hash().Hex(),
		Rail:      apitypes.RailTempo,
		From:      fromLocator,
		To:        req.To,
		Amount:    req.Amount,
		Asset:     req.Asset,
		Chain:     apitypes.ChainTempo,
		Status:    apitypes.TxPending,
		TxHash:    signed.Hash().Hex(),
		CreatedAt: time.Now().UTC(),
	}, nil
}

// GetTransaction looks up a tx by hash and maps receipt status.
func (a *Adapter) GetTransaction(ctx context.Context, txID string) (*apitypes.Transaction, error) {
	hash := common.HexToHash(txID)

	receipt, err := a.client.Eth().TransactionReceipt(ctx, hash)
	if err != nil {
		// Receipt may simply not exist yet — treat as pending.
		if strings.Contains(err.Error(), "not found") {
			return &apitypes.Transaction{
				ID: txID, Rail: apitypes.RailTempo, Status: apitypes.TxPending, TxHash: txID,
			}, nil
		}
		return nil, err
	}

	status := apitypes.TxFailed
	if receipt.Status == 1 {
		status = apitypes.TxSucceeded
	}
	return &apitypes.Transaction{
		ID:     txID,
		Rail:   apitypes.RailTempo,
		Chain:  apitypes.ChainTempo,
		Status: status,
		TxHash: txID,
	}, nil
}

// CreateOnrampOrder: Tempo has no native fiat ramp. Callers should route fiat
// onramp through CrossMint (or Atara OTC, when shipped).
func (a *Adapter) CreateOnrampOrder(
	ctx context.Context,
	req apitypes.CreateOnrampRequest,
	walletAddress string,
) (*apitypes.OnrampOrder, error) {
	return nil, fmt.Errorf("%w: tempo has no fiat onramp; use rail=crossmint", paygwerr.ErrUnsupported)
}

// --- helpers ---

func (a *Adapter) tokenBalance(ctx context.Context, token, owner common.Address) (*big.Int, error) {
	data, err := packBalanceOf(owner)
	if err != nil {
		return nil, err
	}
	out, err := a.client.Eth().CallContract(ctx, ethereum.CallMsg{To: &token, Data: data}, nil)
	if err != nil {
		return nil, fmt.Errorf("call balanceOf: %w", err)
	}
	return unpackBalanceOf(out)
}

// parseUnits converts a decimal string like "1.25" to its integer
// representation at the given decimals (e.g. 1250000 for 6 decimals).
func parseUnits(amount string, decimals int) (*big.Int, error) {
	amount = strings.TrimSpace(amount)
	if amount == "" {
		return nil, fmt.Errorf("empty amount")
	}
	neg := strings.HasPrefix(amount, "-")
	if neg {
		return nil, fmt.Errorf("negative amount")
	}

	parts := strings.SplitN(amount, ".", 2)
	intPart := parts[0]
	fracPart := ""
	if len(parts) == 2 {
		fracPart = parts[1]
	}
	if len(fracPart) > decimals {
		return nil, fmt.Errorf("fractional part exceeds %d decimals", decimals)
	}
	fracPart += strings.Repeat("0", decimals-len(fracPart))

	combined := strings.TrimLeft(intPart+fracPart, "0")
	if combined == "" {
		combined = "0"
	}
	out, ok := new(big.Int).SetString(combined, 10)
	if !ok {
		return nil, fmt.Errorf("invalid number")
	}
	return out, nil
}

// formatUnits is the inverse of parseUnits.
func formatUnits(n *big.Int, decimals int) string {
	if n == nil {
		return "0"
	}
	s := n.String()
	if len(s) <= decimals {
		s = strings.Repeat("0", decimals-len(s)+1) + s
	}
	cut := len(s) - decimals
	intPart := s[:cut]
	fracPart := strings.TrimRight(s[cut:], "0")
	if fracPart == "" {
		return intPart
	}
	return intPart + "." + fracPart
}
