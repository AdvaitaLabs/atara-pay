package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/spf13/cobra"
)

// client is a thin REST client. Construct once per command via newClient().
// Wraps the global timeout + auth header injection.
type client struct {
	base   string
	apiKey string
	h      *http.Client
}

func newClient(cmd *cobra.Command, requireAuth bool) (*client, error) {
	flagBase, _ := cmd.Flags().GetString("base-url")
	flagKey, _ := cmd.Flags().GetString("api-key")

	c, err := resolveConfig(flagBase, flagKey)
	if err != nil {
		return nil, err
	}
	if requireAuth && c.APIKey == "" {
		return nil, errors.New("no API key found — run `atara init`, " +
			"set ATARA_API_KEY, or pass --api-key")
	}
	return &client{
		base:   c.BaseURL,
		apiKey: c.APIKey,
		h:      &http.Client{Timeout: 30 * time.Second},
	}, nil
}

// do executes the request. body=nil → no body. Returns the raw bytes and
// HTTP status; the caller decides what shape to unmarshal into.
func (c *client) do(ctx context.Context, method, path string, body any) ([]byte, int, error) {
	var rdr io.Reader
	if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			return nil, 0, fmt.Errorf("encode body: %w", err)
		}
		rdr = bytes.NewReader(buf)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, rdr)
	if err != nil {
		return nil, 0, err
	}
	if c.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.h.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return b, resp.StatusCode, nil
}

// emit writes raw JSON to stdout. When --pretty is set we re-marshal with
// indent so agents that want machine-readable still default to compact and
// humans can opt in.
func emit(cmd *cobra.Command, raw []byte) {
	pretty, _ := cmd.Flags().GetBool("pretty")
	if !pretty {
		_, _ = os.Stdout.Write(raw)
		if len(raw) == 0 || raw[len(raw)-1] != '\n' {
			fmt.Println()
		}
		return
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		// not JSON — print as-is
		fmt.Println(string(raw))
		return
	}
	out, _ := json.MarshalIndent(v, "", "  ")
	fmt.Println(string(out))
}

// failResponse turns a non-2xx response into a clean cobra error. The
// API's standard error shape is {"error": "..."} so we try to surface
// that first; falling back to raw bytes otherwise.
func failResponse(status int, body []byte) error {
	if len(body) > 0 && body[0] == '{' {
		var s struct {
			Error string `json:"error"`
		}
		if err := json.Unmarshal(body, &s); err == nil && s.Error != "" {
			return fmt.Errorf("[%d] %s", status, s.Error)
		}
	}
	return fmt.Errorf("[%d] %s", status, string(body))
}
