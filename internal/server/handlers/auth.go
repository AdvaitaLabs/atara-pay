// Package handlers contains the Fiber HTTP handlers that turn JSON into
// service calls. The package is intentionally thin: parse → validate →
// invoke storage → serialize. Business logic that isn't trivially the
// HTTP shape belongs in a service package called from here.
package handlers

import (
	"context"
	"errors"
	"net/mail"
	"strings"

	"github.com/gofiber/fiber/v2"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/atara-xyz/atara-pay/internal/auth"
	"github.com/atara-xyz/atara-pay/internal/db/sqlcgen"
	"github.com/atara-xyz/atara-pay/internal/id"
)

// Auth wires the signup/login/api-key handlers to their dependencies.
// Construct one per process and mount its methods on the router.
type Auth struct {
	pool *pgxpool.Pool
	q    *sqlcgen.Queries
}

// NewAuth builds the handler set. Pass the pgxpool so we can open
// transactions; pass the Querier for read-only lookups.
func NewAuth(pool *pgxpool.Pool) *Auth {
	return &Auth{pool: pool, q: sqlcgen.New(pool)}
}

// ──────────────────────────────────────────────────────────────────────
// Signup
// ──────────────────────────────────────────────────────────────────────

// SignupRequest is the JSON body of POST /signup.
type SignupRequest struct {
	CompanyName string `json:"company_name"`
	Email       string `json:"email"`
	Password    string `json:"password"`
}

// SignupResponse is what a successful POST /signup returns.
// The api_keys.{test,live}.key strings are shown ONCE — clients must
// store them immediately.
type SignupResponse struct {
	Tenant  tenantView   `json:"tenant"`
	User    userView     `json:"user"`
	APIKeys apiKeyPair   `json:"api_keys"`
	Notice  string       `json:"_notice"`
}

type tenantView struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	PrimaryEmail string `json:"primary_email"`
	Plan         string `json:"plan"`
}

type userView struct {
	ID    string `json:"id"`
	Email string `json:"email"`
	Role  string `json:"role"`
}

type apiKeyPair struct {
	Test apiKeyMint `json:"test"`
	Live apiKeyMint `json:"live"`
}

type apiKeyMint struct {
	ID     string `json:"id"`
	Key    string `json:"key"`    // raw secret — ONE-TIME
	Prefix string `json:"prefix"` // shown safely in dashboard later
}

