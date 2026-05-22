package handlers

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/atara-xyz/atara-pay/internal/adapters/tempo"
	"github.com/atara-xyz/atara-pay/internal/db/sqlcgen"
	"github.com/atara-xyz/atara-pay/internal/id"
	apitypes "github.com/atara-xyz/atara-pay/internal/types"
	"github.com/atara-xyz/atara-pay/internal/server/middleware"
)

// PrepareTransactionRequest is the JSON body of
// POST /v1/wallet-groups/:id/transactions/prepare.
//
// Identical shape to SendTransactionRequest minus the SignerID — session
// keys cannot delegate spends from a wallet whose key the caller holds.
type PrepareTransactionRequest struct {
	To             string `json:"to"`
	Amount         string `json:"amount"`
	Asset          string `json:"asset"`
	Memo           string `json:"memo,omitempty"`
	IdempotencyKey string `json:"idempotency_key,omitempty"`
}

// PreparedTxView is the response shape of the prepare endpoint. It carries
// the persisted transaction id (clients pass it back to submit) plus
// everything needed for client-side signing.
type PreparedTxView struct {
	PrepareID  string                  `json:"prepare_id"`
	WalletID   string                  `json:"wallet_id"`
	GroupID    string                  `json:"group_id"`
	Rail       string                  `json:"rail"`
	Asset      string                  `json:"asset"`
	Amount     string                  `json:"amount"`
	From       string                  `json:"from"`
	To         string                  `json:"to"`
	Unsigned   *tempo.UnsignedTransfer `json:"unsigned"`
	Status     string                  `json:"status"` // always "prepared"
	ExpiresAt  string                  `json:"expires_at"`
	CreatedAt  string                  `json:"created_at,omitempty"`
}

// preparedTxMetadata is what we shove into transactions.metadata for a
// 'prepared' row. JSON-encoded so future fields can be added without a
// schema migration.
type preparedTxMetadata struct {
	Unsigned  *tempo.UnsignedTransfer `json:"unsigned"`
	Memo      string                  `json:"memo,omitempty"`
	ExpiresAt string                  `json:"expires_at"`
}

const preparedTTL = 15 * time.Minute

// PrepareTransaction implements POST /v1/wallet-groups/:id/transactions/prepare.
//
// Only honored for user-custody wallets — platform-custody groups should
// keep using the existing one-shot SendTransaction (we sign for them).
// For Phase 2, only the Tempo rail is wired; CrossMint user-custody lands
// in Phase 3.
func (h *WalletGroups) PrepareTransaction(c *fiber.Ctx) error {
	tenantID := middleware.TenantID(c)
	if tenantID == "" {
		return badRequest(c, "missing tenant context")
	}
	groupID := c.Params("id")
	if groupID == "" {
		return badRequest(c, "missing group id")
	}

	var req PrepareTransactionRequest
	if err := c.BodyParser(&req); err != nil {
		return badRequest(c, "invalid JSON body")
	}
	if err := validatePrepareTx(req); err != nil {
		return badRequest(c, err.Error())
	}

	ctx := c.UserContext()

	group, err := h.q.GetWalletGroupByID(ctx, groupID)
	if err != nil || group.TenantID != tenantID {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "wallet group not found"})
	}

	// Only Tempo is wired for Phase 2.
	wallet, err := h.q.GetWalletByGroupAndRail(ctx, sqlcgen.GetWalletByGroupAndRailParams{
		GroupID: group.ID,
		Rail:    string(apitypes.RailTempo),
	})
	if err != nil {
		return c.Status(fiber.StatusUnprocessableEntity).JSON(fiber.Map{
			"error": "group has no active tempo wallet",
		})
	}
	if wallet.Custody != "user" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "prepare endpoint is for user-custody wallets only; " +
				"platform-custody wallets use POST .../transactions",
			"code": "wrong_custody_for_prepare",
		})
	}

	// Idempotency replay — if the same key already exists, return the
	// existing prepared row instead of building a new one. Matches the
	// behavior of the platform-custody send path.
	if req.IdempotencyKey != "" {
		existing, lerr := h.q.GetTransactionByIdempotencyKey(ctx, sqlcgen.GetTransactionByIdempotencyKeyParams{
			TenantID:       tenantID,
			IdempotencyKey: pgxText(req.IdempotencyKey),
		})
		if lerr == nil && existing.Status == "prepared" {
			view, verr := preparedRowToView(existing)
			if verr == nil {
				return c.JSON(view)
			}
		}
	}

	// Build the unsigned tx via the Tempo adapter. No private key needed.
	built, err := h.tempo.BuildTransfer(ctx, wallet.Address, apitypes.CreateTransactionRequest{
		From:   wallet.Address,
		To:     req.To,
		Amount: req.Amount,
		Asset:  apitypes.Asset(strings.ToUpper(req.Asset)),
		Chain:  apitypes.ChainTempo,
	})
	if err != nil {
		return upstreamError(c, "tempo", err)
	}

	expiresAt := time.Now().UTC().Add(preparedTTL)
	meta := preparedTxMetadata{
		Unsigned:  built,
		Memo:      req.Memo,
		ExpiresAt: expiresAt.Format(time.RFC3339),
	}
	metaBytes, err := json.Marshal(meta)
	if err != nil {
		return internalError(c, err)
	}

	row, err := h.q.CreateTransaction(ctx, sqlcgen.CreateTransactionParams{
		ID:             id.New(id.PrefixTransaction),
		TenantID:       tenantID,
		WalletID:       wallet.ID,
		GroupID:        group.ID,
		Rail:           string(apitypes.RailTempo),
		Chain:          string(apitypes.ChainTempo),
		Direction:      "outbound",
		Counterparty:   req.To,
		Amount:         req.Amount,
		Asset:          strings.ToUpper(req.Asset),
		Status:         "prepared",
		IdempotencyKey: pgxText(req.IdempotencyKey),
		Metadata:       metaBytes,
	})
	if err != nil {
		return internalError(c, err)
	}

	view, err := preparedRowToView(row)
	if err != nil {
		return internalError(c, err)
	}
	return c.Status(fiber.StatusCreated).JSON(view)
}

