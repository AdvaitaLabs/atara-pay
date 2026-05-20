package handlers

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/gofiber/fiber/v2"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/atara-xyz/atara-pay/internal/adapters/crossmint"
	"github.com/atara-xyz/atara-pay/internal/adapters/tempo"
	"github.com/atara-xyz/atara-pay/internal/db/sqlcgen"
	"github.com/atara-xyz/atara-pay/internal/id"
	"github.com/atara-xyz/atara-pay/internal/keystore"
	"github.com/atara-xyz/atara-pay/internal/limits"
	"github.com/atara-xyz/atara-pay/internal/server/middleware"
	apitypes "github.com/atara-xyz/atara-pay/internal/types"
)

// WalletGroups handles the dual-rail-by-default endpoints.
// A wallet group bundles 1..N rail-specific wallets under a single owner_ref;
// for MVP we always create exactly two wallets per group: CrossMint + Tempo.
type WalletGroups struct {
	pool      *pgxpool.Pool
	q         *sqlcgen.Queries
	crossmint *crossmint.Adapter
	tempo     *tempo.Adapter
	ks        keystore.Keystore
	limits    *limits.Service // optional — nil disables limit enforcement

	// crossMintChainDefault names the CrossMint chain new wallets default
	// to when the request body omits "chain". "base" is cheap, fast, and
	// where Onramp lands.
	crossMintChainDefault string
}

// NewWalletGroups wires the handler set. cm/tp/ks are required; limits is
// optional during the rollout window (passing nil disables limit
// enforcement — useful while the customer hasn't created a policy yet).
func NewWalletGroups(
	pool *pgxpool.Pool,
	cm *crossmint.Adapter,
	tp *tempo.Adapter,
	ks keystore.Keystore,
	limitsSvc *limits.Service,
) *WalletGroups {
	return &WalletGroups{
		pool:                  pool,
		q:                     sqlcgen.New(pool),
		crossmint:             cm,
		tempo:                 tp,
		ks:                    ks,
		limits:                limitsSvc,
		crossMintChainDefault: "base",
	}
}

// ──────────────────────────────────────────────────────────────────────
// Create
// ──────────────────────────────────────────────────────────────────────

// CreateGroupRequest is the JSON body of POST /v1/wallet-groups.
type CreateGroupRequest struct {
	Owner struct {
		Type string `json:"type"` // user|agent|merchant|treasury
		Ref  string `json:"ref"`  // customer's external id
	} `json:"owner"`
	ParentGroupID string `json:"parent_group_id,omitempty"`
	DisplayName   string `json:"display_name,omitempty"`
	// CrossMintChain optionally overrides the default ("base").
	CrossMintChain string `json:"crossmint_chain,omitempty"`
}

// GroupView is the safe-to-return projection of a wallet group plus its
// constituent wallets. The encrypted_private_key column is intentionally
// absent — it never leaves the server.
type GroupView struct {
	ID            string        `json:"id"`
	TenantID      string        `json:"tenant_id"`
	OwnerType     string        `json:"owner_type"`
	OwnerRef      string        `json:"owner_ref"`
	ParentGroupID string        `json:"parent_group_id,omitempty"`
	Custody       string        `json:"custody"`
	DisplayName   string        `json:"display_name,omitempty"`
	Wallets       []WalletView  `json:"wallets"`
	CreatedAt     string        `json:"created_at,omitempty"`
	Reused        bool          `json:"_reused,omitempty"` // idempotency signal
}

// WalletView is the safe projection of a wallets row.
type WalletView struct {
	ID              string `json:"id"`
	Rail            string `json:"rail"`
	Chain           string `json:"chain"`
	Address         string `json:"address"`
	Custody         string `json:"custody"`
	ProviderLocator string `json:"provider_locator,omitempty"`
	Status          string `json:"status"`
}

