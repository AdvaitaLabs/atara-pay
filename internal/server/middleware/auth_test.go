package middleware

import (
	"context"
	"errors"
	"io"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/gofiber/fiber/v2"

	"github.com/atara-xyz/atara-pay/internal/auth"
	"github.com/atara-xyz/atara-pay/internal/db/sqlcgen"
)

// fakeQueries is an in-memory stand-in for sqlcgen.Querier — just enough to
// drive the middleware.
type fakeQueries struct {
	mu       sync.Mutex
	keys     map[string]sqlcgen.ApiKey // keyed by string(hash)
	touched  []string                  // ids passed to TouchAPIKeyUsage
	touchErr error
}

func (f *fakeQueries) GetAPIKeyByHash(_ context.Context, h []byte) (sqlcgen.ApiKey, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	row, ok := f.keys[string(h)]
	if !ok {
		return sqlcgen.ApiKey{}, errors.New("not found")
	}
	return row, nil
}

func (f *fakeQueries) TouchAPIKeyUsage(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.touched = append(f.touched, id)
	return f.touchErr
}

// buildApp wires the middleware in front of a tiny echo handler.
func buildApp(q Queries) *fiber.App {
	app := fiber.New(fiber.Config{DisableStartupMessage: true})
	app.Use(APIKeyAuth(q))
	app.Get("/ok", func(c *fiber.Ctx) error {
		return c.JSON(fiber.Map{
			"tenant_id": TenantID(c),
			"key_id":    APIKeyID(c),
			"env":       Environment(c),
		})
	})
	return app
}

// mustMintAndStore creates a key in fakeQueries and returns the raw value.
func mustMintAndStore(t *testing.T, f *fakeQueries, env, tenantID, keyID string) string {
	t.Helper()
	raw, prefix, hash, err := auth.MintAPIKey(env)
	if err != nil {
		t.Fatalf("MintAPIKey: %v", err)
	}
	f.keys[string(hash)] = sqlcgen.ApiKey{
		ID:          keyID,
		TenantID:    tenantID,
		Environment: env,
		KeyPrefix:   prefix,
		KeyHash:     hash,
	}
	return raw
}

func TestAuth_Success(t *testing.T) {
	f := &fakeQueries{keys: map[string]sqlcgen.ApiKey{}}
	raw := mustMintAndStore(t, f, "test", "tn_xyz", "ak_abc")

	app := buildApp(f)
	req := httptest.NewRequest("GET", "/ok", nil)
	req.Header.Set("Authorization", "Bearer "+raw)
	resp, err := app.Test(req, -1)
	if err != nil {
		t.Fatalf("Test: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d body=%s", resp.StatusCode, body)
	}
	want := `{"env":"test","key_id":"ak_abc","tenant_id":"tn_xyz"}`
	if string(body) != want {
		t.Errorf("body=%s\nwant=%s", body, want)
	}
}

func TestAuth_MissingHeader(t *testing.T) {
	f := &fakeQueries{keys: map[string]sqlcgen.ApiKey{}}
	app := buildApp(f)
	req := httptest.NewRequest("GET", "/ok", nil)
	resp, _ := app.Test(req, -1)
	if resp.StatusCode != 401 {
		t.Errorf("missing header should 401, got %d", resp.StatusCode)
	}
	if resp.Header.Get("WWW-Authenticate") == "" {
		t.Errorf("missing WWW-Authenticate challenge header")
	}
}

func TestAuth_MalformedHeader(t *testing.T) {
	f := &fakeQueries{keys: map[string]sqlcgen.ApiKey{}}
	app := buildApp(f)
	for _, h := range []string{
		"Basic abc",         // wrong scheme
		"Bearer ",           // empty token
		"Bearer not-a-key",  // bad format
		"sk_test_xxxxxxxx",  // missing "Bearer "
	} {
		req := httptest.NewRequest("GET", "/ok", nil)
		req.Header.Set("Authorization", h)
		resp, _ := app.Test(req, -1)
		if resp.StatusCode != 401 {
			t.Errorf("header %q should 401, got %d", h, resp.StatusCode)
		}
	}
}

func TestAuth_UnknownKey(t *testing.T) {
	f := &fakeQueries{keys: map[string]sqlcgen.ApiKey{}}
	// Mint but don't store, so the lookup misses.
	raw, _, _, _ := auth.MintAPIKey("test")

	app := buildApp(f)
	req := httptest.NewRequest("GET", "/ok", nil)
	req.Header.Set("Authorization", "Bearer "+raw)
	resp, _ := app.Test(req, -1)
	if resp.StatusCode != 401 {
		t.Errorf("unknown key should 401, got %d", resp.StatusCode)
	}
}

func TestAuth_EnvironmentMismatch(t *testing.T) {
	// Store a row claiming env=live but with a key minted for test — the
	// middleware must reject (defensive against db tampering).
	f := &fakeQueries{keys: map[string]sqlcgen.ApiKey{}}
	raw, prefix, hash, _ := auth.MintAPIKey("test")
	f.keys[string(hash)] = sqlcgen.ApiKey{
		ID: "ak_x", TenantID: "tn_x",
		Environment: "live",
		KeyPrefix:   prefix, KeyHash: hash,
	}

	app := buildApp(f)
	req := httptest.NewRequest("GET", "/ok", nil)
	req.Header.Set("Authorization", "Bearer "+raw)
	resp, _ := app.Test(req, -1)
	if resp.StatusCode != 401 {
		t.Errorf("env mismatch should 401, got %d", resp.StatusCode)
	}
}
