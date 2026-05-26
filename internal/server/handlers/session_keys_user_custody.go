package handlers

import (
	"encoding/hex"
	"time"

	"github.com/gofiber/fiber/v2"

	"github.com/atara-xyz/atara-pay/internal/adapters/tempo"
	"github.com/atara-xyz/atara-pay/internal/db/sqlcgen"
	"github.com/atara-xyz/atara-pay/internal/server/middleware"
	"github.com/atara-xyz/atara-pay/internal/sessionkey"
	apitypes "github.com/atara-xyz/atara-pay/internal/types"
	"github.com/atara-xyz/atara-pay/internal/webhooks"
)

// PreparedSessionKeyResponse is what POST /session-keys returns when the
// parent wallet is user-custody. The session keypair has been minted and
// persisted (status='pending_authorize'); the caller must sign
// UnsignedAuthorize.RawUnsignedHex with the WALLET MASTER KEY and send it
// back via POST .../submit-authorize before this key can spend.
//
// PrivateKey is the session-key private bytes — same one-time-return
// semantics as the platform-custody mint flow. The agent runtime stores it;
// Atara only retains the encrypted copy for rotation.
type PreparedSessionKeyResponse struct {
	SessionKeyView
	PrivateKey        string                  `json:"private_key"`
	UnsignedAuthorize *tempo.UnsignedTransfer `json:"unsigned_authorize"`
	Notice            string                  `json:"_notice"`
}

// createUserCustody is the branch of Create() for user-custody parent
// wallets. Mints + persists the session keypair, builds the unsigned
// authorizeKey tx, returns both to the caller. Status='pending_authorize'
// until SubmitAuthorize lands the signed tx.
func (h *SessionKeys) createUserCustody(
	c *fiber.Ctx,
	tenantID string,
	group sqlcgen.WalletGroup,
	wallet sqlcgen.Wallet,
	req CreateSessionKeyRequest,
) error {
	// Limits / expiry / rotation: same parsing as the platform-custody path
	// (reuse the existing helper). On-chain enforce is implicit for the
	// user-custody flow — the authorizeKey tx IS the on-chain enforcement.
	perTx, daily, weekly, monthly, err := parseSessionCaps(req)
	if err != nil {
		return badRequest(c, err.Error())
	}

	var expiresAt time.Time
	if req.ExpiresAt != "" {
		t, perr := time.Parse(time.RFC3339, req.ExpiresAt)
		if perr != nil {
			return badRequest(c, "expires_at must be RFC 3339")
		}
		expiresAt = t
	}

	// User-custody session keys only live on rail=tempo today (CrossMint
	// user-custody is Phase 5+). Defensive check even though the caller
	// already resolved the wallet — keeps the failure mode obvious.
	if wallet.Rail != string(apitypes.RailTempo) {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "user-custody session keys only supported on rail=tempo",
			"code":  "user_custody_session_key_rail_unsupported",
			"rail":  wallet.Rail,
		})
	}

	interval := int32(req.RotationIntervalHours) * 3600
	prepared, err := h.svc.PrepareUserCustody(c.UserContext(), sessionkey.MintInput{
		TenantID: tenantID,
		WalletID: wallet.ID,
		GroupID:  group.ID,
		Name:     req.Name,
		Limits: sessionkey.Limits{
			PerTxUSD:          perTx,
			DailyUSD:          daily,
			WeeklyUSD:         weekly,
			MonthlyUSD:        monthly,
			AllowedRecipients: req.Limits.AllowedRecipients,
			DeniedRecipients:  req.Limits.DeniedRecipients,
		},
		RotationMode:      req.RotationMode,
		RotationIntervalS: interval,
		ExpiresAt:         expiresAt,
		// OnChainEnforce is not toggleable here — user-custody implies
		// on-chain authorization, that's the entire point.
	})
	if err != nil {
		return internalError(c, err)
	}

	row, err := h.q.GetSessionKeyByID(c.UserContext(), prepared.ID)
	if err != nil {
		return internalError(c, err)
	}
	view := toSessionKeyView(row)

	h.publish(c.UserContext(), tenantID, webhooks.EventSessionKeyCreated, view,
		webhooks.ResourceRefs{
			WalletID:     wallet.ID,
			GroupID:      group.ID,
			SessionKeyID: row.ID,
		})

	return c.Status(fiber.StatusCreated).JSON(PreparedSessionKeyResponse{
		SessionKeyView:    view,
		PrivateKey:        hex.EncodeToString(prepared.PrivateKey),
		UnsignedAuthorize: prepared.UnsignedAuthorize,
		Notice: "Session key is pending_authorize. Have the wallet owner sign " +
			"unsigned_authorize.raw_unsigned_hex with the wallet master key, " +
			"then POST .../session-keys/{id}/submit-authorize with the signed bytes.",
	})
}

// SubmitAuthorizeRequest is the JSON body of
// POST /v1/wallet-groups/:id/session-keys/:sk_id/submit-authorize.
type SubmitAuthorizeRequest struct {
	SignedTxHex string `json:"signed_tx_hex"`
}

// SubmitAuthorize broadcasts the wallet-owner-signed authorizeKey tx and
// flips the session key from 'pending_authorize' to 'active'. Returns the
// (now active) session-key view + the on-chain tx hash for the dashboard.
func (h *SessionKeys) SubmitAuthorize(c *fiber.Ctx) error {
	tenantID := middleware.TenantID(c)
	if tenantID == "" {
		return badRequest(c, "missing tenant context")
	}
	groupID := c.Params("id")
	skID := c.Params("sk_id")
	if groupID == "" || skID == "" {
		return badRequest(c, "missing group id or session key id")
	}

	var req SubmitAuthorizeRequest
	if err := c.BodyParser(&req); err != nil {
		return badRequest(c, "invalid JSON body")
	}
	if req.SignedTxHex == "" {
		return badRequest(c, "signed_tx_hex is required")
	}

	ctx := c.UserContext()

	// Cross-tenant guard.
	group, err := h.q.GetWalletGroupByID(ctx, groupID)
	if err != nil || group.TenantID != tenantID {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "wallet group not found"})
	}

	hash, err := h.svc.SubmitAuthorize(ctx, tenantID, skID, req.SignedTxHex)
	if err != nil {
		return upstreamError(c, "tempo", err)
	}

	row, err := h.q.GetSessionKeyByID(ctx, skID)
	if err != nil {
		return internalError(c, err)
	}
	view := toSessionKeyView(row)

	h.publish(ctx, tenantID, webhooks.EventSessionKeyCreated, view,
		webhooks.ResourceRefs{
			WalletID:     row.WalletID,
			GroupID:      row.GroupID,
			SessionKeyID: row.ID,
		})

	return c.JSON(fiber.Map{
		"session_key":  view,
		"rail_tx_hash": hash,
	})
}
