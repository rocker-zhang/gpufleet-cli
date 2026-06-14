package cli

import (
	"fmt"

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
	var fullUUID bool
	viewCmd := &cobra.Command{
		Use:   "view",
		Short: "Read the agent's local API (/signals + /cost) and print a deterministic util/cost view",
		RunE: func(cmd *cobra.Command, _ []string) error {
			c := NewClient(endpoint)
			ctx := cmd.Context()

			// Fetch /cost first: Client.Cost degrades a never-collected agent
			// (which serves 503/empty on /signals before any window exists) into a
			// NeverCollected empty state instead of erroring (TASK-0041). A real
			// transport error here (agent down / connection refused) is a genuine
			// failure and is returned so the entrypoint reports it on STDERR.
			cost, err := c.Cost(ctx)
			if err != nil {
				return err
			}

			// /signals is only needed for the degrade-marks cross-check and the
			// agent id. In the no-data state the agent has no signal window yet and
			// answers /signals with a non-200, so a /signals error there is NORMAL,
			// not fatal: render the friendly NO-DATA banner off /cost with a nil
			// pack (exactly what RenderView(nil, cost) already produces). Only when
			// /cost has real data do we treat a /signals failure as a hard error.
			pack, serr := c.Signals(ctx)
			if serr != nil {
				noData := cost == nil || cost.NeverCollected || (cost.Stale && len(cost.Devices) == 0)
				if !noData {
					return serr
				}
				pack = nil
			}

			// /verdict is the window-level RCA Verdict from the agent's local open
			// gate (TASK-0049). It is ADDITIVE to the view: Client.Verdict folds a
			// pre-window 503 or an unreachable agent into a nil verdict ("no verdict
			// yet"), so the banner degrades gracefully and a missing verdict never
			// fails the command. Only a malformed 200 body (wire drift) errors.
			verdict, verr := c.Verdict(ctx)
			if verr != nil {
				return verr
			}

			// The rendered view is the command's PRIMARY output and MUST go to
			// STDOUT so it is pipeable / capturable / redirectable (TASK-0042).
			// The friendly NO-DATA and STALE banners are NORMAL output and ride
			// the same stdout stream; only real errors (returned from RunE and
			// printed by main to os.Stderr) belong on stderr. Note cmd.Print uses
			// cobra's OutOrStderr() fallback (= os.Stderr when no writer is set),
			// which is exactly the stream-separation bug — write to OutOrStdout()
			// explicitly instead.
			render := RenderView
			if fullUUID {
				render = RenderViewFull
			}
			fmt.Fprint(cmd.OutOrStdout(), render(pack, cost, verdict))
			return nil
		},
	}
	viewCmd.Flags().StringVar(&endpoint, "endpoint", DefaultEndpoint,
		"agent local read-only API base URL (the agent serves -addr 127.0.0.1:9577)")
	viewCmd.Flags().BoolVar(&fullUUID, "full-uuid", false,
		"show the full device UUID instead of the default short prefix")

	root.AddCommand(viewCmd)
	return root
}
