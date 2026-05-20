package middleware

import "github.com/gofiber/fiber/v2"

// Convenience getters so handlers don't sprinkle string keys around.
// Each returns "" / false if the auth middleware did not run.

// TenantID returns the authenticated tenant's id, or "" if absent.
func TenantID(c *fiber.Ctx) string {
	v, _ := c.Locals(CtxTenantID).(string)
	return v
}

// APIKeyID returns the api_key.id used to authenticate this request.
func APIKeyID(c *fiber.Ctx) string {
	v, _ := c.Locals(CtxAPIKeyID).(string)
	return v
}

// Environment is either "test" or "live".
func Environment(c *fiber.Ctx) string {
	v, _ := c.Locals(CtxEnvironment).(string)
	return v
}

// IsAuthenticated reports whether the auth middleware accepted this request.
// Routes that mount APIKeyAuth always satisfy this; public routes won't.
func IsAuthenticated(c *fiber.Ctx) bool {
	return TenantID(c) != ""
}
