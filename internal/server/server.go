// Package server wires the public HTTP API on top of the router.
package server

import (
	"errors"
	"net/http"

	"github.com/gofiber/fiber/v2"

	paygwerr "github.com/atara-xyz/atara-pay/internal/errors"
	"github.com/atara-xyz/atara-pay/internal/router"
	"github.com/atara-xyz/atara-pay/internal/types"
)

type Server struct {
	app    *fiber.App
	router *router.Router
}

func New(r *router.Router) *Server {
	app := fiber.New(fiber.Config{
		AppName:               "atara-pay",
		DisableStartupMessage: true,
		ErrorHandler:          errorHandler,
	})

	s := &Server{app: app, router: r}
	s.routes()
	return s
}

func (s *Server) routes() {
	s.app.Get("/health", s.health)
	s.app.Get("/v1/rails", s.listRails)

	v1 := s.app.Group("/v1")
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
