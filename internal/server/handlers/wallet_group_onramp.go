package handlers

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"

	"github.com/atara-xyz/atara-pay/internal/db/sqlcgen"
	"github.com/atara-xyz/atara-pay/internal/id"
	"github.com/atara-xyz/atara-pay/internal/server/middleware"
	apitypes "github.com/atara-xyz/atara-pay/internal/types"
	"github.com/atara-xyz/atara-pay/internal/webhooks"
)

// CreateOnrampRequest is the JSON body of POST /v1/wallet-groups/:id/onramp.
type CreateOnrampRequest struct {
	Fiat struct {
		Amount   string `json:"amount"`
		Currency string `json:"currency"`
	} `json:"fiat"`
	Asset          string `json:"asset"`
	Chain          string `json:"chain,omitempty"`
	IdempotencyKey string `json:"idempotency_key,omitempty"`
}

// OnrampOrderView is the safe-to-return projection of an onramp_orders row.
type OnrampOrderView struct {
	ID              string `json:"id"`
	Status          string `json:"status"`
	Provider        string `json:"provider"`
	WalletID        string `json:"wallet_id"`
	FiatAmount      string `json:"fiat_amount"`
	FiatCurrency    string `json:"fiat_currency"`
	Asset           string `json:"asset"`
	Chain           string `json:"chain"`
	CheckoutURL     string `json:"checkout_url,omitempty"`
	ProviderOrderID string `json:"provider_order_id,omitempty"`
	CreatedAt       string `json:"created_at,omitempty"`
	ExpiresAt       string `json:"expires_at,omitempty"`
}

// CreateOnramp implements POST /v1/wallet-groups/:id/onramp.
//
// Routing rule: onramp is always served by CrossMint. Tempo has no fiat
// rail, and the legacy /v1/onramp/orders endpoint was bypassed by the
// dual-rail decision. If the group has no active CrossMint wallet we
// return 422 — typically because the group was created before CrossMint
// was registered, or in tests.
func (h *WalletGroups) CreateOnramp(c *fiber.Ctx) error {
	tenantID := middleware.TenantID(c)
	if tenantID == "" {
		return badRequest(c, "missing tenant context")
	}
	groupID := c.Params("id")
	if groupID == "" {
		return badRequest(c, "missing group id")
	}

	var req CreateOnrampRequest
	if err := c.BodyParser(&req); err != nil {
		return badRequest(c, "invalid JSON body")
	}
	if err := validateOnramp(req); err != nil {
		return badRequest(c, err.Error())
	}

	ctx := c.UserContext()

	// Group lookup with cross-tenant guard.
	group, err := h.q.GetWalletGroupByID(ctx, groupID)
	if err != nil || group.TenantID != tenantID {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "wallet group not found"})
	}

	// Onramp always goes through the CrossMint wallet — the only rail with
	// a fiat ramp today.
	// Onramp goes through CrossMint, which is the only rail with a fiat
	// ramp today. When the gateway isn't configured with a CrossMint
	// adapter at all (Tempo-only deploys), fail cleanly rather than nil-
	// dereferencing further down.
	if h.crossmint == nil {
		return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{
			"error": "onramp is not enabled on this gateway (CrossMint adapter not configured)",
			"code":  "onramp_not_configured",
		})
	}

	wallet, err := h.q.GetWalletByGroupAndRail(ctx, sqlcgen.GetWalletByGroupAndRailParams{
		GroupID: group.ID,
		Rail:    string(apitypes.RailCrossMint),
	})
	if err != nil {
		return c.Status(fiber.StatusUnprocessableEntity).JSON(fiber.Map{
			"error": "group has no active CrossMint wallet (onramp unavailable)",
		})
	}

	// User-custody onramp needs a CrossMint "linked external wallet" flow
	// we haven't built yet (M14). For now, only platform-custody onramps go
	// through.
	if wallet.Custody == "user" {
		return c.Status(fiber.StatusNotImplemented).JSON(fiber.Map{
			"error":   "user-custody wallet: onramp into external address not yet supported",
			"code":    "user_custody_onramp_unsupported",
			"next":    "M14 will add CrossMint linked-external-wallet onramp",
			"wallet":  wallet.ID,
			"custody": wallet.Custody,
		})
	}

	// Default the chain to the wallet's chain if the request didn't pin one.
	chain := req.Chain
	if chain == "" {
		chain = wallet.Chain
	}

	order, err := h.crossmint.CreateOnrampOrder(ctx, apitypes.CreateOnrampRequest{
		WalletID: wallet.ID,
		Fiat: apitypes.FiatAmount{
			Amount:   req.Fiat.Amount,
			Currency: strings.ToUpper(req.Fiat.Currency),
		},
		Asset: apitypes.Asset(req.Asset),
		Chain: apitypes.Chain(chain),
	}, wallet.Address)
	if err != nil {
		return upstreamError(c, "crossmint", err)
	}

	// Persist audit row.
	row, err := h.recordOnramp(ctx, tenantID, middleware.APIKeyID(c), group, wallet, req, chain, order)
	if err != nil {
		// Same pattern as SendTransaction: rail call succeeded, DB insert
		// failed. Surface the checkout URL so the customer can still
		// continue the flow and we can reconcile by provider_order_id later.
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error":             "internal error recording onramp order (provider call succeeded)",
			"provider":          "crossmint",
			"provider_order_id": order.ID,
			"checkout_url":      order.CheckoutURL,
			"persist_error":     err.Error(),
		})
	}
	view := toOnrampView(row)
	// We emit on order CREATION (not completion) — the CrossMint callback
	// loop that drives the order to "completed" is wired in a later
	// sprint. Customers still find this useful: they get a webhook the
	// moment the hosted-checkout URL is live, can email it to the user,
	// etc.
	h.publish(ctx, tenantID, "onramp.created", view,
		webhooks.ResourceRefs{
			WalletID:      wallet.ID,
			GroupID:       group.ID,
			OnrampOrderID: row.ID,
		})
	return c.Status(fiber.StatusCreated).JSON(view)
}