// Create implements POST /v1/wallet-groups.
//
// Concurrency / idempotency:
//
//  1. Look up the existing group by (tenant_id, owner_ref). If it exists
//     and is not deleted, we return it as-is, no rails called, no rows
//     written. The response carries "_reused": true so clients can tell.
//  2. Otherwise, call CrossMint OUTSIDE any DB transaction. CrossMint is
//     the only external dependency; if it fails we return early and the
//     DB stays untouched.
//  3. Generate the Tempo keypair and encrypt the bytes (pure CPU).
//  4. Open a short DB transaction and insert 1 wallet_group + 2 wallets.
//     If the insert fails after CrossMint succeeded the result is an
//     orphan CrossMint wallet — acceptable for MVP. A sweeper can
//     reconcile by externalUserId later.
//
// Two requests racing past step 1 may both create CrossMint wallets and
// only one will win the wallet_groups UNIQUE (tenant_id, owner_ref) insert.
// The winner returns 201; the loser sees the unique violation and returns
// the already-existing group with reused=true. The orphan CrossMint wallet
// is again something a sweeper resolves.
func (h *WalletGroups) Create(c *fiber.Ctx) error {
	tenantID := middleware.TenantID(c)
	if tenantID == "" {
		return badRequest(c, "missing tenant context")
	}

	var req CreateGroupRequest
	if err := c.BodyParser(&req); err != nil {
		return badRequest(c, "invalid JSON body")
	}
	req.Owner.Ref = strings.TrimSpace(req.Owner.Ref)
	if err := validateCreateGroup(req); err != nil {
		return badRequest(c, err.Error())
	}

	ctx := c.UserContext()

	// Step 1: idempotency lookup.
	if existing, err := h.q.GetWalletGroupByOwnerRef(ctx, sqlcgen.GetWalletGroupByOwnerRefParams{
		TenantID: tenantID,
		OwnerRef: req.Owner.Ref,
	}); err == nil {
		wallets, werr := h.q.ListWalletsByGroup(ctx, existing.ID)
		if werr != nil {
			return internalError(c, werr)
		}
		return c.JSON(toGroupViewWithReuse(existing, wallets, true))
	}

	// Step 2: create CrossMint wallet via REST (outside DB tx).
	cmChain := req.CrossMintChain
	if cmChain == "" {
		cmChain = h.crossMintChainDefault
	}
	cmWallet, err := h.crossmint.CreateWallet(ctx, apitypes.CreateWalletRequest{
		Rail:  apitypes.RailCrossMint,
		Chain: apitypes.Chain(cmChain),
		Owner: apitypes.Owner{
			Type:  "external",
			Value: fmt.Sprintf("atara:%s:%s", tenantID, req.Owner.Ref),
		},
		Type: "smart",
	})
	if err != nil {
		return upstreamError(c, "crossmint", err)
	}

	// Step 3: generate Tempo keypair, encrypt with the keystore.
	priv, tempoAddr, err := tempo.GenerateKeypair()
	if err != nil {
		return internalError(c, err)
	}
	encBlob, keyVer, err := h.ks.Encrypt(priv[:])
	if err != nil {
		return internalError(c, err)
	}

	// Step 4: insert group + 2 wallets in a transaction.
	tx, err := h.pool.Begin(ctx)
	if err != nil {
		return internalError(c, err)
	}
	defer tx.Rollback(context.Background())
	qtx := h.q.WithTx(tx)

	groupID := id.New(id.PrefixWalletGroup)
	custody := "platform"

	group, err := qtx.CreateWalletGroup(ctx, sqlcgen.CreateWalletGroupParams{
		ID:            groupID,
		TenantID:      tenantID,
		OwnerType:     req.Owner.Type,
		OwnerRef:      req.Owner.Ref,
		ParentGroupID: pgxText(req.ParentGroupID),
		Custody:       custody,
		DisplayName:   pgxText(req.DisplayName),
		Metadata:      []byte("{}"),
	})
	if err != nil {
		if isUniqueViolation(err) {
			// Race: another request created the group between Step 1 and now.
			// Roll back, look up the winner, and return it. The CrossMint
			// wallet we just created is orphaned (no DB row) — sweeper job.
			tx.Rollback(context.Background())
			winner, lerr := h.q.GetWalletGroupByOwnerRef(ctx, sqlcgen.GetWalletGroupByOwnerRefParams{
				TenantID: tenantID, OwnerRef: req.Owner.Ref,
			})
			if lerr != nil {
				return internalError(c, lerr)
			}
			wallets, werr := h.q.ListWalletsByGroup(ctx, winner.ID)
			if werr != nil {
				return internalError(c, werr)
			}
			return c.JSON(toGroupViewWithReuse(winner, wallets, true))
		}
		return internalError(c, err)
	}

	cmRow, err := qtx.CreateWallet(ctx, sqlcgen.CreateWalletParams{
		ID:              id.New(id.PrefixWallet),
		GroupID:         group.ID,
		TenantID:        tenantID,
		Rail:            string(apitypes.RailCrossMint),
		Chain:           cmChain,
		Address:         cmWallet.Address,
		ProviderLocator: cmWallet.Locator,
		Custody:         custody,
		// EncryptedPrivateKey + KeyVersion stay null — CrossMint holds the
		// key on their side. The wallets CHECK constraint requires this.
		Status:   "active",
		Metadata: []byte("{}"),
	})
	if err != nil {
		return internalError(c, err)
	}

	tpRow, err := qtx.CreateWallet(ctx, sqlcgen.CreateWalletParams{
		ID:                  id.New(id.PrefixWallet),
		GroupID:             group.ID,
		TenantID:            tenantID,
		Rail:                string(apitypes.RailTempo),
		Chain:               string(apitypes.ChainTempo),
		Address:             tempoAddr,
		ProviderLocator:     tempoAddr, // Tempo locator IS the address
		Custody:             custody,
		EncryptedPrivateKey: encBlob,
		KeyVersion:          pgxInt2(int16(keyVer)),
		Status:              "active",
		Metadata:            []byte("{}"),
	})
	if err != nil {
		return internalError(c, err)
	}

	if err := tx.Commit(ctx); err != nil {
		return internalError(c, err)
	}

	view := toGroupView(group, []sqlcgen.Wallet{cmRow, tpRow})
	return c.Status(fiber.StatusCreated).JSON(view)
}

