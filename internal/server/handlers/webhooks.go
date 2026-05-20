package handlers

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/url"
	"strings"

	"github.com/gofiber/fiber/v2"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/atara-xyz/atara-pay/internal/db/sqlcgen"
	"github.com/atara-xyz/atara-pay/internal/id"
	"github.com/atara-xyz/atara-pay/internal/server/middleware"
)

// Webhooks handles /v1/webhook-endpoints CRUD.
type Webhooks struct {
	pool *pgxpool.Pool
	q    *sqlcgen.Queries
}

func NewWebhooks(pool *pgxpool.Pool) *Webhooks {
	return &Webhooks{pool: pool, q: sqlcgen.New(pool)}
}

// secretBytes is the size of the HMAC secret we mint per endpoint.
// 32 bytes = 256 bits, comfortably above what HMAC-SHA256 needs.
const secretBytes = 32

// ──────────────────────────────────────────────────────────────────────
// Views
// ──────────────────────────────────────────────────────────────────────

// EndpointView is the safe-to-return projection. The raw secret is omitted.
type EndpointView struct {
	ID                  string   `json:"id"`
	URL                 string   `json:"url"`
	SubscribedEvents    []string `json:"subscribed_events"`
	Status              string   `json:"status"`
	Description         string   `json:"description,omitempty"`
	SecretVersion       int      `json:"secret_version"`
	ConsecutiveFailures int      `json:"consecutive_failures"`
	LastSuccessAt       string   `json:"last_success_at,omitempty"`
	LastFailureAt       string   `json:"last_failure_at,omitempty"`
	CreatedAt           string   `json:"created_at,omitempty"`
}

// EndpointWithSecret extends EndpointView with the raw HMAC secret.
// Returned only at creation and rotation — never on list/get.
type EndpointWithSecret struct {
	EndpointView
	Secret string `json:"secret"`
	Notice string `json:"_notice"`
}

// ──────────────────────────────────────────────────────────────────────
// Create
// ──────────────────────────────────────────────────────────────────────

type CreateEndpointRequest struct {
	URL              string   `json:"url"`
	SubscribedEvents []string `json:"subscribed_events,omitempty"`
	Description      string   `json:"description,omitempty"`
}

func (h *Webhooks) Create(c *fiber.Ctx) error {
	tenantID := middleware.TenantID(c)
	if tenantID == "" {
		return badRequest(c, "missing tenant context")
	}
	if role := middleware.Role(c); role != "" && role != "owner" && role != "admin" {
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{
			"error": "your role cannot manage webhook endpoints",
		})
	}

	var req CreateEndpointRequest
	if err := c.BodyParser(&req); err != nil {
		return badRequest(c, "invalid JSON body")
	}
	if err := validateEndpointURL(req.URL); err != nil {
		return badRequest(c, err.Error())
	}

	secret := make([]byte, secretBytes)
	if _, err := rand.Read(secret); err != nil {
		return internalError(c, err)
	}

	subs, err := marshalEventsList(req.SubscribedEvents)
	if err != nil {
		return badRequest(c, err.Error())
	}

	row, err := h.q.CreateWebhookEndpoint(c.UserContext(), sqlcgen.CreateWebhookEndpointParams{
		ID:               id.New(id.PrefixWebhookEndpoint),
		TenantID:         tenantID,
		Url:              req.URL,
		Secret:           secret,
		SecretVersion:    1,
		SubscribedEvents: subs,
		Status:           "active",
		Description:      pgxText(req.Description),
		Metadata:         []byte("{}"),
	})
	if err != nil {
		return internalError(c, err)
	}

	view := toEndpointView(row)
	return c.Status(fiber.StatusCreated).JSON(EndpointWithSecret{
		EndpointView: view,
		Secret:       base64.StdEncoding.EncodeToString(secret),
		Notice:       "Store the secret now. It cannot be retrieved later; use POST /rotate-secret to mint a new one if lost.",
	})
}

// ──────────────────────────────────────────────────────────────────────
// List + Get
// ──────────────────────────────────────────────────────────────────────

type ListEndpointsResponse struct {
	Data []EndpointView `json:"data"`
}

