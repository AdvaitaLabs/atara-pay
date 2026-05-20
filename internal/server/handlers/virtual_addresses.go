package handlers

import (
	"errors"
	"fmt"
	"strings"

	"github.com/ethereum/go-ethereum/common"
	"github.com/gofiber/fiber/v2"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/atara-xyz/atara-pay/internal/adapters/tempo"
	"github.com/atara-xyz/atara-pay/internal/db/sqlcgen"
	"github.com/atara-xyz/atara-pay/internal/id"
	"github.com/atara-xyz/atara-pay/internal/keystore"
	"github.com/atara-xyz/atara-pay/internal/server/middleware"
	apitypes "github.com/atara-xyz/atara-pay/internal/types"
)

// VirtualAddresses serves /v1/wallet-groups/{id}/virtual-addresses.
// The handler needs a Tempo adapter (for the precompile call) and the
// keystore (to decrypt the parent wallet's master key just before
// signing).
type VirtualAddresses struct {
	pool  *pgxpool.Pool
	q     *sqlcgen.Queries
	tempo *tempo.Adapter
	ks    keystore.Keystore
}

func NewVirtualAddresses(pool *pgxpool.Pool, tp *tempo.Adapter, ks keystore.Keystore) *VirtualAddresses {
	return &VirtualAddresses{pool: pool, q: sqlcgen.New(pool), tempo: tp, ks: ks}
}

// VirtualAddressView is the safe projection.
type VirtualAddressView struct {
	ID                 string `json:"id"`
	WalletID           string `json:"wallet_id"`
	GroupID            string `json:"group_id"`
	Label              string `json:"label"`
	Address            string `json:"address"`
	RegistrationTxHash string `json:"registration_tx_hash,omitempty"`
	CreatedAt          string `json:"created_at,omitempty"`
}

type CreateVirtualAddressRequest struct {
	Label string `json:"label"`
}

type ListVirtualAddressesResponse struct {
	Data []VirtualAddressView `json:"data"`
}

// Create implements POST /v1/wallet-groups/{id}/virtual-addresses.
//
// Idempotent on (wallet_id, label): repeating the request returns the
// existing row without re-broadcasting the registration tx. This matches
// the TIP-1022 precompile's own determinism — same inputs always map to
// the same address.
func (h *VirtualAddresses) Create(c *fiber.Ctx) error {
	tenantID := middleware.TenantID(c)
	if tenantID == "" {
		return badRequest(c, "missing tenant context")
	}
	groupID := c.Params("id")
	if groupID == "" {
		return badRequest(c, "missing group id")
	}

	var req CreateVirtualAddressRequest
	if err := c.BodyParser(&req); err != nil {
		return badRequest(c, "invalid JSON body")
	}
	if err := validateLabel(req.Label); err != nil {
		return badRequest(c, err.Error())
	}

	ctx := c.UserContext()

	// Cross-tenant guarded group + Tempo wallet lookup.
	group, err := h.q.GetWalletGroupByID(ctx, groupID)
	if err != nil || group.TenantID != tenantID {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "wallet group not found"})
	}
	wallet, err := h.q.GetWalletByGroupAndRail(ctx, sqlcgen.GetWalletByGroupAndRailParams{
		GroupID: group.ID,
		Rail:    string(apitypes.RailTempo),
	})
	if err != nil {
		return c.Status(fiber.StatusUnprocessableEntity).JSON(fiber.Map{
			"error": "group has no active Tempo wallet (virtual addresses are Tempo-only today)",
		})
	}

	// Idempotency: existing label returns the same row, no chain call.
	if existing, err := h.q.GetVirtualAddressByLabel(ctx,
		sqlcgen.GetVirtualAddressByLabelParams{WalletID: wallet.ID, Label: req.Label},
	); err == nil {
		return c.JSON(toVirtualAddressView(existing))
	}

	// Need the parent wallet's master key to sign the precompile call.
	if len(wallet.EncryptedPrivateKey) == 0 || !wallet.KeyVersion.Valid {
		return internalError(c, errors.New("tempo wallet has no encrypted master key"))
	}
	master, err := h.ks.Decrypt(wallet.EncryptedPrivateKey, wallet.KeyVersion.Int16)
	if err != nil {
		return internalError(c, fmt.Errorf("decrypt master: %w", err))
	}
	defer func() {
		for i := range master {
			master[i] = 0
		}
	}()

	labelBytes := tempo.LabelToBytes32(req.Label)
	txHash, vaddr, err := h.tempo.RegisterVirtualAddress(ctx, tempo.RegisterVirtualAddressInput{
		ParentPrivKey: master,
		ParentAddress: wallet.Address,
		Label:         labelBytes,
	})
	if err != nil {
		return upstreamError(c, "tempo", err)
	}

	row, err := h.q.CreateVirtualAddress(ctx, sqlcgen.CreateVirtualAddressParams{
		ID:                 id.New("vaddr"),
		TenantID:           tenantID,
		WalletID:           wallet.ID,
		GroupID:            group.ID,
		Label:              req.Label,
		Address:            vaddr.Hex(),
		RegistrationTxHash: pgxText(txHash),
		Metadata:           []byte("{}"),
	})
	if err != nil {
		// Rail succeeded but DB INSERT failed — surface so the customer
		// can reconcile via the on-chain address.
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error":             "internal error recording virtual address (chain registration succeeded)",
			"address":           vaddr.Hex(),
			"registration_tx":   txHash,
			"persist_error":     err.Error(),
		})
	}
	return c.Status(fiber.StatusCreated).JSON(toVirtualAddressView(row))
}

