package cli

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	gpufleetv1 "github.com/rocker-zhang/gpufleet-proto/gen/go/gpufleet/v1"
	"google.golang.org/protobuf/encoding/protojson"
)

// longUUIDAgentStub stands up an httptest agent whose /signals + /cost carry the
// four full 36-char NVML UUIDs — the realistic input that misaligned the old
// fixed-width render (TASK-0043). /signals maps all four so no degrade marks fire
// for the priced devices; the unpriced one yields the normal cost degrade mark.
func longUUIDAgentStub(t *testing.T) *httptest.Server {
	t.Helper()
	pack := &gpufleetv1.EvidencePack{
		ContractVersion: "v1",
		AgentId:         "agent-longuuid",
		Mappings: []*gpufleetv1.DeviceJobMapping{
			{DeviceUuid: "GPU-1e760802-aaaa-bbbb-cccc-0123456789ab", Node: "node-a", JobId: "job-train-alpha"},
			{DeviceUuid: "GPU-2f871913-dddd-eeee-ffff-1234567890bc", Node: "node-a", JobId: "job-train-alpha"},
			{DeviceUuid: "GPU-3a982a24-1111-2222-3333-2345678901cd", Node: "node-b", JobId: "job-eval-bravo"},
			{DeviceUuid: "GPU-4b093b35-4444-5555-6666-3456789012de", Node: "node-b", JobId: "job-eval-bravo"},
		},
	}
	sig, err := protojson.Marshal(pack)
	if err != nil {
		t.Fatalf("marshal EvidencePack: %v", err)
	}
	costJSON := `{
	  "devices": [
	    {"uuid":"GPU-1e760802-aaaa-bbbb-cccc-0123456789ab","node":"node-a","mfu":0.62,"tensor_active":0.70,"idle_fraction":0.05,"cost_usd":0.02,"wasted_usd":0.0,"usd_per_hour":0.0,"priced":true,"low_utilization":false},
	    {"uuid":"GPU-2f871913-dddd-eeee-ffff-1234567890bc","node":"node-a","mfu":0.01,"tensor_active":0.02,"idle_fraction":0.95,"cost_usd":0.02,"wasted_usd":0.0190,"usd_per_hour":1.1400,"priced":true,"low_utilization":true},
	    {"uuid":"GPU-3a982a24-1111-2222-3333-2345678901cd","node":"node-b","mfu":0.45,"tensor_active":0.51,"idle_fraction":0.30,"cost_usd":0.02,"wasted_usd":0.0033,"usd_per_hour":0.2100,"priced":true,"low_utilization":false},
	    {"uuid":"GPU-4b093b35-4444-5555-6666-3456789012de","node":"node-b","mfu":0.0,"tensor_active":0.0,"idle_fraction":1.0,"cost_usd":0.0,"wasted_usd":0.0,"usd_per_hour":0.0,"priced":false,"low_utilization":true}
	  ],
	  "jobs": [
	    {"job_id":"job-train-alpha","wasted_usd":0.0190,"usd_per_hour":1.1400,"priced":true,"devices":2},
	    {"job_id":"job-eval-bravo","wasted_usd":0.0033,"usd_per_hour":0.2100,"priced":true,"devices":1}
	  ],
	  "age_seconds": 3.0,
	  "stale": false
	}`
	mux := http.NewServeMux()
	mux.HandleFunc("/signals", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(sig)
	})
	mux.HandleFunc("/cost", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(costJSON))
	})
	return httptest.NewServer(mux)
}

