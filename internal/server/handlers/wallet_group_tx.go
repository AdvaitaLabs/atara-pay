package handlers

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/gofiber/fiber/v2"

	"github.com/atara-xyz/atara-pay/internal/db/sqlcgen"
	"github.com/atara-xyz/atara-pay/internal/id"
	"github.com/atara-xyz/atara-pay/internal/limits"
	"github.com/atara-xyz/atara-pay/internal/server/middleware"
	apitypes "github.com/atara-xyz/atara-pay/internal/types"
)

// SendTransactionRequest is the JSON body of POST /v1/wallet-groups/:id/transactions.
type SendTransactionRequest struct {
	To             string `json:"to"`
	Amount         string `json:"amount"`
	Asset          string `json:"asset"`
	IdempotencyKey string `json:"idempotency_key,omitempty"`
	// Rail optionally overrides the asset → rail mapping. Accepted values
	// match the wallets.rail column ("crossmint" | "tempo").
	Rail string `json:"rail,omitempty"`
}

// TransactionView is the safe-to-return projection of a transactions row.
type TransactionView struct {
	ID           string `json:"id"`
	Rail         string `json:"rail"`
	Chain        string `json:"chain"`
	Status       string `json:"status"`
	From         string `json:"from"`
	To           string `json:"to"`
	Amount       string `json:"amount"`
	Asset        string `json:"asset"`
	TxHash       string `json:"tx_hash,omitempty"`
	ProviderTxID string `json:"provider_tx_id,omitempty"`
	CreatedAt    string `json:"created_at,omitempty"`
}

// SendTransaction implements POST /v1/wallet-groups/:id/transactions.
//
// Routing rules (asset → rail):
//
//	USDC, USDT          → CrossMint
//	pathUSD             → Tempo
//
// Override via body.rail when the customer wants to force a specific rail
// (only allowed if the group has a wallet on that rail).
func (h *WalletGroups) SendTransaction(c *fiber.Ctx) error {
	tenantID := middleware.TenantID(c)
	if tenantID == "" {
		return badRequest(c, "missing tenant context")
	}
	groupID := c.Params("id")
	if groupID == "" {
		return badRequest(c, "missing group id")
	}

	var req SendTransactionRequest
	if err := c.BodyParser(&req); err != nil {
		return badRequest(c, "invalid JSON body")
	}
	if err := validateSendTx(req); err != nil {
		return badRequest(c, err.Error())
	}

	ctx := c.UserContext()

	// Load group (tenant-scoped — cross-tenant probes get 404).
	group, err := h.q.GetWalletGroupByID(ctx, groupID)
	if err != nil || group.TenantID != tenantID {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "wallet group not found"})
	}

	// Idempotency: per-tenant key (the partial UNIQUE in migration 000003).
	// We INSERT after the rail call succeeds; if a duplicate key sneaks in
	// later, the DB returns SQLSTATE 23505 and we surface the existing row.
	// MVP: no replay-cache here, the client retries get a fresh result on
	// transient failures. Full caching arrives with M9 idempotency cache.

	// Pick the rail.
	rail, err := h.routeRail(req)
	if err != nil {
		return badRequest(c, err.Error())
	}

	// Find the matching wallet in this group.
	wallet, err := h.q.GetWalletByGroupAndRail(ctx, sqlcgen.GetWalletByGroupAndRailParams{
		GroupID: group.ID,
		Rail:    string(rail),
	})
	if err != nil {
		return c.Status(fiber.StatusUnprocessableEntity).JSON(fiber.Map{
			"error": fmt.Sprintf("group has no active %s wallet", rail),
		})
	}

	// Limit gate. Runs BEFORE the rail call so a denied attempt never
	// charges upstream. The limits service writes the violation row
	// internally on deny; we surface a 429 with the violation details so
	// the customer can react. limits.Check returns Allowed=true when no
	// policy applies (e.g. a tenant that disabled their default policy).
	if h.limits != nil {
		decision, derr := h.limits.Check(ctx, limits.CheckRequest{
			TenantID:  tenantID,
			WalletID:  wallet.ID,
			Amount:    req.Amount,
			Asset:     req.Asset,
			Recipient: req.To,
		})
		if derr != nil {
			return internalError(c, derr)
		}
		if !decision.Allowed {
			return c.Status(fiber.StatusTooManyRequests).JSON(fiber.Map{
				"error":     "spending limit exceeded",
				"violation": decision.Violation,
				"policy_id": decision.PolicyID,
			})
		}
	}

	// Dispatch.
	var (
		txArtifact *apitypes.Transaction
		dispatchErr error
	)
	switch rail {
	case apitypes.RailCrossMint:
		txArtifact, dispatchErr = h.sendViaCrossMint(ctx, wallet, req)
	case apitypes.RailTempo:
		txArtifact, dispatchErr = h.sendViaTempo(ctx, wallet, req)
	default:
		return c.Status(fiber.StatusNotImplemented).JSON(fiber.Map{
			"error": fmt.Sprintf("rail %s is not yet supported by this endpoint", rail),
		})
	}
	if dispatchErr != nil {
		return upstreamError(c, string(rail), dispatchErr)
	}

	// Persist the audit row.
	txRow, err := h.recordTransaction(ctx, tenantID, middleware.APIKeyID(c), group, wallet, req, txArtifact)
	if err != nil {
		// Rail charged but we couldn't record. Log loudly via the response
		// rather than swallow — better to fail visible than have invisible
		// ledger drift. The rail's tx_hash still flows back so customers
		// can verify on-chain.
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error":        "internal error recording transaction (rail call succeeded)",
			"rail":         string(rail),
			"tx_hash":      txArtifact.TxHash,
			"provider_id":  txArtifact.ID,
			"persist_error": err.Error(),
		})
	}
	return c.Status(fiber.StatusCreated).JSON(toTxView(txRow))
}

