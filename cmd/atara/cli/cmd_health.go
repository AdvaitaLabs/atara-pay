package cli

import (
	"github.com/spf13/cobra"
)

func newHealthCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "health",
		Short: "Hit GET /health and print the response",
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, err := newClient(cmd, false) // no auth required
			if err != nil {
				return err
			}
			body, status, err := c.do(cmd.Context(), "GET", "/health", nil)
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
