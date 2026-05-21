// Package middleware holds the cross-cutting HTTP middleware Atara-Pay
// hangs off its Fiber routes: API-key auth, request-id, rate limit, etc.
//
// auth.go is the gatekeeper for every /v1/* endpoint. It:
//
//	1. Reads the Authorization: Bearer <key> header.
//	2. Validates the key's shape (cheap, no DB).
//	3. SHA-256 hashes it and looks the row up in api_keys.
//	4. Rejects with 401 if missing / revoked / wrong env.
//	5. On success, attaches the tenant/api-key context for handlers
//	   downstream to consume via ctxhelper.
//	6. Fire-and-forget bumps api_keys.last_used_at.
package middleware

import (
	"context"
	"errors"
	"strings"

	"github.com/gofiber/fiber/v2"

	"github.com/atara-xyz/atara-pay/internal/auth"
	"github.com/atara-xyz/atara-pay/internal/db/sqlcgen"
)

// Context keys. Stored on fiber.Ctx.Locals for the handlers.
const (
	CtxTenantID    = "atara.tenant_id"
	CtxAPIKeyID    = "atara.api_key_id"
	CtxEnvironment = "atara.env"
)

// Queries is the subset of sqlcgen.Querier the auth middleware needs.
// Decoupling the dependency lets the middleware be mocked in tests without
// pulling in a real DB.
type Queries interface {
	GetAPIKeyByHash(ctx context.Context, keyHash []byte) (sqlcgen.ApiKey, error)
	TouchAPIKeyUsage(ctx context.Context, id string) error
}

// APIKeyAuth returns a Fiber middleware that authenticates the request via
// the Authorization header. Pass a Queries implementation (usually the
// sqlcgen-generated *Queries struct). Does NOT cross-check the gateway
// environment — use APIKeyAuthForEnvironment when running prod/staging.
func APIKeyAuth(q Queries) fiber.Handler {
	return APIKeyAuthForEnvironment(q, "")
}

// APIKeyAuthForEnvironment is APIKeyAuth plus a gateway-environment gate:
// when gatewayEnv is "production", only "live" keys pass; for "staging" /
// "development", only "test" keys pass. Empty disables the gate (keeps the
// old behavior for callers that didn't opt in — and for the unit tests in
// auth_test.go that exercise the middleware in isolation).
func APIKeyAuthForEnvironment(q Queries, gatewayEnv string) fiber.Handler {
	expectedKeyEnv := requiredKeyEnv(gatewayEnv)
	return func(c *fiber.Ctx) error {
		raw, err := bearerFromHeader(c.Get("Authorization"))
		if err != nil {
			return unauthorized(c, "missing or malformed Authorization header")
		}

		env, err := auth.ParseAPIKey(raw)
		if err != nil {
			return unauthorized(c, "invalid api key format")
		}

		// Cheap pre-DB gate. Reject sk_live_ on staging and sk_test_ on
		// prod before we even hash. Avoids burning a DB lookup for the
		// most common misconfiguration ("forgot to switch keys when
		// moving environments"), and makes the error message specific
		// instead of a generic 401.
		if expectedKeyEnv != "" && env != expectedKeyEnv {
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
				"error": "api key environment (" + env + ") does not match gateway environment",
			})
		}

		hash := auth.HashAPIKey(raw)
		row, err := q.GetAPIKeyByHash(c.UserContext(), hash)
		if err != nil {
			// Treat "not found" exactly like "wrong key" — never leak which.
			return unauthorized(c, "invalid api key")
		}
		if row.Environment != env {
			// The DB row's env disagrees with the prefix we parsed — should
			// be impossible barring db tampering, but reject defensively.
			return unauthorized(c, "invalid api key")
		}

		// Attach to the request context. Handlers fetch via ctxhelper.
		c.Locals(CtxTenantID, row.TenantID)
		c.Locals(CtxAPIKeyID, row.ID)
		c.Locals(CtxEnvironment, row.Environment)

		// Fire-and-forget last_used_at update. Failure here MUST NOT fail
		// the request — at worst the timestamp lags by one call.
		//
		// Detached context: c.UserContext() is cancelled when the request
		// finishes, which would race the goroutine.
		go func(id string) {
			_ = q.TouchAPIKeyUsage(context.Background(), id)
		}(row.ID)

		return c.Next()
	}
}

// bearerFromHeader pulls "abc" out of "Bearer abc". Anything else is an
// error.
func bearerFromHeader(h string) (string, error) {
	if h == "" {
		return "", errors.New("missing")
	}
	const prefix = "Bearer "
	if !strings.HasPrefix(h, prefix) {
		return "", errors.New("not Bearer scheme")
	}
	token := strings.TrimSpace(h[len(prefix):])
	if token == "" {
		return "", errors.New("empty token")
	}
	return token, nil
}

// requiredKeyEnv maps gateway environment to the api_key.environment value
// it will accept. Empty result means "no gate".
func requiredKeyEnv(gatewayEnv string) string {
	switch gatewayEnv {
	case "production":
		return "live"
	case "staging", "development":
		return "test"
	default:
		return ""
	}
}

func unauthorized(c *fiber.Ctx, msg string) error {
	// Lowercase + RFC-7235 challenge header so curl users see why.
	c.Set("WWW-Authenticate", `Bearer realm="atara-pay"`)
	return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
		"error": msg,
	})
}