// Signup is the registration endpoint. It does, atomically:
//
//	1. CHECK no tenant exists at this primary_email
//	2. INSERT tenants
//	3. INSERT users (the owner)
//	4. INSERT api_keys × 2 (one test, one live)
//
// Everything in a single pgx transaction — partial failure leaves no
// half-built tenant lying around.
func (h *Auth) Signup(c *fiber.Ctx) error {
	var req SignupRequest
	if err := c.BodyParser(&req); err != nil {
		return badRequest(c, "invalid JSON body")
	}
	req.CompanyName = strings.TrimSpace(req.CompanyName)
	req.Email = strings.ToLower(strings.TrimSpace(req.Email))

	if err := validateSignup(req); err != nil {
		return badRequest(c, err.Error())
	}

	pwHash, err := auth.HashPassword(req.Password)
	if err != nil {
		return internalError(c, err)
	}

	tenantID := id.New(id.PrefixTenant)
	userID := id.New(id.PrefixUser)
	testID := id.New(id.PrefixAPIKey)
	liveID := id.New(id.PrefixAPIKey)

	// Mint both keys up front (CPU-only, no DB) so we never roll back a
	// transaction just because crypto/rand hiccuped.
	testRaw, testPrefix, testHash, err := auth.MintAPIKey("test")
	if err != nil {
		return internalError(c, err)
	}
	liveRaw, livePrefix, liveHash, err := auth.MintAPIKey("live")
	if err != nil {
		return internalError(c, err)
	}

	tx, err := h.pool.Begin(c.UserContext())
	if err != nil {
		return internalError(c, err)
	}
	defer tx.Rollback(context.Background())

	qtx := h.q.WithTx(tx)

	tenant, err := qtx.CreateTenant(c.UserContext(), sqlcgen.CreateTenantParams{
		ID:           tenantID,
		Name:         req.CompanyName,
		PrimaryEmail: req.Email,
		Plan:         "free",
		Status:       "active",
		Metadata:     []byte("{}"),
	})
	if err != nil {
		if isUniqueViolation(err) {
			return conflict(c, "an account with this email already exists")
		}
		return internalError(c, err)
	}

	user, err := qtx.CreateUser(c.UserContext(), sqlcgen.CreateUserParams{
		ID:           userID,
		TenantID:     tenant.ID,
		Email:        req.Email,
		PasswordHash: pwHash,
		Role:         "owner",
	})
	if err != nil {
		return internalError(c, err)
	}

	createdByForKey := pgxText(user.ID)

	testKey, err := qtx.CreateAPIKey(c.UserContext(), sqlcgen.CreateAPIKeyParams{
		ID:          testID,
		TenantID:    tenant.ID,
		CreatedBy:   createdByForKey,
		Name:        "default test key",
		Environment: "test",
		KeyPrefix:   testPrefix,
		KeyHash:     testHash,
	})
	if err != nil {
		return internalError(c, err)
	}

	liveKey, err := qtx.CreateAPIKey(c.UserContext(), sqlcgen.CreateAPIKeyParams{
		ID:          liveID,
		TenantID:    tenant.ID,
		CreatedBy:   createdByForKey,
		Name:        "default live key",
		Environment: "live",
		KeyPrefix:   livePrefix,
		KeyHash:     liveHash,
	})
	if err != nil {
		return internalError(c, err)
	}

	if err := tx.Commit(c.UserContext()); err != nil {
		return internalError(c, err)
	}

	return c.Status(fiber.StatusCreated).JSON(SignupResponse{
		Tenant: tenantView{
			ID: tenant.ID, Name: tenant.Name,
			PrimaryEmail: tenant.PrimaryEmail, Plan: tenant.Plan,
		},
		User: userView{
			ID: user.ID, Email: user.Email, Role: user.Role,
		},
		APIKeys: apiKeyPair{
			Test: apiKeyMint{ID: testKey.ID, Key: testRaw, Prefix: testPrefix},
			Live: apiKeyMint{ID: liveKey.ID, Key: liveRaw, Prefix: livePrefix},
		},
		Notice: "Store the api_keys.{test,live}.key values now. They cannot be retrieved later — only the prefix remains visible.",
	})
}

// ──────────────────────────────────────────────────────────────────────
// helpers
// ──────────────────────────────────────────────────────────────────────

const (
	minPasswordLen = 12
	maxCompanyLen  = 100
)

func validateSignup(r SignupRequest) error {
	if r.CompanyName == "" {
		return errors.New("company_name is required")
	}
	if len(r.CompanyName) > maxCompanyLen {
		return errors.New("company_name too long")
	}
	if _, err := mail.ParseAddress(r.Email); err != nil {
		return errors.New("email is not a valid address")
	}
	if len(r.Password) < minPasswordLen {
		return errors.New("password must be at least 12 characters")
	}
	return nil
}

// isUniqueViolation tells "duplicate email" from generic db error so we can
// return a clean 409 instead of a 500. pgx exposes SQLSTATE 23505 via the
// driver — we keep the check string-based to avoid pulling in pgconn here.
func isUniqueViolation(err error) bool {
	return err != nil && strings.Contains(err.Error(), "SQLSTATE 23505")
}

func badRequest(c *fiber.Ctx, msg string) error {
	return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": msg})
}

func conflict(c *fiber.Ctx, msg string) error {
	return c.Status(fiber.StatusConflict).JSON(fiber.Map{"error": msg})
}

func internalError(c *fiber.Ctx, err error) error {
	// TODO(observability): structured logger w/ request id once that lands.
	return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
		"error": "internal error",
	})
}