// longUUIDCost builds a /cost payload whose devices carry full 36-char NVML
// UUIDs ("GPU-" + 8-4-4-4-12 hex = 40 chars) — exactly the input that misaligned
// the fixed-width fmt render before TASK-0043. Four devices with distinct prefix
// hex blocks so the short-UUID prefix is still recognizable per device.
func longUUIDCost() *CostResponse {
	return &CostResponse{
		AgeSeconds: 3.0,
		Devices: []DeviceCost{
			{UUID: "GPU-1e760802-aaaa-bbbb-cccc-0123456789ab", Node: "node-a", MFU: 0.62, TensorActive: 0.70, WastedUSD: 0.0, UsdPerHour: 0.0, Priced: true, LowUtilization: false},
			{UUID: "GPU-2f871913-dddd-eeee-ffff-1234567890bc", Node: "node-a", MFU: 0.01, TensorActive: 0.02, WastedUSD: 0.0190, UsdPerHour: 1.1400, Priced: true, LowUtilization: true},
			{UUID: "GPU-3a982a24-1111-2222-3333-2345678901cd", Node: "node-b", MFU: 0.45, TensorActive: 0.51, WastedUSD: 0.0033, UsdPerHour: 0.2100, Priced: true, LowUtilization: false},
			{UUID: "GPU-4b093b35-4444-5555-6666-3456789012de", Node: "node-b", MFU: 0.00, TensorActive: 0.00, WastedUSD: 0.0, UsdPerHour: 0.0, Priced: false, LowUtilization: true},
		},
		Jobs: []JobCost{
			{JobID: "job-train-alpha", WastedUSD: 0.0190, UsdPerHour: 1.1400, Priced: true, Devices: 2},
			{JobID: "job-eval-bravo", WastedUSD: 0.0033, UsdPerHour: 0.2100, Priced: true, Devices: 1},
		},
	}
}

// section returns the lines of the named block (e.g. "DEVICES" / "JOBS"),
// starting at the line AFTER the header keyword, up to the next blank line.
func section(out, header string) []string {
	lines := strings.Split(out, "\n")
	var rows []string
	in := false
	for _, ln := range lines {
		if strings.TrimSpace(ln) == header {
			in = true
			continue
		}
		if in {
			if strings.TrimSpace(ln) == "" {
				break
			}
			rows = append(rows, ln)
		}
	}
	return rows
}

// field is one tabwriter cell with its [start,end) character span on the line.
type field struct {
	text       string
	start, end int
}

// fields splits a rendered tabwriter line into cells on runs of >=2 spaces
// (tabwriter's minimum inter-column gap here is padding=2), so single spaces
// INSIDE a cell — e.g. "n/a (no control plane)" — keep the cell intact. Each
// field records the offset of its first and last non-space rune, giving the
// column's left edge (start) and right edge (end). Two columns are "aligned"
// when their left edges match (left-aligned text) or their right edges match
// (right-justified numerics).
func fields(line string) []field {
	r := []rune(line)
	var fs []field
	i := 0
	for i < len(r) {
		// skip a gap (>=2 spaces, or the leading indent)
		for i < len(r) && r[i] == ' ' {
			i++
		}
		if i >= len(r) {
			break
		}
		start := i
		end := i
		for i < len(r) {
			if r[i] == ' ' {
				// a single internal space stays in the cell; >=2 ends the cell.
				if i+1 < len(r) && r[i+1] == ' ' {
					break
				}
			}
			end = i
			i++
		}
		fs = append(fs, field{text: string(r[start : end+1]), start: start, end: end})
	}
	return fs
}

// assertAligned checks every data row against the header for a tabwriter block:
// left-aligned (text) columns share their LEFT edge with the header; numeric
// columns share their RIGHT edge. A mismatch means a value does not sit under
// its header. It also asserts the cell count matches so a multi-word verdict
// cell (kept intact by fields()) does not silently split.
func assertAligned(t *testing.T, block string, rows []string, numeric map[int]bool) {
	t.Helper()
	if len(rows) < 2 {
		t.Fatalf("%s: expected a header + at least one data row:\n%v", block, rows)
	}
	hf := fields(rows[0])
	for _, dr := range rows[1:] {
		df := fields(dr)
		if len(df) != len(hf) {
			t.Fatalf("%s: data row has %d cells, header has %d\nheader: %q\nrow:    %q",
				block, len(df), len(hf), rows[0], dr)
		}
		for c := range hf {
			if numeric[c] {
				if df[c].end != hf[c].end {
					t.Errorf("%s col %d (numeric) right edges differ: header %d, row %d\nheader: %q\nrow:    %q",
						block, c, hf[c].end, df[c].end, rows[0], dr)
				}
			} else {
				if df[c].start != hf[c].start {
					t.Errorf("%s col %d (text) left edges differ: header %d, row %d\nheader: %q\nrow:    %q",
						block, c, hf[c].start, df[c].start, rows[0], dr)
				}
			}
		}
	}
}

