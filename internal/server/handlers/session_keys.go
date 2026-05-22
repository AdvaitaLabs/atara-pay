package handlers

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/atara-xyz/atara-pay/internal/db/sqlcgen"
	"github.com/atara-xyz/atara-pay/internal/server/middleware"
	"github.com/atara-xyz/atara-pay/internal/sessionkey"
	apitypes "github.com/atara-xyz/atara-pay/internal/types"
	"github.com/atara-xyz/atara-pay/internal/webhooks"
)

// SessionKeys handles /v1/wallet-groups/:id/session-keys endpoints.
type SessionKeys struct {
	pool      *pgxpool.Pool
	q         *sqlcgen.Queries
	svc       *sessionkey.Service
	publisher *webhooks.Publisher // optional
}

func NewSessionKeys(pool *pgxpool.Pool, svc *sessionkey.Service, pub *webhooks.Publisher) *SessionKeys {
	return &SessionKeys{pool: pool, q: sqlcgen.New(pool), svc: svc, publisher: pub}
}

func (h *SessionKeys) publish(
	ctx context.Context, tenantID, eventType string, data any, refs webhooks.ResourceRefs,
) {
	if h.publisher == nil {
		return
	}
	_, _ = h.publisher.Publish(ctx, tenantID, eventType, data, refs)
}

// ──────────────────────────────────────────────────────────────────────
// Views
// ──────────────────────────────────────────────────────────────────────

// SessionKeyView is the safe-to-return projection of a session_keys row.
// encrypted_priv_key NEVER appears here — it stays on the server.
type SessionKeyView struct {
	ID              string `json:"id"`
	Name            string `json:"name"`
	WalletID        string `json:"wallet_id"`
	GroupID         string `json:"group_id"`
	PublicAddress   string `json:"public_address"`
	PolicyID        string `json:"policy_id,omitempty"`
	Status          string `json:"status"`
	RotationMode    string `json:"rotation_mode"`
	RotationIntervalS int32 `json:"rotation_interval_s"`
	ExpiresAt       string `json:"expires_at,omitempty"`
	NextRotationAt  string `json:"next_rotation_at,omitempty"`
	RailNative      bool   `json:"rail_native"`
	CreatedAt       string `json:"created_at,omitempty"`
	RevokedAt       string `json:"revoked_at,omitempty"`
}

// ──────────────────────────────────────────────────────────────────────
// Create
// ──────────────────────────────────────────────────────────────────────

// CreateSessionKeyRequest is the JSON body of
// POST /v1/wallet-groups/:id/session-keys.
type CreateSessionKeyRequest struct {
	Name string `json:"name"`

	// Rail selects which wallet in the group anchors the session key.
	// Defaults to "tempo" — the AI-agent use case is the primary one.
	Rail string `json:"rail,omitempty"`

	Limits struct {
		PerTxAmount       string   `json:"per_tx_amount"`
		DailyAmount       string   `json:"daily_amount"`
		WeeklyAmount      string   `json:"weekly_amount"`
		MonthlyAmount     string   `json:"monthly_amount"`
		AllowedRecipients []string `json:"allowed_recipients,omitempty"`
		DeniedRecipients  []string `json:"denied_recipients,omitempty"`
	} `json:"limits"`

	RotationMode          string `json:"rotation_mode,omitempty"`
	RotationIntervalHours int    `json:"rotation_interval_hours,omitempty"`
	ExpiresAt             string `json:"expires_at,omitempty"` // RFC 3339

	// OnChainEnforce, when true, broadcasts an authorizeKey() call to the
	// Tempo AccountKeychain precompile so the daily cap becomes
	// chain-enforced in addition to gateway-enforced. Requires rail=tempo.
	// Customers paying for the on-chain belt-and-suspenders flip this to
	// true; everyone else stays at gateway-only (the default).
	OnChainEnforce bool `json:"on_chain_enforce,omitempty"`
}

// CreateSessionKeyResponse returns SessionKeyView PLUS the raw private key.
// Customers store private_key immediately — it's only shown ONCE.
type CreateSessionKeyResponse struct {
	SessionKeyView
	PrivateKey string `json:"private_key"` // 64-char hex of secp256k1 raw bytes
	Notice     string `json:"_notice"`
}

