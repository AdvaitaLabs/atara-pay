// Package server wires the public HTTP API on top of the router.
//
// Route layout:
//
//	Public (no auth):
//	  GET    /health
//	  GET    /v1/rails
//	  POST   /signup
//	  POST   /login
//
//	Authenticated (Bearer — accepts both API keys and session JWTs):
//	  GET    /v1/api-keys
//	  POST   /v1/api-keys
//	  DELETE /v1/api-keys/:id
//	  *all current /v1/wallets, /v1/transactions, /v1/onramp routes*
package server

import (
	"errors"
	"net/http"

	"github.com/gofiber/fiber/v2"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	"github.com/atara-xyz/atara-pay/internal/adapters/crossmint"
	"github.com/atara-xyz/atara-pay/internal/adapters/tempo"
	paygwerr "github.com/atara-xyz/atara-pay/internal/errors"
	"github.com/atara-xyz/atara-pay/internal/keystore"
	"github.com/atara-xyz/atara-pay/internal/limits"
	"github.com/atara-xyz/atara-pay/internal/router"
	"github.com/atara-xyz/atara-pay/internal/server/handlers"
	"github.com/atara-xyz/atara-pay/internal/server/middleware"
	"github.com/atara-xyz/atara-pay/internal/types"
)

// Deps bundles everything the server needs to wire its routes. Constructed
// once in main and passed in.
type Deps struct {
	Router            *router.Router
	Pool              *pgxpool.Pool // nil disables auth-required routes
	SessionSigningKey []byte

	// Optional. When all three are non-nil AND Pool is set, the dual-rail
	// /v1/wallet-groups endpoint is mounted. Missing any one disables it.
	CrossMint *crossmint.Adapter
	Tempo     *tempo.Adapter
	Keystore  keystore.Keystore

	// Optional. Redis powers the limits.Service period accumulators
	// (daily / weekly / monthly). Nil falls back to the synchronous gates
	// only — per-tx + recipient + expiry still enforce.
	Redis *redis.Client
}

// Server is the HTTP entry point.
type Server struct {
	app    *fiber.App
	router *router.Router

	pool           *pgxpool.Pool
	authHandlers   *handlers.Auth
	wgHandlers     *handlers.WalletGroups
	limitsHandlers *handlers.Limits
	// queries is the sqlcgen.*Queries the middleware needs. We re-use the
	// queries built inside handlers.Auth to avoid two duplicate caches.
	queries    middleware.Queries
	signingKey []byte
}

// New constructs a Server from its deps. Auth routes are mounted only when
// Deps.Pool is non-nil; in pure in-memory mode (no DB) we still serve the
// /v1/{wallets,transactions,onramp} endpoints for backwards compatibility
// during the migration window.
func New(d Deps) *Server {
	app := fiber.New(fiber.Config{
		AppName:               "atara-pay",
		DisableStartupMessage: true,
		ErrorHandler:          errorHandler,
	})

	s := &Server{app: app, router: d.Router, pool: d.Pool, signingKey: d.SessionSigningKey}

	if d.Pool != nil {
		// 24h default TTL (handlers.NewAuth substitutes when ttl <= 0).
		s.authHandlers = handlers.NewAuth(d.Pool, d.SessionSigningKey, 0)
		// Auth middleware wants the same Querier the handlers use, but
		// behind a narrow interface. The sqlcgen-generated *Queries
		// satisfies it directly.
		s.queries = handlers.NewQueriesForMiddleware(d.Pool)
		s.limitsHandlers = handlers.NewLimits(d.Pool)

		// Build the limits.Service the wallet-group transaction handler
		// consults on every transfer. Redis is optional — d.Redis nil means
		// "synchronous gates only" (per-tx, recipient, expiry); the period
		// accumulators no-op until Redis is wired by a later sprint.
		limitsSvc := limits.New(handlers.NewQueriesForMiddleware(d.Pool), d.Redis)

		// Wallet-group handler needs all four extra deps. Missing any
		// disables the endpoint — server still boots.
		if d.CrossMint != nil && d.Tempo != nil && d.Keystore != nil {
			s.wgHandlers = handlers.NewWalletGroups(
				d.Pool, d.CrossMint, d.Tempo, d.Keystore, limitsSvc,
			)
		}
	}

	s.routes()
	return s
}