// devNumeric / jobNumeric mirror the numeric-column flags passed to newTable in
// view.go so the test asserts the SAME alignment contract the renderer promises.
var devNumeric = map[int]bool{2: true, 3: true, 4: true, 5: true} // mfu,tensor,waste,$/hr
var jobNumeric = map[int]bool{1: true, 2: true, 4: true}          // waste,$/hr,devices

// TestDevicesColumnsAlignUnderHeaders is the core TASK-0043 alignment assertion:
// with full 36-char UUIDs (the input that previously misaligned the table), the
// tabwriter render puts every DEVICES value under its header — text columns
// share the header's left edge, numeric columns share its right edge.
func TestDevicesColumnsAlignUnderHeaders(t *testing.T) {
	out := RenderView(&gpufleetv1.EvidencePack{AgentId: "align"}, longUUIDCost(), nil)
	assertAligned(t, "DEVICES", section(out, "DEVICES"), devNumeric)
}

// TestJobsColumnsAlignUnderHeaders mirrors the DEVICES check for the JOBS block.
func TestJobsColumnsAlignUnderHeaders(t *testing.T) {
	out := RenderView(&gpufleetv1.EvidencePack{AgentId: "align"}, longUUIDCost(), nil)
	assertAligned(t, "JOBS", section(out, "JOBS"), jobNumeric)
}

// TestShortUUIDDefault proves the default render shows a recognizable SHORT
// prefix (first 13 chars + ellipsis), NOT the full 36-char UUID, in the DEVICES
// table — while the prefix still uniquely identifies each device.
func TestShortUUIDDefault(t *testing.T) {
	out := RenderView(&gpufleetv1.EvidencePack{AgentId: "short"}, longUUIDCost(), nil)

	full := "GPU-1e760802-aaaa-bbbb-cccc-0123456789ab"
	prefix := "GPU-1e760802-" // 13 chars
	devRows := section(out, "DEVICES")
	body := strings.Join(devRows, "\n")

	if strings.Contains(body, full) {
		t.Errorf("default DEVICES render must NOT show the full UUID:\n%s", body)
	}
	if !strings.Contains(body, prefix+"…") {
		t.Errorf("default render must show the short prefix %q + ellipsis:\n%s", prefix, body)
	}
	// All four device prefixes are present and distinct (recognizable).
	for _, p := range []string{"GPU-1e760802-", "GPU-2f871913-", "GPU-3a982a24-", "GPU-4b093b35-"} {
		if !strings.Contains(body, p+"…") {
			t.Errorf("missing recognizable short prefix %q:\n%s", p, body)
		}
	}
}

// TestFullUUIDOptShowsWholeUUID proves the --full-uuid path (RenderViewFull)
// renders the entire UUID and no ellipsis, and that columns STILL align under
// their headers with the wider content.
func TestFullUUIDOptShowsWholeUUID(t *testing.T) {
	out := RenderViewFull(&gpufleetv1.EvidencePack{AgentId: "full"}, longUUIDCost(), nil)

	full := "GPU-1e760802-aaaa-bbbb-cccc-0123456789ab"
	if !strings.Contains(out, full) {
		t.Errorf("--full-uuid render must show the whole UUID:\n%s", out)
	}
	if strings.Contains(out, "…") {
		t.Errorf("--full-uuid render must NOT truncate with an ellipsis:\n%s", out)
	}

	// Columns still align under their headers with the wider full-UUID content.
	assertAligned(t, "DEVICES(full)", section(out, "DEVICES"), devNumeric)
}

