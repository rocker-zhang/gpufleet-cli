// Package cli is a read-only BYPASS viewer for the gpufleet agent's local HTTP
// API (D-0010 Endpoint 1). It HTTP-GETs the agent's /signals (canonical
// protojson of the REAL gpufleet.v1.EvidencePack gen type) and /cost (the
// standalone cost wedge JSON) and renders a deterministic single-node table.
//
// cli is OFF the critical path (RULES §A; D-0008/D-0010): it NEVER assembles an
// evidence pack, NEVER originates HTTPS egress, NEVER talks to a controlplane or
// receives its Verdict, and NEVER writes back / controls anything. It only GETs
// the agent's local API and renders. Rendering is deterministic (sorted, no
// wall-clock) and passes the agent's values through verbatim — it fabricates no
// value the agent did not send, and surfaces missing-field degrade marks.
package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	gpufleetv1 "github.com/rocker-zhang/gpufleet-proto/gen/go/gpufleet/v1"
	"google.golang.org/protobuf/encoding/protojson"
)

// DefaultEndpoint is the documented fixed localhost the agent serves on
// (`agent -serve -addr 127.0.0.1:9577`). cli only ever reads this; it is a
// local, off-path read and never an egress to any control plane.
const DefaultEndpoint = "http://127.0.0.1:9577"

// DeviceCost mirrors the agent /cost JSON shape for one device's cost wedge.
// It is a thin wire DTO for decoding the agent's HTTP response — NOT a proto
// mirror. The agent computes these values; cli passes them through verbatim.
type DeviceCost struct {
	UUID         string  `json:"uuid"`
	Node         string  `json:"node"`
	MFU          float64 `json:"mfu"`
	TensorActive float64 `json:"tensor_active"`
	IdleFraction float64 `json:"idle_fraction"`
	CostUSD      float64 `json:"cost_usd"`
	// WastedUSD is per-WINDOW waste; UsdPerHour is the per-HOUR burn RATE. Both
	// are meaningful ONLY when Priced==true (agent CLAUDE.md §5a); when Priced is
	// false they are zero and carry no meaning, so the viewer degrade-marks them.
	WastedUSD      float64 `json:"wasted_usd"`
	UsdPerHour     float64 `json:"usd_per_hour"`
	Priced         bool    `json:"priced"`
	LowUtilization bool    `json:"low_utilization"`
}

// JobCost mirrors the agent /cost JSON shape for one job's aggregated wedge.
type JobCost struct {
	JobID      string  `json:"job_id"`
	WastedUSD  float64 `json:"wasted_usd"`
	UsdPerHour float64 `json:"usd_per_hour"`
	Priced     bool    `json:"priced"`
	Devices    int     `json:"devices"`
}

// CostResponse mirrors the agent /cost payload.
type CostResponse struct {
	Devices []DeviceCost `json:"devices"`
	Jobs    []JobCost    `json:"jobs"`
}

// Client is a read-only HTTP client for the agent's local API. It performs only
// GETs; it has no method that writes, uploads, or controls anything.
type Client struct {
	Endpoint string
	HTTP     *http.Client
}

// NewClient builds a read-only client for the given endpoint. An empty endpoint
// falls back to DefaultEndpoint.
func NewClient(endpoint string) *Client {
	if endpoint == "" {
		endpoint = DefaultEndpoint
	}
	return &Client{
		Endpoint: strings.TrimRight(endpoint, "/"),
		HTTP:     &http.Client{Timeout: 5 * time.Second},
	}
}

