// Package crossmint implements the Atara-Pay Adapter for CrossMint's REST API.
//
// Reference: https://docs.crossmint.com
// Base URL:
//   Production: https://www.crossmint.com
//   Staging:    https://staging.crossmint.com
// Auth: x-api-key header.
package crossmint

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	paygwerr "github.com/atara-xyz/atara-pay/internal/errors"
)

const (
	defaultBaseURL = "https://www.crossmint.com"
	stagingBaseURL = "https://staging.crossmint.com"
	apiVersion     = "2025-06-09"
	httpTimeout    = 30 * time.Second
)

// Client is a thin REST client for the CrossMint API. It is intentionally
// stateless: all auth lives in the API key configured at construction time.
type Client struct {
	baseURL string
	apiKey  string
	http    *http.Client
}

// Config configures a CrossMint Client.
type Config struct {
	APIKey  string
	BaseURL string // optional; defaults to production
}

// NewClient builds a Client. APIKey is required.
func NewClient(cfg Config) (*Client, error) {
	if cfg.APIKey == "" {
		return nil, fmt.Errorf("crossmint: APIKey is required")
	}
	base := cfg.BaseURL
	if base == "" {
		base = defaultBaseURL
	}
	return &Client{
		baseURL: base,
		apiKey:  cfg.APIKey,
		http:    &http.Client{Timeout: httpTimeout},
	}, nil
}

// do is the single point of HTTP egress. It marshals body, sets headers,
// surfaces structured upstream errors, and decodes JSON into out.
func (c *Client) do(ctx context.Context, method, path string, body, out any) error {
	var rdr io.Reader
	if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("marshal request: %w", err)
		}
		rdr = bytes.NewReader(buf)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, rdr)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("x-api-key", c.apiKey)
	req.Header.Set("x-client-name", "atara-pay")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("crossmint request: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)

	if resp.StatusCode >= 400 {
		return &paygwerr.UpstreamError{
			Rail:   "crossmint",
			Status: resp.StatusCode,
			Body:   string(respBody),
		}
	}

	if out == nil || len(respBody) == 0 {
		return nil
	}
	if err := json.Unmarshal(respBody, out); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	return nil
}