// Create implements POST /v1/wallet-groups/:id/session-keys.
//
// Flow:
//
//	1. Cross-tenant guarded group lookup.
//	2. Pick the wallet on body.rail (default tempo). 422 if the group
//	   doesn't have one.
//	3. Hand the inputs to sessionkey.Service.Mint(), which atomically
//	   creates a dedicated limit_policy + session_key inside a single tx.
//	4. Return the raw private key + view. The plaintext key bytes are
//	   zeroed by the service before this point; the buffer we return
//	   is a fresh copy that escapes to JSON.
func (h *SessionKeys) Create(c *fiber.Ctx) error {
	tenantID := middleware.TenantID(c)
	if tenantID == "" {
		return badRequest(c, "missing tenant context")
	}
	groupID := c.Params("id")
	if groupID == "" {
		return badRequest(c, "missing group id")
	}

	// Role gate when authenticated via session JWT.
	if role := middleware.Role(c); role != "" && role != "owner" && role != "admin" {
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{
			"error": "your role cannot create session keys",
		})
	}

	var req CreateSessionKeyRequest
	if err := c.BodyParser(&req); err != nil {
		return badRequest(c, "invalid JSON body")
	}
	if err := validateCreateSessionKey(req); err != nil {
		return badRequest(c, err.Error())
	}

	ctx := c.UserContext()

	group, err := h.q.GetWalletGroupByID(ctx, groupID)
	if err != nil || group.TenantID != tenantID {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{
			"error": "wallet group not found",
		})
	}

	// Resolve rail → wallet. Default is Tempo (AI-agent use case).
	rail := req.Rail
	if rail == "" {
		rail = string(apitypes.RailTempo)
	}
	wallet, err := h.q.GetWalletByGroupAndRail(ctx, sqlcgen.GetWalletByGroupAndRailParams{
		GroupID: group.ID,
		Rail:    rail,
	})
	if err != nil {
		return c.Status(fiber.StatusUnprocessableEntity).JSON(fiber.Map{
			"error": fmt.Sprintf("group has no active %s wallet", rail),
		})
	}

	// Session keys require Atara to mint and encrypt a fresh keypair that
	// authorizes spends from the parent wallet — that only makes sense when
	// the parent is platform-custody. For user-custody, the caller would
	// authorize a sub-key themselves on-chain. Reject cleanly until the
	// on-chain authorizeKey path is wired up for external wallets (M14).
	if wallet.Custody == "user" {
		return c.Status(fiber.StatusNotImplemented).JSON(fiber.Map{
			"error":   "user-custody wallet: session keys must be authorized on-chain by the wallet owner",
			"code":    "user_custody_session_key_unsupported",
			"next":    "M14 will add a flow to register externally-signed session keys",
			"wallet":  wallet.ID,
			"custody": wallet.Custody,
		})
	}

	// Parse cap fields (integer USD for MVP — matches the limit handlers).
	perTx, daily, weekly, monthly, err := parseSessionCaps(req)
	if err != nil {
		return badRequest(c, err.Error())
	}

	// Parse expires_at (optional).
	var expiresAt time.Time
	if req.ExpiresAt != "" {
		t, perr := time.Parse(time.RFC3339, req.ExpiresAt)
		if perr != nil {
			return badRequest(c, "expires_at must be RFC 3339")
		}
		expiresAt = t
	}

	// On-chain enforce requires rail=tempo. Reject cleanly so we don't
	// burn an authorizeKey attempt against the wrong rail.
	if req.OnChainEnforce && rail != string(apitypes.RailTempo) {
		return badRequest(c, "on_chain_enforce requires rail=tempo")
	}

	interval := int32(req.RotationIntervalHours) * 3600
	minted, err := h.svc.Mint(ctx, sessionkey.MintInput{
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
		OnChainEnforce:    req.OnChainEnforce,
	})
	if err != nil {
		return internalError(c, err)
	}

	// Re-read the row to build the safe view (so dashboards see consistent
	// fields the moment the response lands).
	row, err := h.q.GetSessionKeyByID(ctx, minted.ID)
	if err != nil {
		return internalError(c, err)
	}

	view := toSessionKeyView(row)
	h.publish(ctx, tenantID, webhooks.EventSessionKeyCreated, view,
		webhooks.ResourceRefs{
			WalletID:     wallet.ID,
			GroupID:      group.ID,
			SessionKeyID: row.ID,
		})
	return c.Status(fiber.StatusCreated).JSON(CreateSessionKeyResponse{
		SessionKeyView: view,
		PrivateKey:     hex.EncodeToString(minted.PrivateKey),
		Notice:         "Store the private_key value now. It cannot be retrieved later. The session key auto-rotates per rotation_mode.",
	})
}

