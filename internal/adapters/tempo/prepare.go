package tempo

import (
	"context"
	"encoding/hex"
	"fmt"
	"math/big"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	gethTypes "github.com/ethereum/go-ethereum/core/types"

	paygwerr "github.com/atara-xyz/atara-pay/internal/errors"
	apitypes "github.com/atara-xyz/atara-pay/internal/types"
)

// UnsignedTransfer is the result of BuildTransfer: every field the client
// needs to reconstruct, hash, and sign the transaction locally. The hex
// fields are 0x-prefixed lowercase, matching go-ethereum conventions so a
// client using ethers.js / web3.py can consume them without surprises.
//
// RawUnsignedHex is the RLP encoding of the unsigned LegacyTx. Clients that
// sign via eth_signTransaction-compatible APIs typically prefer this single
// blob over reconstructing the fields themselves.
type UnsignedTransfer struct {
	ChainID        int64  `json:"chain_id"`
	Nonce          uint64 `json:"nonce"`
	To             string `json:"to"`             // ERC-20 token contract address
	ValueHex       string `json:"value_hex"`      // always "0x0" for token transfers
	GasLimit       uint64 `json:"gas_limit"`
	GasPriceHex    string `json:"gas_price_hex"`
	DataHex        string `json:"data_hex"`       // ABI-encoded transfer(to, amount)
	RawUnsignedHex string `json:"raw_unsigned_hex"`
}

// BuildTransfer reproduces the unsigned-tx construction half of
// TransferWithKey: nonce, gas, calldata, target, value. It does NOT sign,
// and does NOT touch a keystore. Returns the blob the caller needs to send
// to the wallet owner for off-server signing.
//
// fromAddress is the sender; needed to pull the right nonce.
func (a *Adapter) BuildTransfer(
	ctx context.Context,
	fromAddress string,
	req apitypes.CreateTransactionRequest,
) (*UnsignedTransfer, error) {
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
		// Same fallback as TransferWithKey — Tempo gas estimation can be
		// picky on cold-start tokens; 200k covers a TIP-20 transfer with
		// comfortable margin.
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

	raw, err := tx.MarshalBinary()
	if err != nil {
		return nil, fmt.Errorf("marshal unsigned tx: %w", err)
	}

	return &UnsignedTransfer{
		ChainID:        a.client.ChainID().Int64(),
		Nonce:          nonce,
		To:             tokenAddr.Hex(),
		ValueHex:       "0x0",
		GasLimit:       gasLimit,
		GasPriceHex:    "0x" + gasPrice.Text(16),
		DataHex:        "0x" + hex.EncodeToString(data),
		RawUnsignedHex: "0x" + hex.EncodeToString(raw),
	}, nil
}

// BuildAuthorizeKey is the unsigned-tx counterpart of AuthorizeKey: it
// assembles the AccountKeychain.authorizeKey() precompile call into a
// LegacyTx, fetches nonce/gas estimates, and returns the RLP blob for the
// wallet owner to sign client-side.
//
// Used by the user-custody session-key flow (Phase 4): the wallet master
// key lives off-server, so we cannot broadcast directly — we hand the
// owner an unsigned authorizeKey call, they sign, then we submit via
// BroadcastSignedTx.
func (a *Adapter) BuildAuthorizeKey(
	ctx context.Context,
	walletAddress string,
	sessionKeyAddress string,
	sigType SignatureType,
	restrictions KeyRestrictions,
) (*UnsignedTransfer, error) {
	from := common.HexToAddress(walletAddress)

	calldata, err := PackAuthorizeKey(
		common.HexToAddress(sessionKeyAddress),
		sigType,
		restrictions,
	)
	if err != nil {
		return nil, err
	}

	to := common.HexToAddress(AccountKeychainAddress)

	nonce, err := a.client.Eth().PendingNonceAt(ctx, from)
	if err != nil {
		return nil, fmt.Errorf("authorize key build: nonce: %w", err)
	}
	gasPrice, err := a.client.Eth().SuggestGasPrice(ctx)
	if err != nil {
		return nil, fmt.Errorf("authorize key build: gas price: %w", err)
	}
	gasLimit, err := a.client.Eth().EstimateGas(ctx, ethereum.CallMsg{
		From: from, To: &to, Data: calldata,
	})
	if err != nil {
		// Same fallback as AuthorizeKey (signed path) — keychain precompile
		// gas estimation often refuses on cold-start; observed ~150-200k.
		gasLimit = 500_000
	}

	tx := gethTypes.NewTx(&gethTypes.LegacyTx{
		Nonce:    nonce,
		To:       &to,
		Value:    big.NewInt(0),
		Gas:      gasLimit,
		GasPrice: gasPrice,
		Data:     calldata,
	})

	raw, err := tx.MarshalBinary()
	if err != nil {
		return nil, fmt.Errorf("marshal unsigned authorize tx: %w", err)
	}

	return &UnsignedTransfer{
		ChainID:        a.client.ChainID().Int64(),
		Nonce:          nonce,
		To:             to.Hex(),
		ValueHex:       "0x0",
		GasLimit:       gasLimit,
		GasPriceHex:    "0x" + gasPrice.Text(16),
		DataHex:        "0x" + hex.EncodeToString(calldata),
		RawUnsignedHex: "0x" + hex.EncodeToString(raw),
	}, nil
}

// BroadcastSignedTx accepts a 0x-prefixed RLP-encoded signed transaction
// (matching what eth_sendRawTransaction expects) and broadcasts it to the
// Tempo RPC. Returns the on-chain transaction hash.
//
// The caller is responsible for verifying that the signed tx's recovered
// `from` matches the wallet address it was issued for — the chain itself
// will accept any valid signature.
func (a *Adapter) BroadcastSignedTx(ctx context.Context, signedHex string) (string, error) {
	raw, err := hexDecode(signedHex)
	if err != nil {
		return "", fmt.Errorf("%w: signed_tx_hex: %v", paygwerr.ErrBadRequest, err)
	}
	var tx gethTypes.Transaction
	if err := tx.UnmarshalBinary(raw); err != nil {
		return "", fmt.Errorf("%w: decode signed tx: %v", paygwerr.ErrBadRequest, err)
	}
	if err := a.client.Eth().SendTransaction(ctx, &tx); err != nil {
		return "", fmt.Errorf("send tx: %w", err)
	}
	return tx.Hash().Hex(), nil
}

// hexDecode tolerates the optional "0x" prefix and uppercase digits.
func hexDecode(s string) ([]byte, error) {
	if len(s) >= 2 && (s[0:2] == "0x" || s[0:2] == "0X") {
		s = s[2:]
	}
	return hex.DecodeString(s)
}