// SubmitTransactionRequest is the JSON body of
// POST /v1/wallet-groups/:id/transactions/submit.
type SubmitTransactionRequest struct {
	PrepareID   string `json:"prepare_id"`
	SignedTxHex string `json:"signed_tx_hex"`
}

// SubmitTransaction implements POST /v1/wallet-groups/:id/transactions/submit.
// Loads the prepared row, broadcasts the client-supplied signed bytes via
// the Tempo adapter, and flips the row to 'broadcast' + tx_hash.
func (h *WalletGroups) SubmitTransaction(c *fiber.Ctx) error {
	tenantID := middleware.TenantID(c)
	if tenantID == "" {
		return badRequest(c, "missing tenant context")
	}
	groupID := c.Params("id")
	if groupID == "" {
		return badRequest(c, "missing group id")
	}

	var req SubmitTransactionRequest
	if err := c.BodyParser(&req); err != nil {
		return badRequest(c, "invalid JSON body")
	}
	if req.PrepareID == "" || req.SignedTxHex == "" {
		return badRequest(c, "prepare_id and signed_tx_hex are required")
	}

	ctx := c.UserContext()

	row, err := h.q.GetTransactionByID(ctx, req.PrepareID)
	if err != nil || row.TenantID != tenantID || row.GroupID != groupID {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "prepared transaction not found"})
	}
	if row.Status != "prepared" {
		return c.Status(fiber.StatusConflict).JSON(fiber.Map{
			"error":  fmt.Sprintf("transaction is in status %q; submit only valid for 'prepared'", row.Status),
			"code":   "wrong_status",
			"status": row.Status,
		})
	}

	// TTL: a prepared tx older than preparedTTL is rejected. Stale nonce/
	// gas estimates are the primary risk — better to make the client re-prepare.
	var meta preparedTxMetadata
	if err := json.Unmarshal(row.Metadata, &meta); err == nil && meta.ExpiresAt != "" {
		if t, perr := time.Parse(time.RFC3339, meta.ExpiresAt); perr == nil && time.Now().UTC().After(t) {
			return c.Status(fiber.StatusGone).JSON(fiber.Map{
				"error": "prepared transaction expired; re-prepare to refresh nonce/gas",
				"code":  "prepared_expired",
			})
		}
	}

	hash, err := h.tempo.BroadcastSignedTx(ctx, req.SignedTxHex)
	if err != nil {
		// Mark the row failed so the dashboard sees the attempt.
		_, _ = h.q.UpdateTransactionStatus(ctx, sqlcgen.UpdateTransactionStatusParams{
			ID:     row.ID,
			Status: "failed",
		})
		return upstreamError(c, "tempo", err)
	}

	updated, err := h.q.UpdateTransactionStatus(ctx, sqlcgen.UpdateTransactionStatusParams{
		ID:     row.ID,
		Status: "broadcast",
		TxHash: pgxText(hash),
	})
	if err != nil {
		return internalError(c, err)
	}

	return c.JSON(fiber.Map{
		"id":           updated.ID,
		"wallet_id":    updated.WalletID,
		"group_id":     updated.GroupID,
		"rail":         updated.Rail,
		"asset":        updated.Asset,
		"amount":       updated.Amount,
		"to":           updated.Counterparty,
		"status":       updated.Status,
		"rail_tx_hash": hash,
	})
}

// ──────────────────────────────────────────────────────────────────────
// helpers
// ──────────────────────────────────────────────────────────────────────

func validatePrepareTx(r PrepareTransactionRequest) error {
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

func preparedRowToView(row sqlcgen.Transaction) (PreparedTxView, error) {
	var meta preparedTxMetadata
	if len(row.Metadata) > 0 {
		_ = json.Unmarshal(row.Metadata, &meta)
	}
	v := PreparedTxView{
		PrepareID: row.ID,
		WalletID:  row.WalletID,
		GroupID:   row.GroupID,
		Rail:      row.Rail,
		Asset:     row.Asset,
		Amount:    row.Amount,
		To:        row.Counterparty,
		Unsigned:  meta.Unsigned,
		Status:    row.Status,
		ExpiresAt: meta.ExpiresAt,
	}
	if row.CreatedAt.Valid {
		v.CreatedAt = row.CreatedAt.Time.UTC().Format(time.RFC3339)
	}
	return v, nil
}

// Make sure the local pgxText helper resolves — it lives in the handlers
// package alongside pgxInt2, but Go's package layout means we need the same
// signature available here. The package-private helper from pgx.go already
// satisfies this; this no-op typecheck keeps the linter happy if pgx.go
// drifts.
var _ = func(string) pgtype.Text { return pgxText("") }