func (h *Webhooks) List(c *fiber.Ctx) error {
	tenantID := middleware.TenantID(c)
	if tenantID == "" {
		return badRequest(c, "missing tenant context")
	}
	limit := c.QueryInt("limit", 50)
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	offset := c.QueryInt("offset", 0)
	if offset < 0 {
		offset = 0
	}
	rows, err := h.q.ListWebhookEndpointsForTenant(c.UserContext(),
		sqlcgen.ListWebhookEndpointsForTenantParams{
			TenantID: tenantID, Limit: int32(limit), Offset: int32(offset),
		})
	if err != nil {
		return internalError(c, err)
	}
	out := make([]EndpointView, len(rows))
	for i, r := range rows {
		out[i] = toEndpointView(r)
	}
	return c.JSON(ListEndpointsResponse{Data: out})
}

func (h *Webhooks) Get(c *fiber.Ctx) error {
	tenantID := middleware.TenantID(c)
	if tenantID == "" {
		return badRequest(c, "missing tenant context")
	}
	row, err := h.q.GetWebhookEndpointByID(c.UserContext(), c.Params("id"))
	if err != nil || row.TenantID != tenantID || row.Status == "deleted" {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{
			"error": "webhook endpoint not found",
		})
	}
	return c.JSON(toEndpointView(row))
}

// ──────────────────────────────────────────────────────────────────────
// Update (PATCH)
// ──────────────────────────────────────────────────────────────────────

// UpdateEndpointRequest uses pointer fields so a missing key is "no
// change" while a null / empty value is an explicit clear. Fiber's
// BodyParser leaves omitted JSON keys as nil pointers.
type UpdateEndpointRequest struct {
	URL              *string   `json:"url,omitempty"`
	SubscribedEvents *[]string `json:"subscribed_events,omitempty"`
	Description      *string   `json:"description,omitempty"`
	Status           *string   `json:"status,omitempty"`
}

func (h *Webhooks) Update(c *fiber.Ctx) error {
	tenantID := middleware.TenantID(c)
	if tenantID == "" {
		return badRequest(c, "missing tenant context")
	}
	if role := middleware.Role(c); role != "" && role != "owner" && role != "admin" {
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"error": "your role cannot modify webhook endpoints"})
	}

	endpointID := c.Params("id")
	row, err := h.q.GetWebhookEndpointByID(c.UserContext(), endpointID)
	if err != nil || row.TenantID != tenantID || row.Status == "deleted" {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "webhook endpoint not found"})
	}

	var req UpdateEndpointRequest
	if err := c.BodyParser(&req); err != nil {
		return badRequest(c, "invalid JSON body")
	}

	// Optional fields: pgxText("") emits SQL NULL, which the COALESCE in
	// UpdateWebhookEndpoint reads as "keep the existing value".
	urlText := pgxText("")
	if req.URL != nil {
		if err := validateEndpointURL(*req.URL); err != nil {
			return badRequest(c, err.Error())
		}
		urlText = pgxText(*req.URL)
	}

	var subsBytes []byte
	if req.SubscribedEvents != nil {
		subs, err := marshalEventsList(*req.SubscribedEvents)
		if err != nil {
			return badRequest(c, err.Error())
		}
		subsBytes = subs
	}

	descText := pgxText("")
	if req.Description != nil {
		descText = pgxText(*req.Description)
	}

	statusText := pgxText("")
	if req.Status != nil {
		switch *req.Status {
		case "active", "paused":
		default:
			return badRequest(c, `status must be "active" or "paused"`)
		}
		statusText = pgxText(*req.Status)
	}

	updated, err := h.q.UpdateWebhookEndpoint(c.UserContext(), sqlcgen.UpdateWebhookEndpointParams{
		ID:               endpointID,
		Url:              urlText,
		SubscribedEvents: subsBytes, // nil → SQL COALESCE keeps current
		Description:      descText,
		Status:           statusText,
	})
	if err != nil {
		return internalError(c, err)
	}
	return c.JSON(toEndpointView(updated))
}

// ──────────────────────────────────────────────────────────────────────
// Rotate secret
// ──────────────────────────────────────────────────────────────────────

