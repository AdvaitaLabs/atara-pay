// Package integration drives the actual binary against a real Postgres +
// Redis stack. It's gated behind the `integration` build tag so plain
// `go test ./...` (which the developer loop and CI's fast lane use) never
// pulls in network deps.
//
// Run locally:
//
//	docker compose up -d
//	make migrate-up
//	go test -tags=integration ./tests/integration -count=1 -v
//
// Run in CI: same, with the stack provided as a job service.
//
// The smoke test exercises ONE happy-path flow end-to-end:
//   signup → mint API key → /v1/api-keys list → PUT tenant limits.
// Adding wallet-group / transaction paths needs a live CrossMint sandbox
// account; left for a follow-up integration test once the Atara test
// tenant is provisioned.
//
//go:build integration

package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/atara-xyz/atara-pay/internal/cache"
	"github.com/atara-xyz/atara-pay/internal/db"
	"github.com/atara-xyz/atara-pay/internal/router"
	"github.com/atara-xyz/atara-pay/internal/server"
	"github.com/atara-xyz/atara-pay/internal/types"
)

// dbAndRedis builds the live deps the server wants. Skips the whole test
// when DATABASE_URL or REDIS_URL is unset — keeps the test useful in CI
// without breaking local `go test ./...` runs.
func dbAndRedis(t *testing.T) (*db.Pool, *cache.Client) {
	t.Helper()
	dbURL := os.Getenv("DATABASE_URL")
	redisURL := os.Getenv("REDIS_URL")
	if dbURL == "" || redisURL == "" {
		t.Skip("integration: DATABASE_URL and REDIS_URL must be set " +
			"(run `docker compose up -d && make migrate-up` first)")
	}

	ctx := context.Background()
	pool, err := db.Connect(ctx, db.Config{URL: dbURL})
	if err != nil {
		t.Fatalf("connect pg: %v", err)
	}
	rdb, err := cache.Connect(ctx, cache.Config{URL: redisURL})
	if err != nil {
		t.Fatalf("connect redis: %v", err)
	}
	return pool, rdb
}

// uniqueEmail returns a per-run email so each test gets a fresh tenant
// without colliding on the primary_email UNIQUE.
func uniqueEmail() string {
	return fmt.Sprintf("test-%d@atara.local", time.Now().UnixNano())
}

// postJSON is a thin client over the test server. Returns status + body.
func postJSON(t *testing.T, srv *httptest.Server, path, bearer string, body any) (int, []byte) {
	t.Helper()
	buf, _ := json.Marshal(body)
	req, _ := http.NewRequest("POST", srv.URL+path, bytes.NewReader(buf))
	req.Header.Set("Content-Type", "application/json")
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST %s: %v", path, err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, b
}

func getJSON(t *testing.T, srv *httptest.Server, path, bearer string) (int, []byte) {
	t.Helper()
	req, _ := http.NewRequest("GET", srv.URL+path, nil)
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, b
}

// TestSignupAndAPIKeyFlow validates the foundational auth path against a
// real Postgres. Anything beyond this (wallet creation, transfers) needs
// upstream rail sandbox accounts; layered on later.
func TestSignupAndAPIKeyFlow(t *testing.T) {
	pool, rdb := dbAndRedis(t)
	defer pool.Close()
	defer rdb.Close()

	// Atara server with the minimum deps. No CrossMint / Tempo / Keystore
	// configured — the routes that need them stay un-mounted; the auth
	// path we exercise here doesn't care.
	r := router.New(types.RailCrossMint /* placeholder; no adapters */)
	s := server.New(server.Deps{
		Router:             r,
		Pool:               pool,
		SessionSigningKey:  bytes.Repeat([]byte("k"), 48),
		Redis:              rdb,
		RateLimitPerMinute: 0, // off — we make >1 req/min in this test
	})
	_ = s

	// Bridge fiber → httptest. fiber.App doesn't expose net/http directly,
	// so for now we use fiber's Test(req) one-shot helper through a thin
	// adapter. The simplest robust path is to start the actual server on
	// 127.0.0.1:0 and point http.Client at it.
	addr := startEphemeral(t, s)
	srv := &httptest.Server{URL: "http://" + addr}

	// ── 1. signup ─────────────────────────────────────────────────────
	email := uniqueEmail()
	password := "atara-integration-test-pass"
	status, body := postJSON(t, srv, "/signup", "", map[string]any{
		"company_name": "Integration Tester",
		"email":        email,
		"password":     password,
	})
	if status != http.StatusCreated {
		t.Fatalf("signup status = %d body=%s", status, body)
	}
	var signup struct {
		Tenant struct{ ID string } `json:"tenant"`
		APIKeys struct {
			Test struct{ Key, Prefix string } `json:"test"`
			Live struct{ Key, Prefix string } `json:"live"`
		} `json:"api_keys"`
	}
	if err := json.Unmarshal(body, &signup); err != nil {
		t.Fatalf("decode signup: %v body=%s", err, body)
	}
	if signup.Tenant.ID == "" || signup.APIKeys.Test.Key == "" {
		t.Fatalf("incomplete signup response: %s", body)
	}
	if !strings.HasPrefix(signup.APIKeys.Test.Key, "sk_test_") {
		t.Errorf("test key has wrong prefix: %q", signup.APIKeys.Test.Key)
	}

	// ── 2. list api-keys with the freshly minted test key ─────────────
	status, body = getJSON(t, srv, "/v1/api-keys", signup.APIKeys.Test.Key)
	if status != http.StatusOK {
		t.Fatalf("list api-keys status = %d body=%s", status, body)
	}
	var list struct{ Data []map[string]any }
	_ = json.Unmarshal(body, &list)
	if len(list.Data) < 2 {
		t.Errorf("expected at least 2 keys (test+live), got %d", len(list.Data))
	}

	// ── 3. switch limit tier ──────────────────────────────────────────
	status, body = putJSON(t, srv, "/v1/tenants/me/limits", signup.APIKeys.Test.Key, map[string]any{
		"tier": "standard",
	})
	if status != http.StatusOK {
		t.Fatalf("update limits status = %d body=%s", status, body)
	}

	// ── 4. duplicate signup must conflict on the same email ───────────
	status, _ = postJSON(t, srv, "/signup", "", map[string]any{
		"company_name": "Integration Tester",
		"email":        email,
		"password":     password,
	})
	if status != http.StatusConflict {
		t.Errorf("duplicate signup status = %d, want 409", status)
	}
}

// putJSON parallels postJSON for PATCH-shaped methods. Kept inline to
// avoid pulling in another helper package for one method.
func putJSON(t *testing.T, srv *httptest.Server, path, bearer string, body any) (int, []byte) {
	t.Helper()
	buf, _ := json.Marshal(body)
	req, _ := http.NewRequest("PUT", srv.URL+path, bytes.NewReader(buf))
	req.Header.Set("Content-Type", "application/json")
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("PUT %s: %v", path, err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, b
}
