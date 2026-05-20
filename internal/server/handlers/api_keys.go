package handlers

import (
	"github.com/gofiber/fiber/v2"

	"github.com/atara-xyz/atara-pay/internal/auth"
	"github.com/atara-xyz/atara-pay/internal/db/sqlcgen"
	"github.com/atara-xyz/atara-pay/internal/id"
	"github.com/atara-xyz/atara-pay/internal/server/middleware"
)

// APIKeyView is the safe-to-return projection of an api_keys row. The raw
// secret is omitted (only available at creation time, see SignupResponse and
// CreateAPIKey responses).
type APIKeyView struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Environment string `json:"environment"`
	Prefix      string `json:"prefix"`
	CreatedAt   string `json:"created_at,omitempty"`
	LastUsedAt  string `json:"last_used_at,omitempty"`
	RevokedAt   string `json:"revoked_at,omitempty"`
}

func toAPIKeyView(k sqlcgen.ApiKey) APIKeyView {
	v := APIKeyView{
		ID:          k.ID,
		Name:        k.Name,
		Environment: k.Environment,
		Prefix:      k.KeyPrefix,
	}
	if k.CreatedAt.Valid {
		v.CreatedAt = k.CreatedAt.Time.UTC().Format("2006-01-02T15:04:05Z")
	}
	if k.LastUsedAt.Valid {
		v.LastUsedAt = k.LastUsedAt.Time.UTC().Format("2006-01-02T15:04:05Z")
	}
	if k.RevokedAt.Valid {
		v.RevokedAt = k.RevokedAt.Time.UTC().Format("2006-01-02T15:04:05Z")
	}
	return v
}

// ──────────────────────────────────────────────────────────────────────
// List
// ──────────────────────────────────────────────────────────────────────

// ListAPIKeysResponse wraps the API keys list under a "data" field, leaving
// room to add pagination metadata later without breaking clients.
type ListAPIKeysResponse struct {
	Data []APIKeyView `json:"data"`
}

// ListAPIKeys returns every key (active + revoked) for the current tenant.
// Revoked keys are included so customers can audit history — the UI shows
// them with a strike-through.
func (h *Auth) ListAPIKeys(c *fiber.Ctx) error {
	tenantID := middleware.TenantID(c)
	if tenantID == "" {
		return badRequest(c, "missing tenant context")
	}
	rows, err := h.q.ListAPIKeysInTenant(c.UserContext(), tenantID)
	if err != nil {
		return internalError(c, err)
	}
	out := make([]APIKeyView, len(rows))
	for i, r := range rows {
		out[i] = toAPIKeyView(r)
	}
	return c.JSON(ListAPIKeysResponse{Data: out})
}

// ──────────────────────────────────────────────────────────────────────
// Create
// ──────────────────────────────────────────────────────────────────────

// CreateAPIKeyRequest is the JSON body of POST /v1/api-keys.
type CreateAPIKeyRequest struct {
	Name        string `json:"name"`
	Environment string `json:"environment"` // "test" or "live"
}

// CreateAPIKeyResponse returns the full key view PLUS the raw secret, which
// is only shown here. Lose it and you mint a new one.
type CreateAPIKeyResponse struct {
	APIKeyView
	Key    string `json:"key"`
	Notice string `json:"_notice"`
}

