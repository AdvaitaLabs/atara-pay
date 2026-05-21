package cli

import (
	"github.com/spf13/cobra"
)

func newWebhooksCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "webhooks",
		Short: "Manage outbound webhook endpoints",
	}
	cmd.AddCommand(
		webhooksCreateCmd(),
		webhooksListCmd(),
		webhooksGetCmd(),
		webhooksUpdateCmd(),
		webhooksDeleteCmd(),
		webhooksRotateSecretCmd(),
	)
	return cmd
}

func webhooksCreateCmd() *cobra.Command {
	var url, description string
	var events []string
	c := &cobra.Command{
		Use:   "create",
		Short: "POST /v1/webhook-endpoints",
		RunE: func(cmd *cobra.Command, _ []string) error {
			cl, err := newClient(cmd, true)
			if err != nil {
				return err
			}
			body, status, err := cl.do(cmd.Context(), "POST", "/v1/webhook-endpoints", map[string]any{
				"url":         url,
				"events":      events,
				"description": description,
			})
			if err != nil {
				return err
			}
			if status != 201 {
				return failResponse(status, body)
			}
			emit(cmd, body)
			return nil
		},
	}
	c.Flags().StringVar(&url, "url", "", "HTTPS endpoint to POST events to (required)")
	c.Flags().StringSliceVar(&events, "event", nil, "event type to subscribe (repeatable)")
	c.Flags().StringVar(&description, "description", "", "human label (optional)")
	_ = c.MarkFlagRequired("url")
	return c
}

func webhooksListCmd() *cobra.Command {
	var limit, offset int
	c := &cobra.Command{
		Use:   "list",
		Short: "GET /v1/webhook-endpoints",
		RunE: func(cmd *cobra.Command, _ []string) error {
			cl, err := newClient(cmd, true)
			if err != nil {
				return err
			}
			path := paginated("/v1/webhook-endpoints", limit, offset)
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

func webhooksGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "get <id>",
		Short: "GET /v1/webhook-endpoints/{id}",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cl, err := newClient(cmd, true)
			if err != nil {
				return err
			}
			body, status, err := cl.do(cmd.Context(), "GET", "/v1/webhook-endpoints/"+args[0], nil)
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

func webhooksUpdateCmd() *cobra.Command {
	var url, description string
	var events []string
	var disabled bool
	var disabledSet bool
	c := &cobra.Command{
		Use:   "update <id>",
		Short: "PATCH /v1/webhook-endpoints/{id}",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cl, err := newClient(cmd, true)
			if err != nil {
				return err
			}
			patch := map[string]any{}
			if url != "" {
				patch["url"] = url
			}
			if description != "" {
				patch["description"] = description
			}
			if len(events) > 0 {
				patch["events"] = events
			}
			if disabledSet {
				patch["disabled"] = disabled
			}
			body, status, err := cl.do(cmd.Context(), "PATCH",
				"/v1/webhook-endpoints/"+args[0], patch)
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
	c.Flags().StringVar(&url, "url", "", "new endpoint URL")
	c.Flags().StringVar(&description, "description", "", "new description")
	c.Flags().StringSliceVar(&events, "event", nil, "replace event subscription list")
	c.Flags().BoolVar(&disabled, "disabled", false, "disable/enable the endpoint")
	// Cobra exposes whether the flag was set via Changed; capture in PreRun.
	c.PreRun = func(cmd *cobra.Command, _ []string) {
		disabledSet = cmd.Flags().Changed("disabled")
	}
	return c
}

func webhooksDeleteCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "delete <id>",
		Short: "DELETE /v1/webhook-endpoints/{id}",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cl, err := newClient(cmd, true)
			if err != nil {
				return err
			}
			body, status, err := cl.do(cmd.Context(), "DELETE",
				"/v1/webhook-endpoints/"+args[0], nil)
			if err != nil {
				return err
			}
			if status != 200 && status != 204 {
				return failResponse(status, body)
			}
			emit(cmd, body)
			return nil
		},
	}
}

func webhooksRotateSecretCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "rotate-secret <id>",
		Short: "POST /v1/webhook-endpoints/{id}/rotate-secret (returns new secret ONCE)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cl, err := newClient(cmd, true)
			if err != nil {
				return err
			}
			body, status, err := cl.do(cmd.Context(), "POST",
				"/v1/webhook-endpoints/"+args[0]+"/rotate-secret", nil)
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
