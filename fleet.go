// fleet.go is the cli's PAID-TIER fleet view (D-0010 Endpoint 2). Unlike the
// `view` command — which is strictly local and egress-free (it reads only the
// agent's 127.0.0.1 API) — `fleet` is an EXPLICIT, opt-in HTTPS egress to the
// closed control plane's /v1/fleet endpoint. It is the one cli path that talks to
// a control plane, and only when the operator supplies its URL + a license token.
//
// cli still adjudicates nothing: it GETs the open gpufleet.v1.AggregationEnvelope
// (open Verdicts keyed by node/job/device + counts) and renders it VERBATIM,
// sorted, no wall-clock. The closed roll-up math never crosses the wire; cli
// never recomputes a class, count, or cost.
package cli

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"

	gpufleetv1 "github.com/rocker-zhang/gpufleet-proto/gen/go/gpufleet/v1"
	"google.golang.org/protobuf/encoding/protojson"

	"github.com/spf13/cobra"
)

// FleetClient is a read-only client for the closed control plane's paid fleet
// endpoint. It performs only a GET; it sends a license token and never writes,
// uploads telemetry, or controls anything. It is DISTINCT from the local-only
// Client so the egress boundary is unmistakable in the type system.
type FleetClient struct {
	// Endpoint is the control plane base URL (e.g. https://cp.gpufleet.sg). There
	// is NO localhost default: paid egress must be opted into explicitly.
	Endpoint string
	// Token is the operator's license token, sent as a Bearer credential.
	Token string
	HTTP  *http.Client
}

// NewFleetClient builds a paid-tier fleet client. endpoint and token are
// required; an empty endpoint is a usage error surfaced by Fleet.
func NewFleetClient(endpoint, token string) *FleetClient {
	return &FleetClient{
		Endpoint: strings.TrimRight(endpoint, "/"),
		Token:    token,
		HTTP:     &http.Client{Timeout: 10 * time.Second},
	}
}

