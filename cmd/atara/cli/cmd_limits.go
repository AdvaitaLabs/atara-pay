package cli

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

func newLimitsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "limits",
		Short: "Read or update tenant-level spending limits",
	}
	cmd.AddCommand(limitsGetCmd(), limitsUpdateCmd(), limitsViolationsCmd())
	return cmd
}

func limitsGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "get",
		Short: "GET /v1/tenants/me/limits",
		RunE: func(cmd *cobra.Command, _ []string) error {
			cl, err := newClient(cmd, true)
			if err != nil {
				return err
			}
			body, status, err := cl.do(cmd.Context(), "GET", "/v1/tenants/me/limits", nil)
			if err != nil {
				return err
			}
			if status != 200 {
				return failResponse(status, body)
			}
			emit(cmd, body)
			return nil
		},
	}
}

// limitsUpdateCmd accepts either a single --json blob (matches the API shape
// 1:1) or a path read from --file. Keeping it raw avoids drift if the server
// schema gains a new field.
func limitsUpdateCmd() *cobra.Command {
	var jsonBlob, file string
	c := &cobra.Command{
		Use:   "update",
		Short: "PUT /v1/tenants/me/limits (replace policy)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if (jsonBlob == "" && file == "") || (jsonBlob != "" && file != "") {
				return fmt.Errorf("exactly one of --json or --file is required")
			}
			raw := []byte(jsonBlob)
			if file != "" {
				b, err := os.ReadFile(file)
				if err != nil {
					return fmt.Errorf("read %s: %w", file, err)
				}
				raw = b
			}
			var payload any
			if err := json.Unmarshal(raw, &payload); err != nil {
				return fmt.Errorf("invalid JSON: %w", err)
			}
			cl, err := newClient(cmd, true)
			if err != nil {
				return err
			}
			body, status, err := cl.do(cmd.Context(), "PUT", "/v1/tenants/me/limits", payload)
			if err != nil {
				return err
			}
			if status != 200 {
				return failResponse(status, body)
			}
			emit(cmd, body)
			return nil
		},
	}
	c.Flags().StringVar(&jsonBlob, "json", "", "limit policy JSON literal")
	c.Flags().StringVar(&file, "file", "", "path to JSON file containing the policy")
	return c
}

func limitsViolationsCmd() *cobra.Command {
	var limit, offset int
	c := &cobra.Command{
		Use:   "violations",
		Short: "GET /v1/tenants/me/limits/violations",
		RunE: func(cmd *cobra.Command, _ []string) error {
			cl, err := newClient(cmd, true)
			if err != nil {
				return err
			}
			path := paginated("/v1/tenants/me/limits/violations", limit, offset)
			body, status, err := cl.do(cmd.Context(), "GET", path, nil)
			if err != nil {
				return err
			}
			if status != 200 {
				return failResponse(status, body)
			}
			emit(cmd, body)
			return nil
		},
	}
	c.Flags().IntVar(&limit, "limit", 50, "rows to return")
	c.Flags().IntVar(&offset, "offset", 0, "rows to skip")
	return c
}