// ──────────────────────────────────────────────────────────────────────
// List
// ──────────────────────────────────────────────────────────────────────

type ListSessionKeysResponse struct {
	Data []SessionKeyView `json:"data"`
}

// List implements GET /v1/wallet-groups/:id/session-keys[?limit&offset].
// Returns active + revoked + expired so the dashboard can show full history.
func (h *SessionKeys) List(c *fiber.Ctx) error {
	tenantID := middleware.TenantID(c)
	if tenantID == "" {
		return badRequest(c, "missing tenant context")
	}
	groupID := c.Params("id")
	if groupID == "" {
		return badRequest(c, "missing group id")
	}

	ctx := c.UserContext()
	group, err := h.q.GetWalletGroupByID(ctx, groupID)
	if err != nil || group.TenantID != tenantID {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "wallet group not found"})
	}

	limit := c.QueryInt("limit", 50)
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	offset := c.QueryInt("offset", 0)
	if offset < 0 {
		offset = 0
	}

	// Listing-by-wallet returns ONE wallet's session keys; for the group
	// view we walk each wallet. Group wallet count is small (currently 2),
	// so the N+1 is bounded and cheap. Add a dedicated query if we ever
	// have 5+ rails per group.
	wallets, err := h.q.ListWalletsByGroup(ctx, group.ID)
	if err != nil {
		return internalError(c, err)
	}
	var rows []sqlcgen.SessionKey
	for _, w := range wallets {
		batch, err := h.q.ListSessionKeysForWallet(ctx, sqlcgen.ListSessionKeysForWalletParams{
			WalletID: w.ID,
			Limit:    int32(limit),
			Offset:   int32(offset),
		})
		if err != nil {
			return internalError(c, err)
		}
		rows = append(rows, batch...)
	}

	out := make([]SessionKeyView, len(rows))
	for i, r := range rows {
		out[i] = toSessionKeyView(r)
	}
	return c.JSON(ListSessionKeysResponse{Data: out})
}

// ──────────────────────────────────────────────────────────────────────
// Revoke
// ──────────────────────────────────────────────────────────────────────

type RevokeSessionKeyRequest struct {
	Reason string `json:"reason"`
}

// Revoke implements DELETE /v1/wallet-groups/:id/session-keys/:sk_id.
// Tenant-scoped: a leaked key id from another tenant gets the same 404 as
// a genuinely-missing id.
//
// Idempotent: already-revoked keys are returned unchanged so duplicate
// DELETEs don't rewrite revoked_at.
func (h *SessionKeys) Revoke(c *fiber.Ctx) error {
	tenantID := middleware.TenantID(c)
	if tenantID == "" {
		return badRequest(c, "missing tenant context")
	}
	if role := middleware.Role(c); role != "" && role != "owner" && role != "admin" {
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{
			"error": "your role cannot revoke session keys",
		})
	}

	skID := c.Params("sk_id")
	if skID == "" {
		return badRequest(c, "missing session key id")
	}

	ctx := c.UserContext()
	existing, err := h.q.GetSessionKeyByID(ctx, skID)
	if err != nil || existing.TenantID != tenantID {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "session key not found"})
	}

	if existing.Status != "active" {
		// Already revoked / expired — return current state unchanged.
		return c.JSON(toSessionKeyView(existing))
	}

	var req RevokeSessionKeyRequest
	_ = c.BodyParser(&req) // body optional

	reason := req.Reason
	if reason == "" {
		reason = "manually revoked"
	}
	row, err := h.q.RevokeSessionKey(ctx, sqlcgen.RevokeSessionKeyParams{
		ID:            skID,
		RevokedReason: pgxText(reason),
	})
	if err != nil {
		return internalError(c, err)
	}
	view := toSessionKeyView(row)
	h.publish(ctx, tenantID, webhooks.EventSessionKeyRevoked, view,
		webhooks.ResourceRefs{
			WalletID:     row.WalletID,
			GroupID:      row.GroupID,
			SessionKeyID: row.ID,
		})
	return c.JSON(view)
}