func (c *Client) get(ctx context.Context, path string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.Endpoint+path, nil)
	if err != nil {
		return nil, err
	}
	// Defensive, contract-explicit: cli only ever GETs JSON from the agent's
	// local read-only API. Still GET-only — no body, no mutating verb.
	req.Header.Set("Accept", "application/json")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("cli: GET %s: %w", path, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("cli: read %s: %w", path, err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("cli: GET %s: status %d: %s", path, resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return body, nil
}

// Signals GETs /signals and unmarshals it into the REAL gpufleet.v1.EvidencePack
// gen type via protojson. cli is the 3rd real consumer of the gen module; it does
// NOT hand-roll a proto mirror.
func (c *Client) Signals(ctx context.Context) (*gpufleetv1.EvidencePack, error) {
	body, err := c.get(ctx, "/signals")
	if err != nil {
		return nil, err
	}
	var pack gpufleetv1.EvidencePack
	if err := protojson.Unmarshal(body, &pack); err != nil {
		return nil, fmt.Errorf("cli: parse /signals as gpufleet.v1.EvidencePack: %w", err)
	}
	return &pack, nil
}

// Cost GETs /cost and decodes the agent's standalone cost-wedge JSON.
func (c *Client) Cost(ctx context.Context) (*CostResponse, error) {
	body, err := c.get(ctx, "/cost")
	if err != nil {
		return nil, err
	}
	var cost CostResponse
	if err := json.Unmarshal(body, &cost); err != nil {
		return nil, fmt.Errorf("cli: parse /cost JSON: %w", err)
	}
	return &cost, nil
}

// degradeMark is a missing-field degradation surfaced to the viewer. cli does
// NOT invent these: a mark exists only where the agent's own state implies a
// missing fact — an unpriced device (Priced==false → cost unknown), or a device
// the agent mapped in /signals but omitted from /cost (its MFU inputs degraded,
// so the agent did not fabricate a wedge). This mirrors the agent's own
// "omit, don't fabricate" contract.
type degradeMark struct {
	DeviceUUID string
	Field      string
	Reason     string
}

// degradeMarks derives the missing-field marks deterministically from what the
// agent actually sent across /signals and /cost. It fabricates no value.
func degradeMarks(pack *gpufleetv1.EvidencePack, cost *CostResponse) []degradeMark {
	costed := map[string]bool{}
	for _, d := range cost.Devices {
		costed[d.UUID] = true
	}
	var marks []degradeMark
	// Devices the agent mapped but did NOT emit a cost wedge for: the agent
	// degraded their MFU inputs and omitted them rather than fabricate a wedge.
	if pack != nil {
		for _, m := range pack.GetMappings() {
			u := m.GetDeviceUuid()
			if u != "" && !costed[u] {
				marks = append(marks, degradeMark{u, "mfu", "agent omitted device from /cost (MFU inputs degraded)"})
			}
		}
	}
	// Devices the agent emitted but could not price.
	for _, d := range cost.Devices {
		if !d.Priced {
			marks = append(marks, degradeMark{d.UUID, "cost", "agent reports device unpriced (no $/hour rate)"})
		}
	}
	sort.Slice(marks, func(i, j int) bool {
		if marks[i].DeviceUUID != marks[j].DeviceUUID {
			return marks[i].DeviceUUID < marks[j].DeviceUUID
		}
		return marks[i].Field < marks[j].Field
	})
	return marks
}

// RenderView produces a deterministic, human-readable single-node view from the
// agent's /signals EvidencePack and /cost wedge. Output is sorted (by device
// UUID, then job id) and carries no wall-clock value, so the same inputs render
// byte-identically. The Verdict column is fixed "n/a (no control plane)" — cli
// has no rca/gate and NEVER fabricates a verdict (D-0008/D-0010).
func RenderView(pack *gpufleetv1.EvidencePack, cost *CostResponse) string {
	var b strings.Builder

	agentID := ""
	if pack != nil {
		agentID = pack.GetAgentId()
	}
	fmt.Fprintf(&b, "gpufleet single-node view  (agent=%s, source=local read-only API)\n", agentID)

	// Deterministic device table, sorted by UUID.
	devs := append([]DeviceCost(nil), cost.Devices...)
	sort.Slice(devs, func(i, j int) bool { return devs[i].UUID < devs[j].UUID })

	fmt.Fprintf(&b, "\nDEVICES\n")
	// Two distinct money columns straight from the agent's /cost wire (agent
	// CLAUDE.md §5a): `waste(win)` is the per-WINDOW waste (`wasted_usd`) and
	// `$/hr` is the per-HOUR burn RATE (`usd_per_hour`, semantics
	// CostImpact.UsdPerHour). For an idle device over a sub-hour window the $/hr
	// rate exceeds the windowed waste. cli passes both through verbatim; when the
	// agent reports priced==false (no $/hr rate) it shows a degrade mark `n/a`,
	// NEVER a fabricated $0.
	fmt.Fprintf(&b, "  %-20s %-10s %6s %8s %12s %12s %8s  %s\n",
		"device", "node", "mfu", "tensor", "waste(win)", "$/hr", "lowutil", "verdict")
	for _, d := range devs {
		low := "-"
		if d.LowUtilization {
			low = "LOW"
		}
		wasted := fmt.Sprintf("$%.4f", d.WastedUSD)
		perHour := fmt.Sprintf("$%.4f", d.UsdPerHour)
		if !d.Priced {
			// priced==false ⇒ the $ fields carry no meaning; degrade-mark, not $0.
			wasted = "n/a"
			perHour = "n/a"
		}
		fmt.Fprintf(&b, "  %-20s %-10s %6.3f %8.3f %12s %12s %8s  %s\n",
			d.UUID, d.Node, d.MFU, d.TensorActive, wasted, perHour, low,
			"n/a (no control plane)")
	}

	// Deterministic job table, sorted by job id.
	jobs := append([]JobCost(nil), cost.Jobs...)
	sort.Slice(jobs, func(i, j int) bool { return jobs[i].JobID < jobs[j].JobID })
	if len(jobs) > 0 {
		fmt.Fprintf(&b, "\nJOBS\n")
		fmt.Fprintf(&b, "  %-20s %12s %12s %8s %8s\n", "job", "waste(win)", "$/hr", "priced", "devices")
		for _, j := range jobs {
			wasted := fmt.Sprintf("$%.4f", j.WastedUSD)
			perHour := fmt.Sprintf("$%.4f", j.UsdPerHour)
			priced := "yes"
			if !j.Priced {
				wasted = "n/a"
				perHour = "n/a"
				priced = "no"
			}
			fmt.Fprintf(&b, "  %-20s %12s %12s %8s %8d\n", j.JobID, wasted, perHour, priced, j.Devices)
		}
	}

	// Missing-field degrade marks, passed through from the agent's own state.
	marks := degradeMarks(pack, cost)
	if len(marks) > 0 {
		fmt.Fprintf(&b, "\nDEGRADED (missing-field marks from agent)\n")
		for _, m := range marks {
			fmt.Fprintf(&b, "  %-20s %-12s %s\n", m.DeviceUUID, m.Field, m.Reason)
		}
	}

	return b.String()
}