// ──────────────────────────────────────────────────────────────────────
// persistence
// ──────────────────────────────────────────────────────────────────────

func (h *WalletGroups) recordOnramp(
	ctx context.Context,
	tenantID, apiKeyID string,
	g sqlcgen.WalletGroup,
	w sqlcgen.Wallet,
	req CreateOnrampRequest,
	chain string,
	order *apitypes.OnrampOrder,
) (sqlcgen.OnrampOrder, error) {
	// CrossMint hosted checkout URLs expire ~30 minutes after creation. We
	// stamp expires_at at insert so sweeps can clean stale pending orders.
	const checkoutTTL = 30 * time.Minute
	expiresAt := time.Now().UTC().Add(checkoutTTL)

	return h.q.CreateOnrampOrder(ctx, sqlcgen.CreateOnrampOrderParams{
		ID:                id.New(id.PrefixOnrampOrder),
		TenantID:          tenantID,
		WalletID:          w.ID,
		GroupID:           g.ID,
		Provider:          "crossmint",
		ProviderOrderID:   pgxText(order.ID),
		CheckoutUrl:       pgxText(order.CheckoutURL),
		FiatAmount:        req.Fiat.Amount,
		FiatCurrency:      strings.ToUpper(req.Fiat.Currency),
		Asset:             req.Asset,
		Chain:             chain,
		Status:            mapOnrampStatus(order.Status),
		InitiatedByApikey: pgxText(apiKeyID),
		IdempotencyKey:    pgxText(req.IdempotencyKey),
		ExpiresAt:         pgxTimestamptz(expiresAt),
		Metadata:          []byte("{}"),
	})
}

// mapOnrampStatus normalizes the provider's phase string to our state
// machine. CrossMint returns phases like "checkout-ready"; we coerce to
// the closest atara_pay status enum value. Unknown phases land at
// "pending" — defensive default.
func mapOnrampStatus(provider string) string {
	switch strings.ToLower(strings.ReplaceAll(provider, "-", "_")) {
	case "checkout_ready", "awaiting_payment":
		return "checkout_opened"
	case "paid":
		return "paid"
	case "delivering":
		return "delivering"
	case "completed":
		return "completed"
	case "failed":
		return "failed"
	case "expired":
		return "expired"
	default:
		return "pending"
	}
}

// ──────────────────────────────────────────────────────────────────────
// helpers
// ──────────────────────────────────────────────────────────────────────

func validateOnramp(r CreateOnrampRequest) error {
	if r.Fiat.Amount == "" {
		return errors.New("fiat.amount is required")
	}
	if r.Fiat.Currency == "" {
		return errors.New("fiat.currency is required")
	}
	if r.Asset == "" {
		return errors.New("asset is required")
	}
	return nil
}

func toOnrampView(o sqlcgen.OnrampOrder) OnrampOrderView {
	v := OnrampOrderView{
		ID:           o.ID,
		Status:       o.Status,
		Provider:     o.Provider,
		WalletID:     o.WalletID,
		FiatAmount:   o.FiatAmount,
		FiatCurrency: o.FiatCurrency,
		Asset:        o.Asset,
		Chain:        o.Chain,
	}
	if o.CheckoutUrl.Valid {
		v.CheckoutURL = o.CheckoutUrl.String
	}
	if o.ProviderOrderID.Valid {
		v.ProviderOrderID = o.ProviderOrderID.String
	}
	if o.CreatedAt.Valid {
		v.CreatedAt = o.CreatedAt.Time.UTC().Format("2006-01-02T15:04:05Z")
	}
	if o.ExpiresAt.Valid {
		v.ExpiresAt = o.ExpiresAt.Time.UTC().Format("2006-01-02T15:04:05Z")
	}
	return v
}