func (s *Server) routes() {
	// Public.
	s.app.Get("/health", s.health)
	s.app.Get("/v1/rails", s.listRails)

	if s.authHandlers != nil {
		s.app.Post("/signup", s.authHandlers.Signup)
		s.app.Post("/login", s.authHandlers.Login)
	}

	// Authenticated /v1 group.
	v1 := s.app.Group("/v1")
	if s.authHandlers != nil && len(s.signingKey) >= 32 {
		v1.Use(middleware.EitherAuth(s.queries, s.signingKey))
	}

	// API key management.
	if s.authHandlers != nil {
		v1.Get("/api-keys", s.authHandlers.ListAPIKeys)
		v1.Post("/api-keys", s.authHandlers.CreateAPIKey)
		v1.Delete("/api-keys/:id", s.authHandlers.RevokeAPIKey)
	}

	// Dual-rail wallet-group endpoints.
	if s.wgHandlers != nil {
		v1.Post("/wallet-groups", s.wgHandlers.Create)
		v1.Get("/wallet-groups", s.wgHandlers.List)
		v1.Get("/wallet-groups/:id", s.wgHandlers.Get)
		v1.Post("/wallet-groups/:id/transactions", s.wgHandlers.SendTransaction)
		v1.Post("/wallet-groups/:id/onramp", s.wgHandlers.CreateOnramp)
	}

	// Tenant-level limit management.
	if s.limitsHandlers != nil {
		v1.Get("/tenants/me/limits", s.limitsHandlers.GetTenantLimits)
		v1.Put("/tenants/me/limits", s.limitsHandlers.UpdateTenantLimits)
		v1.Get("/tenants/me/limits/violations", s.limitsHandlers.ListViolations)
	}

	// Rail-backed routes (unchanged from MVP).
	v1.Post("/wallets", s.createWallet)
	v1.Get("/wallets/:locator", s.getWallet)
	v1.Get("/wallets/:locator/balances", s.getBalances)

	v1.Post("/transactions", s.createTransaction)
	v1.Get("/transactions/:id", s.getTransaction)

	v1.Post("/onramp/orders", s.createOnramp)
}

func (s *Server) Listen(addr string) error {
	return s.app.Listen(addr)
}

// --- handlers ---

func (s *Server) health(c *fiber.Ctx) error {
	return c.JSON(fiber.Map{
		"status": "ok",
		"rails":  s.router.Registered(),
	})
}

func (s *Server) listRails(c *fiber.Ctx) error {
	return c.JSON(fiber.Map{"rails": s.router.Registered()})
}

func (s *Server) createWallet(c *fiber.Ctx) error {
	var req types.CreateWalletRequest
	if err := c.BodyParser(&req); err != nil {
		return badRequest(err)
	}
	a, err := s.router.Pick(railFromCtx(c, req.Rail))
	if err != nil {
		return err
	}
	w, err := a.CreateWallet(c.UserContext(), req)
	if err != nil {
		return err
	}
	return c.Status(http.StatusCreated).JSON(w)
}

func (s *Server) getWallet(c *fiber.Ctx) error {
	a, err := s.router.Pick(railFromCtx(c, ""))
	if err != nil {
		return err
	}
	w, err := a.GetWallet(c.UserContext(), c.Params("locator"))
	if err != nil {
		return err
	}
	return c.JSON(w)
}