// ──────────────────────────────────────────────────────────────────────
// helpers
// ──────────────────────────────────────────────────────────────────────

func validateCreateSessionKey(r CreateSessionKeyRequest) error {
	if r.Name == "" {
		return errors.New("name is required")
	}
	if r.Rail != "" && r.Rail != "tempo" && r.Rail != "crossmint" {
		return errors.New(`rail must be "tempo" or "crossmint"`)
	}
	switch r.RotationMode {
	case "", "auto_rotate", "notify_only", "hard_expire":
	default:
		return errors.New(`rotation_mode must be one of "auto_rotate", "notify_only", "hard_expire"`)
	}
	if r.RotationIntervalHours < 0 || r.RotationIntervalHours > 24*30 {
		return errors.New("rotation_interval_hours must be between 0 and 720")
	}
	return nil
}

func parseSessionCaps(r CreateSessionKeyRequest) (perTx, daily, weekly, monthly int64, err error) {
	if perTx, err = parseUSD(r.Limits.PerTxAmount); err != nil {
		err = fmt.Errorf("per_tx_amount: %w", err)
		return
	}
	if daily, err = parseUSD(r.Limits.DailyAmount); err != nil {
		err = fmt.Errorf("daily_amount: %w", err)
		return
	}
	if weekly, err = parseUSD(r.Limits.WeeklyAmount); err != nil {
		err = fmt.Errorf("weekly_amount: %w", err)
		return
	}
	if monthly, err = parseUSD(r.Limits.MonthlyAmount); err != nil {
		err = fmt.Errorf("monthly_amount: %w", err)
		return
	}
	return
}

// parseUSD takes "100" / "100.00" (whole-dollar MVP) and returns int64 USD.
// Empty string returns 0 — sessionkey.validateMint then enforces
// non-negative.
func parseUSD(s string) (int64, error) {
	if s == "" {
		return 0, nil
	}
	// Strip a trailing ".00" so the customer's whole-dollar input still
	// parses. Sub-dollar caps wait for M9 (dashboard for cents).
	if dot := indexByte(s, '.'); dot >= 0 {
		tail := s[dot+1:]
		if !allZeros(tail) {
			return 0, errors.New("must be a whole USD integer (no sub-dollar amounts yet)")
		}
		s = s[:dot]
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil || n < 0 {
		return 0, errors.New("must be a non-negative integer USD amount")
	}
	return n, nil
}

func indexByte(s string, b byte) int {
	for i := 0; i < len(s); i++ {
		if s[i] == b {
			return i
		}
	}
	return -1
}

func allZeros(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] != '0' {
			return false
		}
	}
	return true
}

func toSessionKeyView(r sqlcgen.SessionKey) SessionKeyView {
	v := SessionKeyView{
		ID:                r.ID,
		Name:              r.Name,
		WalletID:          r.WalletID,
		GroupID:           r.GroupID,
		PublicAddress:     r.PublicAddress,
		Status:            r.Status,
		RotationMode:      r.RotationMode,
		RotationIntervalS: r.RotationIntervalS,
		RailNative:        r.RailNative,
	}
	if r.PolicyID.Valid {
		v.PolicyID = r.PolicyID.String
	}
	if r.ExpiresAt.Valid {
		v.ExpiresAt = r.ExpiresAt.Time.UTC().Format(time.RFC3339)
	}
	if r.NextRotationAt.Valid {
		v.NextRotationAt = r.NextRotationAt.Time.UTC().Format(time.RFC3339)
	}
	if r.CreatedAt.Valid {
		v.CreatedAt = r.CreatedAt.Time.UTC().Format(time.RFC3339)
	}
	if r.RevokedAt.Valid {
		v.RevokedAt = r.RevokedAt.Time.UTC().Format(time.RFC3339)
	}
	return v
}