// List implements GET /v1/wallet-groups/{id}/virtual-addresses.
func (h *VirtualAddresses) List(c *fiber.Ctx) error {
	tenantID := middleware.TenantID(c)
	if tenantID == "" {
		return badRequest(c, "missing tenant context")
	}
	groupID := c.Params("id")
	ctx := c.UserContext()

	group, err := h.q.GetWalletGroupByID(ctx, groupID)
	if err != nil || group.TenantID != tenantID {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "wallet group not found"})
	}
	wallet, err := h.q.GetWalletByGroupAndRail(ctx, sqlcgen.GetWalletByGroupAndRailParams{
		GroupID: group.ID, Rail: string(apitypes.RailTempo),
	})
	if err != nil {
		return c.JSON(ListVirtualAddressesResponse{Data: []VirtualAddressView{}})
	}

	limit := c.QueryInt("limit", 50)
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	offset := c.QueryInt("offset", 0)
	if offset < 0 {
		offset = 0
	}
	rows, err := h.q.ListVirtualAddressesForWallet(ctx,
		sqlcgen.ListVirtualAddressesForWalletParams{
			WalletID: wallet.ID,
			Limit:    int32(limit),
			Offset:   int32(offset),
		})
	if err != nil {
		return internalError(c, err)
	}
	out := make([]VirtualAddressView, len(rows))
	for i, r := range rows {
		out[i] = toVirtualAddressView(r)
	}
	return c.JSON(ListVirtualAddressesResponse{Data: out})
}

// ──────────────────────────────────────────────────────────────────────
// helpers
// ──────────────────────────────────────────────────────────────────────

func validateLabel(s string) error {
	s = strings.TrimSpace(s)
	if s == "" {
		return errors.New("label is required")
	}
	if len(s) > 200 {
		return errors.New("label too long (max 200 chars)")
	}
	return nil
}

func toVirtualAddressView(r sqlcgen.VirtualAddress) VirtualAddressView {
	v := VirtualAddressView{
		ID:       r.ID,
		WalletID: r.WalletID,
		GroupID:  r.GroupID,
		Label:    r.Label,
		Address:  r.Address,
	}
	if r.RegistrationTxHash.Valid {
		v.RegistrationTxHash = r.RegistrationTxHash.String
	}
	if r.CreatedAt.Valid {
		v.CreatedAt = r.CreatedAt.Time.UTC().Format("2006-01-02T15:04:05Z")
	}
	return v
}

// Keep the common import live so future address-comparison helpers can
// land here without a churn diff. (go vet ignores blank refs.)
var _ = common.HexToAddress
