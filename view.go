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
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	gpufleetv1 "github.com/rocker-zhang/gpufleet-proto/gen/go/gpufleet/v1"
	"google.golang.org/protobuf/encoding/protojson"
)

// shortUUIDLen is the number of leading characters of a device UUID kept in the
// default (short) render. A typical NVML UUID is "GPU-<8-4-4-4-12 hex>" (40
// chars incl. the "GPU-" prefix); the first 13 chars ("GPU-" + the first hex
// block, e.g. "GPU-1e760802-") are enough to recognize a device at a glance
// while keeping the DEVICES table narrow. The full UUID is available via the
// view command's --full-uuid flag (RenderViewFull / TASK-0043).
const shortUUIDLen = 13

// shortUUID returns a recognizable prefix of a device UUID for the default
// render: the first shortUUIDLen runes followed by a single-rune ellipsis when
// the UUID is longer. Short UUIDs (e.g. the test fixtures "GPU-idle") are
// returned unchanged so nothing is truncated that already fits. It is
// rune-aware so it never splits a multi-byte rune. This affects DISPLAY ONLY —
// no value/semantics change (TASK-0043).
func shortUUID(uuid string) string {
	r := []rune(uuid)
	if len(r) <= shortUUIDLen {
		return uuid
	}
	return string(r[:shortUUIDLen]) + "…"
}

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
//
// CollectedAt / AgeSeconds / Stale / StaleReason are the data-freshness fields
// (TASK-0040), reported by the agent at the TOP level (freshness is a property of
// the whole window, not one device). The viewer renders a data-age line and, when
// Stale is true, a prominent STALE marker + the agent's reason — it does NOT
// recompute staleness, it passes the agent's verdict through verbatim (the agent
// owns the threshold; cli stays a read-only viewer). When fresh, Stale is false
// and StaleReason empty. These are an ADDITIVE top-level extension; the per-device
// DeviceCost/JobCost DTOs are unchanged, so the committed cost golden still
// decodes (it simply omits these and they stay zero/false).
type CostResponse struct {
	Devices     []DeviceCost `json:"devices"`
	Jobs        []JobCost    `json:"jobs"`
	CollectedAt time.Time    `json:"collected_at,omitempty"`
	AgeSeconds  float64      `json:"age_seconds"`
	Stale       bool         `json:"stale"`
	// NeverCollected (TASK-0041) is the agent's MOST-stale state: it has not once
	// successfully scraped metrics (e.g. the exporter was unreachable from
	// startup), so /cost returns 200 with empty devices, stale=true,
	// never_collected=true, and a reason. The viewer renders a clear human message
	// ("agent has not collected data yet — <reason>") rather than a blank table.
	NeverCollected bool   `json:"never_collected"`
	StaleReason    string `json:"stale_reason,omitempty"`
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
		// Return a TYPED status error carrying the body so callers (Cost) can
		// distinguish an agent that answered non-200 (e.g. a never-collected 503 from
		// an older agent) from a transport failure, and degrade gracefully instead of
		// bubbling a raw HTTP error to the user (TASK-0041).
		return nil, &statusError{path: path, code: resp.StatusCode, body: body}
	}
	return body, nil
}

// statusError is a non-200 HTTP response from the agent's local API. It carries
// the response body so a caller can try to decode it (e.g. a never-collected
// /cost) before falling back to a human message.
type statusError struct {
	path string
	code int
	body []byte
}

func (e *statusError) Error() string {
	return fmt.Sprintf("cli: GET %s: status %d: %s", e.path, e.code, e.text())
}