func (s *Server) getBalances(c *fiber.Ctx) error {
	a, err := s.router.Pick(railFromCtx(c, ""))
	if err != nil {
		return err
	}
	bs, err := a.GetBalances(c.UserContext(), c.Params("locator"))
	if err != nil {
		return err
	}
	return c.JSON(fiber.Map{"balances": bs})
}

// txRequest extends the unified body with the source-wallet locator on the rail.
// Atara-Pay-issued wallet ids and provider locators are 1:1 for now; once an
// idempotency/audit DB lands, we will resolve Atara-Pay ids → provider locators
// before calling the adapter.
type txRequest struct {
	types.CreateTransactionRequest
	FromLocator string `json:"from_locator,omitempty"`
}

func (s *Server) createTransaction(c *fiber.Ctx) error {
	var req txRequest
	if err := c.BodyParser(&req); err != nil {
		return badRequest(err)
	}
	a, err := s.router.Pick(railFromCtx(c, ""))
	if err != nil {
		return err
	}
	locator := req.FromLocator
	if locator == "" {
		locator = req.From
	}
	tx, err := a.CreateTransaction(c.UserContext(), req.CreateTransactionRequest, locator)
	if err != nil {
		return err
	}
	return c.Status(http.StatusCreated).JSON(tx)
}

func (s *Server) getTransaction(c *fiber.Ctx) error {
	a, err := s.router.Pick(railFromCtx(c, ""))
	if err != nil {
		return err
	}
	tx, err := a.GetTransaction(c.UserContext(), c.Params("id"))
	if err != nil {
		return err
	}
	return c.JSON(tx)
}

// onrampRequest carries the wallet's on-chain address (CrossMint orders take
// an address, not a wallet id).
type onrampRequest struct {
	types.CreateOnrampRequest
	WalletAddress string `json:"wallet_address"`
}

func (s *Server) createOnramp(c *fiber.Ctx) error {
	var req onrampRequest
	if err := c.BodyParser(&req); err != nil {
		return badRequest(err)
	}
	a, err := s.router.Pick(railFromCtx(c, ""))
	if err != nil {
		return err
	}
	addr := req.WalletAddress
	if addr == "" {
		addr = req.WalletID
	}
	order, err := a.CreateOnrampOrder(c.UserContext(), req.CreateOnrampRequest, addr)
	if err != nil {
		return err
	}
	return c.Status(http.StatusCreated).JSON(order)
}

// --- helpers ---

// railFromCtx picks the rail in this order: body field, ?rail=, default.
func railFromCtx(c *fiber.Ctx, fromBody types.Rail) types.Rail {
	if fromBody != "" {
		return fromBody
	}
	if q := c.Query("rail"); q != "" {
		return types.Rail(q)
	}
	return ""
}

func badRequest(err error) error {
	return fiber.NewError(http.StatusBadRequest, err.Error())
}

func errorHandler(c *fiber.Ctx, err error) error {
	var fe *fiber.Error
	if errors.As(err, &fe) {
		return c.Status(fe.Code).JSON(fiber.Map{"error": fe.Message})
	}

	var up *paygwerr.UpstreamError
	if errors.As(err, &up) {
		return c.Status(http.StatusBadGateway).JSON(fiber.Map{
			"error":         "upstream rail error",
			"rail":          up.Rail,
			"upstream_code": up.Status,
			"upstream_body": up.Body,
		})
	}

	switch {
	case errors.Is(err, paygwerr.ErrNotFound):
		return c.Status(http.StatusNotFound).JSON(fiber.Map{"error": err.Error()})
	case errors.Is(err, paygwerr.ErrUnauthorized):
		return c.Status(http.StatusUnauthorized).JSON(fiber.Map{"error": err.Error()})
	case errors.Is(err, paygwerr.ErrBadRequest):
		return c.Status(http.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	case errors.Is(err, paygwerr.ErrUnsupported):
		return c.Status(http.StatusNotImplemented).JSON(fiber.Map{"error": err.Error()})
	}

	return c.Status(http.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
}
