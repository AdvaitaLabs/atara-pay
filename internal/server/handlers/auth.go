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
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/atara-xyz/atara-pay/internal/auth"
	"github.com/atara-xyz/atara-pay/internal/db/sqlcgen"
	"github.com/atara-xyz/atara-pay/internal/id"
	"github.com/atara-xyz/atara-pay/internal/limits"
)

// Auth wires the signup/login/api-key handlers to their dependencies.
// Construct one per process and mount its methods on the router.
type Auth struct {
	pool          *pgxpool.Pool
	q             *sqlcgen.Queries
	sessionSecret []byte
	sessionTTL    time.Duration
}

// NewAuth builds the handler set. sessionSecret must be at least 32 bytes
// (see auth.NewSessionToken). sessionTTL=0 falls back to 24h.
func NewAuth(pool *pgxpool.Pool, sessionSecret []byte, sessionTTL time.Duration) *Auth {
	if sessionTTL <= 0 {
		sessionTTL = 24 * time.Hour
	}
	return &Auth{
		pool:          pool,
		q:             sqlcgen.New(pool),
		sessionSecret: sessionSecret,
		sessionTTL:    sessionTTL,
	}
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

	// Default tenant_default policy at the Conservative tier. Customers
	// upgrade tiers later via the /v1/tenants/:id/limits endpoint (M4.4);
	// every brand-new tenant starts safe.
	tier := limits.TierConservative
	_, err = qtx.CreateLimitPolicy(c.UserContext(), sqlcgen.CreateLimitPolicyParams{
		ID:                id.New(id.PrefixLimitPolicy),
		TenantID:          tenant.ID,
		ScopeType:         "tenant_default",
		ScopeID:           pgxText(""), // tenant_default leaves scope_id NULL
		PerTxAmount:       limits.ToNumeric(tier.PerTxUSD),
		PerTxAsset:        pgxText("USDC"),
		DailyAmount:       limits.ToNumeric(tier.DailyUSD),
		WeeklyAmount:      limits.ToNumeric(tier.WeeklyUSD),
		MonthlyAmount:     limits.ToNumeric(tier.MonthlyUSD),
		PeriodAsset:       "USDC",
		Timezone:          "UTC",
		ResetDayOfWeek:    1, // Monday
		ResetDayOfMonth:   1,
		AllowedRecipients: []byte("[]"),
		DeniedRecipients:  []byte("[]"),
		Enabled:           true,
		Metadata: []byte(`{"tier":"` + tier.Name + `"}`),
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
// Login
// ──────────────────────────────────────────────────────────────────────

// LoginRequest is the JSON body of POST /login.
type LoginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

// LoginResponse is what a successful POST /login returns. The session_token
// is an HS256 JWT — clients send it back as Authorization: Bearer for the
// dashboard's authenticated routes.
type LoginResponse struct {
	SessionToken string   `json:"session_token"`
	ExpiresAt    int64    `json:"expires_at"`
	User         userView `json:"user"`
	TenantID     string   `json:"tenant_id"`
}

// Login authenticates by email + password. On success it mints a session
// JWT and updates last_login_at. On failure it returns a generic 401 — we
// never differentiate "wrong email" from "wrong password" to avoid leaking
// which emails are registered.
func (h *Auth) Login(c *fiber.Ctx) error {
	var req LoginRequest
	if err := c.BodyParser(&req); err != nil {
		return badRequest(c, "invalid JSON body")
	}
	req.Email = strings.ToLower(strings.TrimSpace(req.Email))
	if req.Email == "" || req.Password == "" {
		return loginRejected(c)
	}

	// Look up by email. We use GetUserByEmail (across tenants) and pick the
	// first hit, since email is unique per tenant but not globally. If the
	// same email exists at multiple tenants, a follow-up endpoint can ask
	// the user which one to log into; for MVP we take the earliest signup.
	users, err := h.q.GetUserByEmail(c.UserContext(), req.Email)
	if err != nil || len(users) == 0 {
		// Hash a dummy password anyway so timing doesn't reveal whether
		// the email exists. argon2 dominates the request latency.
		_, _ = auth.HashPassword("dummy-to-equalize-timing")
		return loginRejected(c)
	}
	user := users[0]

	if err := auth.VerifyPassword(user.PasswordHash, req.Password); err != nil {
		return loginRejected(c)
	}

	tok, err := auth.NewSessionToken(
		h.sessionSecret, user.ID, user.TenantID, user.Role, h.sessionTTL,
	)
	if err != nil {
		return internalError(c, err)
	}

	// Fire-and-forget login timestamp update — never blocks the login.
	go func(id string) {
		_ = h.q.TouchUserLogin(context.Background(), id)
	}(user.ID)

	return c.JSON(LoginResponse{
		SessionToken: tok,
		ExpiresAt:    time.Now().Add(h.sessionTTL).Unix(),
		User: userView{
			ID:    user.ID,
			Email: user.Email,
			Role:  user.Role,
		},
		TenantID: user.TenantID,
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

// loginRejected is the single 401 path used by Login. The body and timing
// must look identical regardless of whether the email or password was wrong.
func loginRejected(c *fiber.Ctx) error {
	return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
		"error": "invalid email or password",
	})
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