// text is the trimmed response body, used as a human reason when the body is not
// structured JSON.
func (e *statusError) text() string { return strings.TrimSpace(string(e.body)) }

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
//
// Never-collected resilience (TASK-0041): the agent serves never-collected as a
// 200 JSON body (stale + never_collected + reason), which decodes normally. But
// an OLDER agent (or any non-200 from /cost) MUST still degrade to a clear empty
// state, not a raw HTTP error: when get() returns a status error whose body is a
// decodable CostResponse, use it; otherwise synthesize a NeverCollected response
// carrying the agent's error text as the reason. Either way RenderView prints a
// human message, never a bubbled raw HTTP error and never a silent blank.
func (c *Client) Cost(ctx context.Context) (*CostResponse, error) {
	body, err := c.get(ctx, "/cost")
	if err != nil {
		var serr *statusError
		if errors.As(err, &serr) {
			// The agent answered but not 200 (e.g. an older agent's 503 "no signal
			// window collected yet"). Treat it as a never-collected empty state.
			var cost CostResponse
			if jerr := json.Unmarshal(serr.body, &cost); jerr == nil && (cost.NeverCollected || len(cost.Devices) == 0) {
				cost.NeverCollected = true
				cost.Stale = true
				if cost.StaleReason == "" {
					cost.StaleReason = serr.text()
				}
				return &cost, nil
			}
			return &CostResponse{
				NeverCollected: true,
				Stale:          true,
				StaleReason:    serr.text(),
			}, nil
		}
		// A transport error (agent down / connection refused) is NOT a never-collected
		// agent — return it so the caller surfaces "cannot reach the agent".
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
//
// The default render shows each device UUID as a recognizable SHORT prefix so
// the DEVICES/JOBS columns stay narrow and aligned (TASK-0043). Use
// RenderViewFull for the whole UUID (the view command's --full-uuid flag).
func RenderView(pack *gpufleetv1.EvidencePack, cost *CostResponse) string {
	return renderView(pack, cost, false)
}

// RenderViewFull is RenderView but prints the FULL device UUID instead of the
// short prefix. It backs the view command's --full-uuid flag (TASK-0043). It
// changes DISPLAY ONLY — every value/semantic and the deterministic order are
// identical to RenderView.
func RenderViewFull(pack *gpufleetv1.EvidencePack, cost *CostResponse) string {
	return renderView(pack, cost, true)
}

// renderView is the shared renderer. fullUUID selects the long vs short UUID
// display; everything else (values, banners, order, columns) is identical.
func renderView(pack *gpufleetv1.EvidencePack, cost *CostResponse, fullUUID bool) string {
	var b strings.Builder

	// Defensive: a nil cost means the caller had no cost payload at all. Treat it as
	// the never-collected empty state rather than panicking (TASK-0041).
	if cost == nil {
		cost = &CostResponse{NeverCollected: true, Stale: true,
			StaleReason: "agent returned no cost data"}
	}

	agentID := ""
	if pack != nil {
		agentID = pack.GetAgentId()
	}
	fmt.Fprintf(&b, "gpufleet single-node view  (agent=%s, source=local read-only API)\n", agentID)

	// NEVER-COLLECTED empty state (TASK-0041): the agent has not once successfully
	// scraped metrics (exporter unreachable from startup, etc.), so there are no
	// device values to show. Render a CLEAR human message — never a raw HTTP error,
	// never a silently blank table — and stop. This also covers the defensive case
	// where /cost returned no devices while flagged stale (an older agent's empty
	// 503 body decoded into NeverCollected by Client.Cost).
	if cost.NeverCollected || (cost.Stale && len(cost.Devices) == 0) {
		reason := cost.StaleReason
		if reason == "" {
			reason = "agent has not collected any metrics yet (exporter unreachable?)"
		}
		fmt.Fprintf(&b, "*** NO DATA ***  the agent has not collected any GPU metrics yet\n")
		fmt.Fprintf(&b, "reason: %s\n", reason)
		fmt.Fprintf(&b, "the exporter may be unreachable, or the agent just started — retry shortly.\n")
		return b.String()
	}

	// Data-freshness line (TASK-0040): the agent-side age since its last SUCCESSFUL
	// collection, passed through verbatim from /cost (deterministic — no cli
	// wall-clock). When the agent reports stale, surface a prominent STALE marker +
	// the agent's reason so a held-stale window is NEVER shown as current (RULES
	// §B). The data values below are the last-known window, kept but explicitly
	// flagged — not blanked, not fabricated.
	fmt.Fprintf(&b, "data age: %.1fs", cost.AgeSeconds)
	if cost.Stale {
		fmt.Fprintf(&b, "   *** STALE ***")
		if cost.StaleReason != "" {
			fmt.Fprintf(&b, "  (%s)", cost.StaleReason)
		}
	}
	fmt.Fprintf(&b, "\n")
	if cost.Stale {
		fmt.Fprintf(&b, "NOTE: data below is STALE (last-known values held, NOT live) — do not treat as current.\n")
	}

	// Deterministic device table, sorted by UUID.
	devs := append([]DeviceCost(nil), cost.Devices...)
	sort.Slice(devs, func(i, j int) bool { return devs[i].UUID < devs[j].UUID })

	// DEVICES table, rendered through text/tabwriter so columns auto-size to
	// their content and the header lines up with every row even when a device
	// UUID is the full 36-char NVML id (TASK-0043). Text columns are left-aligned
	// (the tabwriter default); numeric columns are pre-justified right so they
	// align on their right edge under right-justified headers (mixedTable does
	// the per-column padding before tabwriter adds the inter-column gap).
	//
	// Two distinct money columns straight from the agent's /cost wire (agent
	// CLAUDE.md §5a): `waste(win)` is the per-WINDOW waste (`wasted_usd`) and
	// `$/hr` is the per-HOUR burn RATE (`usd_per_hour`, semantics
	// CostImpact.UsdPerHour). For an idle device over a sub-hour window the $/hr
	// rate exceeds the windowed waste. cli passes both through verbatim; when the
	// agent reports priced==false (no $/hr rate) it shows a degrade mark `n/a`,
	// NEVER a fabricated $0. Switching to tabwriter changes LAYOUT ONLY — no
	// value/semantic, sort, banner, or verdict changes.
	fmt.Fprintf(&b, "\nDEVICES\n")
	devTable := newTable(
		[]string{"device", "node", "mfu", "tensor", "waste(win)", "$/hr", "lowutil", "verdict"},
		[]bool{false, false, true, true, true, true, false, false}, // numeric cols right-aligned
	)
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
		uuid := d.UUID
		if !fullUUID {
			uuid = shortUUID(uuid)
		}
		devTable.row(
			uuid, d.Node,
			fmt.Sprintf("%.3f", d.MFU), fmt.Sprintf("%.3f", d.TensorActive),
			wasted, perHour, low, "n/a (no control plane)",
		)
	}
	devTable.flush(&b)

	// Deterministic job table, sorted by job id.
	jobs := append([]JobCost(nil), cost.Jobs...)
	sort.Slice(jobs, func(i, j int) bool { return jobs[i].JobID < jobs[j].JobID })
	if len(jobs) > 0 {
		fmt.Fprintf(&b, "\nJOBS\n")
		jobTable := newTable(
			[]string{"job", "waste(win)", "$/hr", "priced", "devices"},
			[]bool{false, true, true, false, true},
		)
		for _, j := range jobs {
			wasted := fmt.Sprintf("$%.4f", j.WastedUSD)
			perHour := fmt.Sprintf("$%.4f", j.UsdPerHour)
			priced := "yes"
			if !j.Priced {
				wasted = "n/a"
				perHour = "n/a"
				priced = "no"
			}
			jobTable.row(j.JobID, wasted, perHour, priced, fmt.Sprintf("%d", j.Devices))
		}
		jobTable.flush(&b)
	}

	// Missing-field degrade marks, passed through from the agent's own state. The
	// degrade marks still carry the FULL UUID regardless of fullUUID: they are a
	// diagnostic cross-check list, not the at-a-glance table, so the complete id
	// stays unambiguous here.
	marks := degradeMarks(pack, cost)
	if len(marks) > 0 {
		fmt.Fprintf(&b, "\nDEGRADED (missing-field marks from agent)\n")
		degTable := newTable(
			[]string{"device", "field", "reason"},
			[]bool{false, false, false},
		)
		for _, m := range marks {
			degTable.row(m.DeviceUUID, m.Field, m.Reason)
		}
		degTable.flush(&b)
	}

	return b.String()
}

// table accumulates rows for a single aligned block and renders them through
// text/tabwriter (TASK-0043). Columns flagged numeric are right-justified: each
// numeric cell (and its header) is left-padded to the column's widest content
// BEFORE tabwriter runs, so numbers align on their right edge while tabwriter
// supplies a uniform inter-column gap; text columns are left as-is and
// left-aligned by tabwriter's default. Rendering is order-preserving and
// deterministic — same rows in, byte-identical block out.
type table struct {
	headers []string
	numeric []bool
	rows    [][]string
	width   []int // running max display width per column (header + all cells)
}

func newTable(headers []string, numeric []bool) *table {
	t := &table{headers: headers, numeric: numeric, width: make([]int, len(headers))}
	for i, h := range headers {
		t.width[i] = dispWidth(h)
	}
	return t
}

func (t *table) row(cells ...string) {
	for i, c := range cells {
		if w := dispWidth(c); w > t.width[i] {
			t.width[i] = w
		}
	}
	t.rows = append(t.rows, cells)
}

// flush writes the header + rows to w as a tab-separated, tabwriter-aligned
// block (2-space leading indent, 2-space minimum column gap). Numeric cells are
// right-justified within their column width first so they share a right edge.
func (t *table) flush(w io.Writer) {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, t.line(t.headers))
	for _, r := range t.rows {
		fmt.Fprintln(tw, t.line(r))
	}
	_ = tw.Flush()
}

// line joins one record into a leading-indented, tab-separated row, right-
// justifying numeric cells to their column width so they align on the right.
func (t *table) line(cells []string) string {
	out := make([]string, len(cells))
	for i, c := range cells {
		if t.numeric[i] {
			c = pad(c, t.width[i])
		}
		out[i] = c
	}
	return "  " + strings.Join(out, "\t")
}

// pad left-pads s with spaces to width w (right-justify); rune-aware so
// multi-byte content is measured by display runes, not bytes.
func pad(s string, w int) string {
	n := w - dispWidth(s)
	if n <= 0 {
		return s
	}
	return strings.Repeat(" ", n) + s
}

// dispWidth is the rune count of s — the width tabwriter itself uses for ASCII /
// single-width content (the only content these tables carry).
func dispWidth(s string) int { return len([]rune(s)) }