// TestNumericColumnsRightAligned asserts the numeric money columns share a right
// edge: in the DEVICES block the two $-columns' values end at the same offset as
// (or aligned with) their header, which under tabwriter means equal column ends.
// We check that two data rows with different-width numeric values still have the
// SAME field starts for the numeric columns — only possible if they are
// right-justified to a common width.
func TestNumericColumnsRightAligned(t *testing.T) {
	out := RenderView(&gpufleetv1.EvidencePack{AgentId: "num"}, longUUIDCost(), nil)
	rows := section(out, "DEVICES")
	if len(rows) < 3 {
		t.Fatalf("need multiple data rows:\n%s", out)
	}
	// The money columns mix "$0.0000" (7 chars) and "n/a" (3 chars) across rows;
	// right-justification means those differing-width cells must still END at the
	// same offset. Compare every numeric column's right edge across two rows.
	f1 := fields(rows[1])
	f2 := fields(rows[2])
	if len(f1) != len(f2) {
		t.Fatalf("rows have differing cell counts:\n%q\n%q", rows[1], rows[2])
	}
	for c := range f1 {
		if devNumeric[c] && f1[c].end != f2[c].end {
			t.Errorf("numeric column %d not right-aligned across rows (right edge %d vs %d):\n%q\n%q",
				c, f1[c].end, f2[c].end, rows[1], rows[2])
		}
	}
	// Sanity: at least one numeric column actually mixes widths (n/a vs $...).
	if !strings.Contains(out, "n/a") || !strings.Contains(out, "$0.0000") {
		t.Fatalf("test fixture should mix n/a and $ cells to exercise right-align:\n%s", out)
	}
}

// TestShortUUIDStillOnStdout locks that the new short-UUID tabwriter render is
// still the command's PRIMARY output on STDOUT (TASK-0042 not regressed): the
// DEVICES table with short prefixes lands on stdout, stderr stays empty.
func TestShortUUIDStillOnStdout(t *testing.T) {
	srv := longUUIDAgentStub(t)
	defer srv.Close()

	stdout, stderr, err := runView(t, srv.URL)
	if err != nil {
		t.Fatalf("view error on long-UUID agent: %v", err)
	}
	if !strings.Contains(stdout, "DEVICES") {
		t.Errorf("DEVICES header must be on STDOUT:\n%s", stdout)
	}
	if !strings.Contains(stdout, "GPU-1e760802-…") {
		t.Errorf("short-prefix device row must be on STDOUT:\n%s", stdout)
	}
	if strings.Contains(stdout, "GPU-1e760802-aaaa-bbbb-cccc-0123456789ab") {
		t.Errorf("default STDOUT render must NOT show the full UUID:\n%s", stdout)
	}
	if strings.TrimSpace(stderr) != "" {
		t.Errorf("STDERR must be empty on a successful view:\n%s", stderr)
	}
}

// TestFullUUIDFlagStillOnStdout drives the real cobra command with --full-uuid
// and asserts the whole UUID renders on STDOUT (the flag is wired and does not
// regress stream separation).
func TestFullUUIDFlagStillOnStdout(t *testing.T) {
	srv := longUUIDAgentStub(t)
	defer srv.Close()

	stdout, stderr, err := runViewArgs(t, "view", "--endpoint", srv.URL, "--full-uuid")
	if err != nil {
		t.Fatalf("view --full-uuid error: %v", err)
	}
	if !strings.Contains(stdout, "GPU-1e760802-aaaa-bbbb-cccc-0123456789ab") {
		t.Errorf("--full-uuid must render the whole UUID on STDOUT:\n%s", stdout)
	}
	if strings.Contains(stdout, "…") {
		t.Errorf("--full-uuid must not show an ellipsis on STDOUT:\n%s", stdout)
	}
	if strings.TrimSpace(stderr) != "" {
		t.Errorf("STDERR must be empty on a successful --full-uuid view:\n%s", stderr)
	}
}

// TestAlignRenderDeterministic locks determinism of the tabwriter render.
func TestAlignRenderDeterministic(t *testing.T) {
	pack := &gpufleetv1.EvidencePack{AgentId: "det"}
	c := longUUIDCost()
	if a, b := RenderView(pack, c, nil), RenderView(pack, c, nil); a != b {
		t.Errorf("tabwriter render not deterministic")
	}
	if a, b := RenderViewFull(pack, c, nil), RenderViewFull(pack, c, nil); a != b {
		t.Errorf("full-uuid tabwriter render not deterministic")
	}
}
