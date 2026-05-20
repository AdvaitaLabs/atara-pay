package tempo

import (
	"context"
	"fmt"
	"math/big"
	"time"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	gethTypes "github.com/ethereum/go-ethereum/core/types"
	gethCrypto "github.com/ethereum/go-ethereum/crypto"

	paygwerr "github.com/atara-xyz/atara-pay/internal/errors"
	apitypes "github.com/atara-xyz/atara-pay/internal/types"
)

// TransferWithKey signs and broadcasts a TIP-20 transfer using the supplied
// raw private-key bytes. The caller is responsible for sourcing those bytes
// (typically by decrypting wallets.encrypted_private_key via the keystore).
//
// This is the primitive the M3.4 wallet-group orchestrator will use. The
// older Adapter.CreateTransaction (still backed by the in-memory keystore)
// stays in place during the migration window — once every code path
// upgrades, the keystore field on Adapter can be removed.
func (a *Adapter) TransferWithKey(
	ctx context.Context,
	privBytes []byte,
	req apitypes.CreateTransactionRequest,
	fromAddress string,
) (*apitypes.Transaction, error) {
	priv, err := privateKeyFromBytes(privBytes)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", paygwerr.ErrBadRequest, err)
	}

	// Defensive: confirm the decrypted key actually owns the wallet address
	// we were told. Mismatch implies either a DB swap, a keystore version
	// confusion, or a code bug — never silently sign with the wrong key.
	derived := gethCrypto.PubkeyToAddress(priv.PublicKey)
	if derived != common.HexToAddress(fromAddress) {
		return nil, fmt.Errorf("tempo: private key derives %s but wallet record says %s",
			derived.Hex(), fromAddress)
	}

	from := common.HexToAddress(fromAddress)
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
		// Tempo gas estimation can be picky on cold-start tokens; 200k is
		// well over a TIP-20 transfer's cost.
		gasLimit = 200_000
	}

	tx := gethTypes.NewTx(&gethTypes.LegacyTx{
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
		From:      fromAddress,
		To:        req.To,
		Amount:    req.Amount,
		Asset:     req.Asset,
		Chain:     apitypes.ChainTempo,
		Status:    apitypes.TxPending,
		TxHash:    signed.Hash().Hex(),
		CreatedAt: time.Now().UTC(),
	}, nil
}