// CreateAPIKey mints a fresh key for the current tenant. Only OWNERS and
// ADMINS may call this when authenticated via session JWT; API-key auth
// allows the calling key to act on its tenant's behalf.
func (h *Auth) CreateAPIKey(c *fiber.Ctx) error {
	tenantID := middleware.TenantID(c)
	if tenantID == "" {
		return badRequest(c, "missing tenant context")
	}

	// Session-JWT auth attaches a role; API-key auth doesn't. If a role IS
	// present, gate by it. If not, the caller authenticated via an API key
	// — that key already proves tenant ownership, so allow it.
	if role := middleware.Role(c); role != "" && !canManageKeys(role) {
		return c.Status(fiber.StatusForbidden).
			JSON(fiber.Map{"error": "your role cannot create API keys"})
	}

	var req CreateAPIKeyRequest
	if err := c.BodyParser(&req); err != nil {
		return badRequest(c, "invalid JSON body")
	}
	if req.Name == "" {
		return badRequest(c, "name is required")
	}
	if req.Environment != "test" && req.Environment != "live" {
		return badRequest(c, "environment must be 'test' or 'live'")
	}

	raw, prefix, hash, err := auth.MintAPIKey(req.Environment)
	if err != nil {
		return internalError(c, err)
	}

	row, err := h.q.CreateAPIKey(c.UserContext(), sqlcgen.CreateAPIKeyParams{
		ID:          id.New(id.PrefixAPIKey),
		TenantID:    tenantID,
		CreatedBy:   pgxText(middleware.UserID(c)), // empty when API-key auth — pgxText turns "" into SQL NULL
		Name:        req.Name,
		Environment: req.Environment,
		KeyPrefix:   prefix,
		KeyHash:     hash,
	})
	if err != nil {
		return internalError(c, err)
	}

	view := toAPIKeyView(row)
	return c.Status(fiber.StatusCreated).JSON(CreateAPIKeyResponse{
		APIKeyView: view,
		Key:        raw,
		Notice:     "Store the key value now. It cannot be retrieved later — only the prefix remains visible.",
	})
}

// ──────────────────────────────────────────────────────────────────────
// Revoke
// ──────────────────────────────────────────────────────────────────────

// RevokeAPIKeyRequest carries an optional human-readable reason captured
// in the audit trail.
type RevokeAPIKeyRequest struct {
	Reason string `json:"reason"`
}

// RevokeAPIKey marks a key as revoked. The DELETE is soft — the row stays
// so audits can still see which key authenticated past requests.
func (h *Auth) RevokeAPIKey(c *fiber.Ctx) error {
	tenantID := middleware.TenantID(c)
	if tenantID == "" {
		return badRequest(c, "missing tenant context")
	}
	if role := middleware.Role(c); role != "" && !canManageKeys(role) {
		return c.Status(fiber.StatusForbidden).
			JSON(fiber.Map{"error": "your role cannot revoke API keys"})
	}

	keyID := c.Params("id")
	if keyID == "" {
		return badRequest(c, "missing id")
	}

	// Cross-tenant guard: load the row first and confirm the tenant matches
	// before mutating. Otherwise a leaked key from tenant A could revoke
	// keys in tenant B by guessing IDs.
	existing, err := h.q.GetAPIKeyByID(c.UserContext(), keyID)
	if err != nil || existing.TenantID != tenantID {
		// Same response either way to avoid leaking key-id existence.
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "key not found"})
	}

	// Idempotent: if already revoked, return the existing view unchanged.
	// RevokeAPIKey's WHERE clause filters on revoked_at IS NULL so we'd hit
	// pgx.ErrNoRows otherwise — checking up front is clearer.
	if existing.RevokedAt.Valid {
		return c.JSON(toAPIKeyView(existing))
	}

	var req RevokeAPIKeyRequest
	_ = c.BodyParser(&req) // body is optional

	reason := req.Reason
	if reason == "" {
		reason = "manually revoked"
	}

	updated, err := h.q.RevokeAPIKey(c.UserContext(), sqlcgen.RevokeAPIKeyParams{
		ID:            keyID,
		RevokedReason: pgxText(reason),
	})
	if err != nil {
		return internalError(c, err)
	}
	return c.JSON(toAPIKeyView(updated))
}

// canManageKeys returns whether a session-authenticated user with the given
// role may create / revoke API keys. Owners and admins can; everyone else
// (developer / finance / viewer) cannot.
func canManageKeys(role string) bool {
	return role == "owner" || role == "admin"
}