// ──────────────────────────────────────────────────────────────────────
// Read endpoints
// ──────────────────────────────────────────────────────────────────────

// Get implements GET /v1/wallet-groups/:id. Returns the group and its
// wallets (CrossMint + Tempo + any future rails), tenant-scoped — a leaked
// group id from one tenant cannot read another tenant's data.
func (h *WalletGroups) Get(c *fiber.Ctx) error {
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
		// Same 404 either way so cross-tenant probes can't tell whether
		// the id exists in some other tenant.
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "wallet group not found"})
	}

	wallets, err := h.q.ListWalletsByGroup(ctx, group.ID)
	if err != nil {
		return internalError(c, err)
	}

	return c.JSON(toGroupView(group, wallets))
}

// ListGroupsResponse wraps the page under a "data" field, leaving room to
// add pagination metadata later without breaking clients.
type ListGroupsResponse struct {
	Data []GroupView `json:"data"`
}

// List implements GET /v1/wallet-groups. Returns the tenant's groups in
// reverse-chronological order; supports ?limit (default 50, max 200) and
// ?offset cursor-less paging until M9 puts in a proper one.
func (h *WalletGroups) List(c *fiber.Ctx) error {
	tenantID := middleware.TenantID(c)
	if tenantID == "" {
		return badRequest(c, "missing tenant context")
	}

	limit := c.QueryInt("limit", 50)
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	offset := c.QueryInt("offset", 0)
	if offset < 0 {
		offset = 0
	}

	ctx := c.UserContext()
	groups, err := h.q.ListWalletGroupsInTenant(ctx, sqlcgen.ListWalletGroupsInTenantParams{
		TenantID: tenantID,
		Limit:    int32(limit),
		Offset:   int32(offset),
	})
	if err != nil {
		return internalError(c, err)
	}

	// N+1 by design for now — wallet lists are tiny (1–3 rows per group).
	// Once we have 5+ rails or millions of groups, swap for a single
	// JOIN query with array_agg.
	views := make([]GroupView, len(groups))
	for i, g := range groups {
		wallets, werr := h.q.ListWalletsByGroup(ctx, g.ID)
		if werr != nil {
			return internalError(c, werr)
		}
		views[i] = toGroupView(g, wallets)
	}
	return c.JSON(ListGroupsResponse{Data: views})
}

// ──────────────────────────────────────────────────────────────────────
// helpers (file-local)
// ──────────────────────────────────────────────────────────────────────

func validateCreateGroup(r CreateGroupRequest) error {
	switch r.Owner.Type {
	case "user", "agent", "merchant", "treasury":
	default:
		return errors.New(`owner.type must be one of "user", "agent", "merchant", "treasury"`)
	}
	if r.Owner.Ref == "" {
		return errors.New("owner.ref is required")
	}
	if len(r.Owner.Ref) > 200 {
		return errors.New("owner.ref too long")
	}
	if r.Owner.Type == "agent" && r.ParentGroupID == "" {
		return errors.New("parent_group_id is required when owner.type is 'agent'")
	}
	return nil
}

func toGroupView(g sqlcgen.WalletGroup, wallets []sqlcgen.Wallet) GroupView {
	return toGroupViewWithReuse(g, wallets, false)
}

func toGroupViewWithReuse(g sqlcgen.WalletGroup, wallets []sqlcgen.Wallet, reused bool) GroupView {
	v := GroupView{
		ID:        g.ID,
		TenantID:  g.TenantID,
		OwnerType: g.OwnerType,
		OwnerRef:  g.OwnerRef,
		Custody:   g.Custody,
		Wallets:   make([]WalletView, len(wallets)),
		Reused:    reused,
	}
	if g.ParentGroupID.Valid {
		v.ParentGroupID = g.ParentGroupID.String
	}
	if g.DisplayName.Valid {
		v.DisplayName = g.DisplayName.String
	}
	if g.CreatedAt.Valid {
		v.CreatedAt = g.CreatedAt.Time.UTC().Format("2006-01-02T15:04:05Z")
	}
	for i, w := range wallets {
		v.Wallets[i] = WalletView{
			ID:              w.ID,
			Rail:            w.Rail,
			Chain:           w.Chain,
			Address:         w.Address,
			Custody:         w.Custody,
			ProviderLocator: w.ProviderLocator,
			Status:          w.Status,
		}
	}
	return v
}

func upstreamError(c *fiber.Ctx, rail string, err error) error {
	return c.Status(fiber.StatusBadGateway).JSON(fiber.Map{
		"error": fmt.Sprintf("%s rail returned an error", rail),
		"rail":  rail,
		"cause": err.Error(),
	})
}
