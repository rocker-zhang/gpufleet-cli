package cli

import (
	"github.com/rocker-zhang/gpufleet-agent"
	"github.com/spf13/cobra"
)

// NewRootCmd builds the gpufleet CLI. It wires agent + semantics (+ rca via the
// rca subpackage) locally; no control plane is required.
func NewRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "gpufleet",
		Short: "gpufleet local CLI — job-level utilization, cost, and deterministic RCA",
		Long: "gpufleet wires the open collector, cost/efficiency semantics, and the\n" +
			"deterministic RCA engine locally. It is standalone-useful with no\n" +
			"control plane and contains no closed logic.",
		SilenceUsage: true,
	}

	var node, jobID string
	viewCmd := &cobra.Command{
		Use:   "view",
		Short: "Collect evidence (mock by default) and print a job-level util/cost view",
		RunE: func(cmd *cobra.Command, _ []string) error {
			ev, err := agent.Collect(agent.DefaultReader(node))
			if err != nil {
				return err
			}
			out, err := RenderJobView(jobID, ev)
			if err != nil {
				return err
			}
			cmd.Print(out)
			return nil
		},
	}
	viewCmd.Flags().StringVar(&node, "node", "", "node name to stamp on evidence")
	viewCmd.Flags().StringVar(&jobID, "job", "local-job", "job id to attribute devices to")

	root.AddCommand(viewCmd)
	return root
}