func (h *Webhooks) RotateSecret(c *fiber.Ctx) error {
	tenantID := middleware.TenantID(c)
	if tenantID == "" {
		return badRequest(c, "missing tenant context")
	}
	if role := middleware.Role(c); role != "" && role != "owner" && role != "admin" {
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"error": "your role cannot rotate webhook secrets"})
	}

	endpointID := c.Params("id")
	row, err := h.q.GetWebhookEndpointByID(c.UserContext(), endpointID)
	if err != nil || row.TenantID != tenantID || row.Status == "deleted" {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "webhook endpoint not found"})
	}

	secret := make([]byte, secretBytes)
	if _, err := rand.Read(secret); err != nil {
		return internalError(c, err)
	}
	updated, err := h.q.RotateWebhookEndpointSecret(c.UserContext(),
		sqlcgen.RotateWebhookEndpointSecretParams{
			ID: endpointID, Secret: secret,
		})
	if err != nil {
		return internalError(c, err)
	}
	view := toEndpointView(updated)
	return c.JSON(EndpointWithSecret{
		EndpointView: view,
		Secret:       base64.StdEncoding.EncodeToString(secret),
		Notice:       "Store the new secret. In-flight deliveries continue under the prior secret version until they finish.",
	})
}

// ──────────────────────────────────────────────────────────────────────
// Delete
// ──────────────────────────────────────────────────────────────────────

func (h *Webhooks) Delete(c *fiber.Ctx) error {
	tenantID := middleware.TenantID(c)
	if tenantID == "" {
		return badRequest(c, "missing tenant context")
	}
	if role := middleware.Role(c); role != "" && role != "owner" && role != "admin" {
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"error": "your role cannot delete webhook endpoints"})
	}
	endpointID := c.Params("id")
	row, err := h.q.GetWebhookEndpointByID(c.UserContext(), endpointID)
	if err != nil || row.TenantID != tenantID {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "webhook endpoint not found"})
	}
	if row.Status == "deleted" {
		return c.JSON(toEndpointView(row)) // idempotent
	}
	if err := h.q.DeleteWebhookEndpoint(c.UserContext(), endpointID); err != nil {
		return internalError(c, err)
	}
	row.Status = "deleted"
	return c.JSON(toEndpointView(row))
}

// ──────────────────────────────────────────────────────────────────────
// helpers
// ──────────────────────────────────────────────────────────────────────

// validateEndpointURL rejects anything that isn't an HTTPS URL with a host.
// Localhost http is allowed when ATARA_PAY_WEBHOOK_INSECURE=1 (intended
// for local development only).
func validateEndpointURL(raw string) error {
	if raw == "" {
		return errors.New("url is required")
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return errors.New("url is not a valid absolute URL")
	}
	if u.Scheme == "https" {
		return nil
	}
	if u.Scheme == "http" && isLocalHost(u.Host) {
		return nil
	}
	return errors.New("url must use https (http allowed only for localhost)")
}

func isLocalHost(host string) bool {
	host = strings.ToLower(host)
	host = strings.TrimSuffix(host, ":80")
	host = strings.SplitN(host, ":", 2)[0]
	return host == "localhost" || host == "127.0.0.1" || host == "::1"
}

// marshalEventsList validates each entry shape and returns a JSONB-ready
// byte slice. Event types follow the "resource.action" convention
// ("onramp.completed", "transaction.succeeded") — empty list = subscribe
// to everything.
func marshalEventsList(xs []string) ([]byte, error) {
	if xs == nil {
		return []byte("[]"), nil
	}
	for _, s := range xs {
		if s == "" {
			return nil, errors.New("subscribed_events: empty string is not a valid event type")
		}
		if !strings.Contains(s, ".") {
			return nil, errors.New("subscribed_events: each entry must look like resource.action")
		}
	}
	out, err := json.Marshal(xs)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func toEndpointView(r sqlcgen.WebhookEndpoint) EndpointView {
	v := EndpointView{
		ID:                  r.ID,
		URL:                 r.Url,
		Status:              r.Status,
		SecretVersion:       int(r.SecretVersion),
		ConsecutiveFailures: int(r.ConsecutiveFailures),
		SubscribedEvents:    parseEventsList(r.SubscribedEvents),
	}
	if r.Description.Valid {
		v.Description = r.Description.String
	}
	if r.LastSuccessAt.Valid {
		v.LastSuccessAt = r.LastSuccessAt.Time.UTC().Format("2006-01-02T15:04:05Z")
	}
	if r.LastFailureAt.Valid {
		v.LastFailureAt = r.LastFailureAt.Time.UTC().Format("2006-01-02T15:04:05Z")
	}
	if r.CreatedAt.Valid {
		v.CreatedAt = r.CreatedAt.Time.UTC().Format("2006-01-02T15:04:05Z")
	}
	return v
}

func parseEventsList(raw []byte) []string {
	if len(raw) == 0 {
		return []string{}
	}
	var out []string
	if err := json.Unmarshal(raw, &out); err != nil {
		return []string{}
	}
	return out
}

