package handlers

import (
	"github.com/gofiber/fiber/v2"

	"github.com/atara-xyz/atara-pay/internal/db/sqlcgen"
	apitypes "github.com/atara-xyz/atara-pay/internal/types"
	"github.com/atara-xyz/atara-pay/internal/server/middleware"
)

// WalletCapabilities describes what API actions are usable for one wallet,
// given its custody mode and rail. Frontends consume this to disable / enable
// buttons; back-end agents consume it to pick the right endpoint.
//
// Boolean semantics: `true` = the corresponding endpoint will accept calls
// for this wallet today. `false` = the gateway returns a 501 (or has no
// route at all) for that combination.
//
// Each false carries a `*Reason` string so the UI can explain *why* without
// guessing. The reason text is stable enough to key i18n strings off — do
// not paraphrase it on each request.
type WalletCapabilities struct {
	WalletID string `json:"wallet_id"`
	Rail     string `json:"rail"`
	Chain    string `json:"chain"`
	Custody  string `json:"custody"`

	CanReadBalance     bool   `json:"can_read_balance"`
	CanReadBalanceWhy  string `json:"can_read_balance_reason,omitempty"`

	CanTransfer        bool   `json:"can_transfer"`        // POST .../transactions (server signs)
	CanTransferWhy     string `json:"can_transfer_reason,omitempty"`

	CanPrepareSubmit   bool   `json:"can_prepare_submit"`  // POST .../transactions/prepare + submit
	CanPrepareSubmitWhy string `json:"can_prepare_submit_reason,omitempty"`

	CanOnramp          bool   `json:"can_onramp"`
	CanOnrampWhy       string `json:"can_onramp_reason,omitempty"`

	CanMintSessionKey  bool   `json:"can_mint_session_key"`
	CanMintSessionKeyWhy string `json:"can_mint_session_key_reason,omitempty"`
}

// GroupCapabilities is the envelope returned from
// GET /v1/wallet-groups/:id/capabilities — one entry per wallet plus the
// group-level custody / owner_type for convenience.
type GroupCapabilities struct {
	GroupID   string               `json:"group_id"`
	OwnerType string               `json:"owner_type"`
	Custody   string               `json:"custody"`
	Wallets   []WalletCapabilities `json:"wallets"`
}

// Capabilities implements GET /v1/wallet-groups/:id/capabilities.
//
// Pure derivation from existing wallet rows; no rail calls. Safe to expose
// to a publishable key once that lands (M15) since it leaks no balances or
// session-key material.
func (h *WalletGroups) Capabilities(c *fiber.Ctx) error {
	tenantID := middleware.TenantID(c)
	if tenantID == "" {
		return badRequest(c, "missing tenant context")
	}
	groupID := c.Params("id")
	if groupID == "" {
		return badRequest(c, "missing id")
	}

	ctx := c.UserContext()
	group, err := h.q.GetWalletGroupByID(ctx, groupID)
	if err != nil || group.TenantID != tenantID {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "wallet group not found"})
	}

	wallets, err := h.q.ListWalletsByGroup(ctx, group.ID)
	if err != nil {
		return internalError(c, err)
	}

	out := GroupCapabilities{
		GroupID:   group.ID,
		OwnerType: group.OwnerType,
		Custody:   group.Custody,
		Wallets:   make([]WalletCapabilities, len(wallets)),
	}
	for i, w := range wallets {
		out.Wallets[i] = capabilitiesFor(w)
	}
	return c.JSON(out)
}

// capabilitiesFor is the source of truth for what each (rail, custody)
// combination can do *right now*. When new endpoints unlock more
// combinations, update only this function.
func capabilitiesFor(w sqlcgen.Wallet) WalletCapabilities {
	c := WalletCapabilities{
		WalletID: w.ID,
		Rail:     w.Rail,
		Chain:    w.Chain,
		Custody:  w.Custody,

		// Reading balance is always allowed — it's just an RPC query against
		// the on-chain address, no key needed.
		CanReadBalance: true,
	}

	switch w.Custody {
	case "platform":
		c.CanTransfer = true
		c.CanOnramp = w.Rail == string(apitypes.RailCrossMint)
		if !c.CanOnramp {
			c.CanOnrampWhy = "onramp is only available on the CrossMint rail today"
		}
		c.CanMintSessionKey = true
		// Platform-custody groups always sign server-side; the prepare/submit
		// flow only makes sense for user-custody. Returning false here keeps
		// the dashboard from rendering a "prepare" button that would 4xx.
		c.CanPrepareSubmit = false
		c.CanPrepareSubmitWhy = "prepare/submit is for user-custody wallets; platform-custody uses .../transactions directly"

	case "user":
		// Atara doesn't hold the key — server-side signing impossible.
		c.CanTransfer = false
		c.CanTransferWhy = "user-custody wallet: server has no key. Use POST .../transactions/prepare + .../submit"

		// Only Tempo wired so far (Phase 2). CrossMint rail still 501s.
		c.CanPrepareSubmit = w.Rail == string(apitypes.RailTempo)
		if !c.CanPrepareSubmit {
			c.CanPrepareSubmitWhy = "user-custody " + w.Rail +
				" rail not yet wired (Phase 4); only tempo supports prepare/submit today"
		}

		// CrossMint linked-external-wallet onramp is Phase 5+.
		c.CanOnramp = false
		c.CanOnrampWhy = "user-custody onramp requires CrossMint linked-external-wallet (Phase 5+)"

		// Phase 4: user-custody session keys land via a two-step flow on
		// Tempo (mint pending_authorize → wallet owner signs authorizeKey →
		// submit-authorize activates). CrossMint user-custody session keys
		// stay 501 until that rail's user-custody story lands too.
		if w.Rail == string(apitypes.RailTempo) {
			c.CanMintSessionKey = true
		} else {
			c.CanMintSessionKey = false
			c.CanMintSessionKeyWhy = "user-custody session keys are tempo-only today; " + w.Rail + " support is Phase 5+"
		}

	case "mpc":
		// MPC support is reserved (M15). All write capabilities false until
		// then so a misconfigured wallet doesn't accidentally let writes
		// through.
		c.CanTransfer = false
		c.CanTransferWhy = "MPC custody not yet supported (M15)"
		c.CanPrepareSubmit = false
		c.CanPrepareSubmitWhy = "MPC custody not yet supported (M15)"
		c.CanOnramp = false
		c.CanOnrampWhy = "MPC custody not yet supported (M15)"
		c.CanMintSessionKey = false
		c.CanMintSessionKeyWhy = "MPC custody not yet supported (M15)"
	}

	return c
}
