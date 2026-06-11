package cli

import (
	"github.com/spf13/cobra"
)

// NewRootCmd builds the gpufleet read-only bypass CLI. It is a pure VIEWER: it
// HTTP-GETs the agent's local read-only API (D-0010 Endpoint 1) and renders a
// deterministic single-node util/cost view. It assembles no evidence pack,
// originates no HTTPS egress, talks to no control plane, and writes nothing back.
func NewRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "gpufleet",
		Short: "gpufleet read-only viewer — job-level utilization & $cost from the agent's local API",
		Long: "gpufleet is a read-only BYPASS viewer. It reads the agent's local\n" +
			"read-only HTTP API (/signals + /cost) and renders a deterministic\n" +
			"single-node utilization/cost view. It is off the critical path: it\n" +
			"assembles no evidence pack, originates no egress, contacts no control\n" +
			"plane, and never writes back. No control plane is required.",
		SilenceUsage: true,
	}

	var endpoint string
	viewCmd := &cobra.Command{
		Use:   "view",
		Short: "Read the agent's local API (/signals + /cost) and print a deterministic util/cost view",
		RunE: func(cmd *cobra.Command, _ []string) error {
			c := NewClient(endpoint)
			ctx := cmd.Context()

			pack, err := c.Signals(ctx)
			if err != nil {
				return err
			}
			cost, err := c.Cost(ctx)
			if err != nil {
				return err
			}
			cmd.Print(RenderView(pack, cost))
			return nil
		},
	}
	viewCmd.Flags().StringVar(&endpoint, "endpoint", DefaultEndpoint,
		"agent local read-only API base URL (the agent serves -addr 127.0.0.1:9577)")

	root.AddCommand(viewCmd)
	return root
}