// ──────────────────────────────────────────────────────────────────────
// dispatchers
// ──────────────────────────────────────────────────────────────────────

func (h *WalletGroups) sendViaCrossMint(
	ctx context.Context, w sqlcgen.Wallet, req SendTransactionRequest,
) (*apitypes.Transaction, error) {
	return h.crossmint.CreateTransaction(ctx, apitypes.CreateTransactionRequest{
		From:   w.Address,
		To:     req.To,
		Amount: req.Amount,
		Asset:  apitypes.Asset(req.Asset),
		Chain:  apitypes.Chain(w.Chain),
	}, w.ProviderLocator)
}

func (h *WalletGroups) sendViaTempo(
	ctx context.Context, w sqlcgen.Wallet, req SendTransactionRequest,
) (*apitypes.Transaction, error) {
	if len(w.EncryptedPrivateKey) == 0 || !w.KeyVersion.Valid {
		return nil, errors.New("tempo wallet row has no encrypted key (schema invariant violated)")
	}
	priv, err := h.ks.Decrypt(w.EncryptedPrivateKey, w.KeyVersion.Int16)
	if err != nil {
		return nil, fmt.Errorf("keystore decrypt: %w", err)
	}
	// Zero the plaintext key as soon as the adapter is done.
	defer func() {
		for i := range priv {
			priv[i] = 0
		}
	}()

	return h.tempo.TransferWithKey(ctx, priv, apitypes.CreateTransactionRequest{
		From:   w.Address,
		To:     req.To,
		Amount: req.Amount,
		Asset:  apitypes.Asset(req.Asset),
		Chain:  apitypes.ChainTempo,
	}, w.Address)
}

// ──────────────────────────────────────────────────────────────────────
// persistence
// ──────────────────────────────────────────────────────────────────────

func (h *WalletGroups) recordTransaction(
	ctx context.Context,
	tenantID, apiKeyID string,
	g sqlcgen.WalletGroup,
	w sqlcgen.Wallet,
	req SendTransactionRequest,
	artifact *apitypes.Transaction,
) (sqlcgen.Transaction, error) {
	return h.q.CreateTransaction(ctx, sqlcgen.CreateTransactionParams{
		ID:                id.New(id.PrefixTransaction),
		TenantID:          tenantID,
		WalletID:          w.ID,
		GroupID:           g.ID,
		Rail:              w.Rail,
		Chain:             w.Chain,
		Direction:         "outbound",
		Counterparty:      req.To,
		Amount:            req.Amount,
		Asset:             req.Asset,
		Status:            string(artifact.Status),
		TxHash:            pgxText(artifact.TxHash),
		ProviderTxID:      pgxText(artifact.ID),
		InitiatedByApikey: pgxText(apiKeyID),
		IdempotencyKey:    pgxText(req.IdempotencyKey),
		Metadata:          []byte("{}"),
	})
}

// ──────────────────────────────────────────────────────────────────────
// helpers
// ──────────────────────────────────────────────────────────────────────

func validateSendTx(r SendTransactionRequest) error {
	if r.To == "" {
		return errors.New("to is required")
	}
	if r.Amount == "" {
		return errors.New("amount is required")
	}
	if r.Asset == "" {
		return errors.New("asset is required")
	}
	return nil
}

// routeRail picks the destination rail from the asset, allowing an explicit
// body.rail override.
func (h *WalletGroups) routeRail(r SendTransactionRequest) (apitypes.Rail, error) {
	if r.Rail != "" {
		switch r.Rail {
		case string(apitypes.RailCrossMint), string(apitypes.RailTempo):
			return apitypes.Rail(r.Rail), nil
		default:
			return "", fmt.Errorf("unsupported rail override %q", r.Rail)
		}
	}
	switch strings.ToUpper(r.Asset) {
	case "USDC", "USDT":
		return apitypes.RailCrossMint, nil
	case "PATHUSD":
		return apitypes.RailTempo, nil
	default:
		return "", fmt.Errorf("no default rail for asset %q (pass body.rail to override)", r.Asset)
	}
}

func toTxView(t sqlcgen.Transaction) TransactionView {
	v := TransactionView{
		ID:     t.ID,
		Rail:   t.Rail,
		Chain:  t.Chain,
		Status: t.Status,
		From:   t.WalletID,
		To:     t.Counterparty,
		Amount: t.Amount,
		Asset:  t.Asset,
	}
	if t.TxHash.Valid {
		v.TxHash = t.TxHash.String
	}
	if t.ProviderTxID.Valid {
		v.ProviderTxID = t.ProviderTxID.String
	}
	if t.CreatedAt.Valid {
		v.CreatedAt = t.CreatedAt.Time.UTC().Format("2006-01-02T15:04:05Z")
	}
	return v
}