// Fleet GETs /v1/fleet and decodes the OPEN AggregationEnvelope via protojson —
// the same canonical wire the control plane marshals. It maps the paid-tier
// failure modes to clear operator errors (missing endpoint, 401/402 license,
// other non-200) rather than a raw HTTP error.
func (c *FleetClient) Fleet(ctx context.Context) (*gpufleetv1.AggregationEnvelope, error) {
	if c.Endpoint == "" {
		return nil, fmt.Errorf("cli fleet: no control-plane URL set (use --controlplane or GPUFLEET_CONTROLPLANE_URL)")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.Endpoint+"/v1/fleet", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("cli fleet: GET /v1/fleet: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("cli fleet: read response: %w", err)
	}
	switch resp.StatusCode {
	case http.StatusOK:
		// fall through to decode
	case http.StatusPaymentRequired, http.StatusUnauthorized:
		return nil, fmt.Errorf("cli fleet: control plane requires a valid license (HTTP %d) — check --token", resp.StatusCode)
	default:
		return nil, fmt.Errorf("cli fleet: control plane returned HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	env := &gpufleetv1.AggregationEnvelope{}
	if err := protojson.Unmarshal(body, env); err != nil {
		return nil, fmt.Errorf("cli fleet: parse /v1/fleet as gpufleet.v1.AggregationEnvelope: %w", err)
	}
	return env, nil
}

// RenderFleet produces a deterministic fleet table from the open envelope. Rows
// are sorted by node id (the control plane already sorts; cli re-sorts a copy
// defensively so output is stable regardless of producer). Every value — class,
// confidence, counts — is passed through verbatim; cli computes nothing.
func RenderFleet(env *gpufleetv1.AggregationEnvelope, controlplane string, fullUUID bool) string {
	var b strings.Builder
	fmt.Fprintf(&b, "gpufleet FLEET view  (source=control plane %s, contract=%s)\n",
		controlplane, env.GetContractVersion())

	if ws := env.GetWindowStart(); ws != nil {
		fmt.Fprintf(&b, "window: %s .. %s\n",
			ws.AsTime().UTC().Format(time.RFC3339),
			env.GetWindowEnd().AsTime().UTC().Format(time.RFC3339))
	} else {
		fmt.Fprintf(&b, "window: (no nodes reporting yet)\n")
	}

	entries := append([]*gpufleetv1.FleetEntry(nil), env.GetEntries()...)
	sort.Slice(entries, func(i, j int) bool { return entries[i].GetNodeId() < entries[j].GetNodeId() })

	if len(entries) == 0 {
		fmt.Fprintf(&b, "\n(no nodes have reported a verdict to the control plane yet)\n")
		return b.String()
	}

	fmt.Fprintf(&b, "\nNODES\n")
	t := newTable(
		[]string{"node", "job", "device", "rca", "confidence"},
		[]bool{false, false, false, false, true},
	)
	for _, e := range entries {
		v := e.GetVerdict()
		fc := v.GetFaultClass()
		// ABSTAIN/UNSPECIFIED render as ABSTAIN with no confidence number — an
		// unspecified class is never presented as a fired fault (mirrors view.go).
		rca := faultClassDisplay(fc)
		conf := fmt.Sprintf("%.2f", v.GetConfidence())
		if fc == gpufleetv1.FaultClass_FAULT_CLASS_ABSTAIN || fc == gpufleetv1.FaultClass_FAULT_CLASS_UNSPECIFIED {
			rca = "ABSTAIN"
			conf = "-"
		}
		device := e.GetDeviceUuid()
		if !fullUUID && device != "" {
			device = shortUUID(device)
		}
		if device == "" {
			device = "-"
		}
		job := e.GetJobId()
		if job == "" {
			job = "-"
		}
		t.row(e.GetNodeId(), job, device, rca, conf)
	}
	t.flush(&b)

	fmt.Fprintf(&b, "\nfleet summary: %d node(s)  %d faulted  %d abstained\n",
		env.GetTotalNodes(), env.GetFaultedNodes(), env.GetAbstainedNodes())
	fmt.Fprintf(&b, "note: deep RCA + narration is the closed control plane; this view is open Verdicts (class + cited signals + confidence) keyed by fleet dimension.\n")
	return b.String()
}

// newFleetCmd builds the `gpufleet fleet` subcommand: the paid-tier control-plane
// fleet view. It is the cli's only egress path and requires an explicit URL.
func newFleetCmd() *cobra.Command {
	var controlplane, token string
	var fullUUID bool
	cmd := &cobra.Command{
		Use:   "fleet",
		Short: "PAID: read the control plane's /v1/fleet and print a deterministic fleet view",
		Long: "fleet is the paid-tier fleet view. Unlike `view` (local, egress-free),\n" +
			"it makes an explicit HTTPS request to the closed control plane's\n" +
			"/v1/fleet endpoint and renders the open AggregationEnvelope it returns.\n" +
			"Requires --controlplane (or GPUFLEET_CONTROLPLANE_URL) and a license\n" +
			"--token (or GPUFLEET_LICENSE_TOKEN).",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if controlplane == "" {
				controlplane = os.Getenv("GPUFLEET_CONTROLPLANE_URL")
			}
			if token == "" {
				token = os.Getenv("GPUFLEET_LICENSE_TOKEN")
			}
			c := NewFleetClient(controlplane, token)
			env, err := c.Fleet(cmd.Context())
			if err != nil {
				return err
			}
			fmt.Fprint(cmd.OutOrStdout(), RenderFleet(env, controlplane, fullUUID))
			return nil
		},
	}
	cmd.Flags().StringVar(&controlplane, "controlplane", "",
		"control plane base URL for the paid fleet endpoint (or GPUFLEET_CONTROLPLANE_URL)")
	cmd.Flags().StringVar(&token, "token", "",
		"license token for the paid fleet endpoint (or GPUFLEET_LICENSE_TOKEN)")
	cmd.Flags().BoolVar(&fullUUID, "full-uuid", false,
		"show the full device UUID instead of the default short prefix")
	return cmd
}
