package middleware

import (
	"strings"

	"github.com/gofiber/fiber/v2"

	"github.com/atara-xyz/atara-pay/internal/auth"
)

// Session-token context keys (in addition to the API-key keys in auth.go).
const (
	CtxUserID = "atara.user_id"
	CtxRole   = "atara.role"
)

// SessionAuth returns a middleware that authenticates a request via the
// dashboard's session JWT (Authorization: Bearer <jwt>).
//
// On success it stashes the same CtxTenantID handlers use for API-key auth,
// PLUS CtxUserID and CtxRole. Downstream handlers therefore work
// indistinguishably under either auth mode — the only signal is whether
// UserID(c) returns "".
func SessionAuth(signingKey []byte) fiber.Handler {
	if len(signingKey) < 32 {
		// Booting with a too-short signing key is a config bug; refuse
		// to start the middleware rather than silently allowing weak tokens.
		panic("middleware: SessionAuth requires >= 32-byte signing key")
	}
	return func(c *fiber.Ctx) error {
		raw, err := bearerFromHeader(c.Get("Authorization"))
		if err != nil {
			return unauthorized(c, "missing or malformed Authorization header")
		}

		claims, err := auth.ParseSessionToken(signingKey, raw)
		if err != nil {
			return unauthorized(c, "invalid session token")
		}

		c.Locals(CtxTenantID, claims.TenantID)
		c.Locals(CtxUserID, claims.UserID)
		c.Locals(CtxRole, claims.Role)
		return c.Next()
	}
}

// EitherAuth lets a route accept BOTH API-key bearer tokens AND session JWTs.
// It inspects the token prefix — "sk_" routes to the API-key path, anything
// else (JWTs start with "eyJ") routes to the session path.
//
// Useful for endpoints like /v1/api-keys that are reached from both the
// customer's backend (API key) and the dashboard (session JWT).
func EitherAuth(q Queries, signingKey []byte) fiber.Handler {
	return EitherAuthForEnvironment(q, signingKey, "")
}

// EitherAuthForEnvironment is EitherAuth with the same gateway-env gate
// as APIKeyAuthForEnvironment. Session JWTs are unaffected — dashboard
// users live in a single environment by virtue of which gateway they hit.
func EitherAuthForEnvironment(q Queries, signingKey []byte, gatewayEnv string) fiber.Handler {
	apiKeyMW := APIKeyAuthForEnvironment(q, gatewayEnv)
	sessionMW := SessionAuth(signingKey)
	return func(c *fiber.Ctx) error {
		raw, err := bearerFromHeader(c.Get("Authorization"))
		if err != nil {
			return unauthorized(c, "missing or malformed Authorization header")
		}
		if strings.HasPrefix(raw, "sk_") {
			return apiKeyMW(c)
		}
		return sessionMW(c)
	}
}

// UserID returns the authenticated session user's id, or "" if the request
// authenticated via an API key.
func UserID(c *fiber.Ctx) string {
	v, _ := c.Locals(CtxUserID).(string)
	return v
}

// Role returns the authenticated user's role, or "" if the request
// authenticated via an API key (which carries no user context).
func Role(c *fiber.Ctx) string {
	v, _ := c.Locals(CtxRole).(string)
	return v
}

